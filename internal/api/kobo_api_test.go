package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	_ "image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/api"
	"github.com/rdborg/mediarium/internal/books"
	"github.com/rdborg/mediarium/internal/kobo"
)

// These tests play the part of a Kobo e-reader. There is no Kobo here to try
// it on, so the requests and answers follow what Kobo's store protocol is
// known to look like (the shapes Calibre-Web, Komga and kobo-shelf use with
// real devices). The "store" is a small fake that answers the way Kobo's
// servers are documented to.

type storeCall struct {
	Method, Path, Auth, SyncToken, Body string
}

// fakeKoboStore stands in for storeapi.kobo.com.
type fakeKoboStore struct {
	*httptest.Server
	mu    sync.Mutex
	calls []storeCall
}

func newFakeKoboStore(t *testing.T) *fakeKoboStore {
	t.Helper()
	f := &fakeKoboStore{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, storeCall{r.Method, r.URL.Path + "?" + r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("x-kobo-synctoken"), string(data)})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/initialization":
			_, _ = io.WriteString(w, `{"Resources":{"oauth_host":"https://oauth.kobo.example","library_sync":"https://storeapi.kobo.com/v1/library/sync","store_host":"store.example","image_host":"//cdn.kobo.com/book-images/"}}`)
		case r.URL.Path == "/v1/auth/device":
			_, _ = io.WriteString(w, `{"AccessToken":"real-access","RefreshToken":"real-refresh","TokenType":"Bearer"}`)
		case r.URL.Path == "/v1/user/profile":
			_, _ = io.WriteString(w, `{"UserId":"store-user"}`)
		case r.URL.Path == "/v1/library/sync":
			if r.Header.Get("x-kobo-synctoken") == "store.one" {
				w.Header().Set("x-kobo-synctoken", "store.two")
				_, _ = io.WriteString(w, `[]`)
				return
			}
			w.Header().Set("x-kobo-synctoken", "store.one")
			w.Header().Set("x-kobo-sync-mode", "delta")
			_, _ = io.WriteString(w, `[{"NewEntitlement":{"BookEntitlement":{"Id":"store-book-1"}}}]`)
		case strings.HasPrefix(r.URL.Path, "/v1/library/store-book-1"):
			_, _ = io.WriteString(w, `[{"EntitlementId":"store-book-1"}]`)
		default:
			http.Error(w, `{"error":"nope"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeKoboStore) seen() []storeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]storeCall(nil), f.calls...)
}

// koboDevice is the Kobo: it knows its api_endpoint, keeps the sync token and
// signs in with a Kobo token of its own (which Mediarium must never keep).
type koboDevice struct {
	t        *testing.T
	client   *http.Client
	endpoint string // api_endpoint: base + /kobo/<token>
	token    string // the sync token header it holds
	bearer   string // its Kobo account token; "" = not signed in to Kobo
}

func (d *koboDevice) request(method, path string, payload any) (*http.Response, []byte) {
	d.t.Helper()
	var rd io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, d.endpoint+path, rd)
	if err != nil {
		d.t.Fatal(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; U; Android 2.0; en-us;) AppleWebKit/533.1 (KHTML, like Gecko) Version/4.0 Mobile Safari/533.1 (Kobo Touch 0391/4.38.23171)")
	if d.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+d.bearer)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		d.t.Fatal(err)
	}
	return resp, erBody(d.t, resp)
}

type syncEntries []map[string]json.RawMessage

// sync asks for the library the way a Kobo does and keeps the token it is given.
func (d *koboDevice) sync() (syncEntries, *http.Response) {
	d.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, d.endpoint+"/v1/library/sync", nil)
	if d.token != "" {
		req.Header.Set("x-kobo-synctoken", d.token)
	}
	if d.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+d.bearer)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		d.t.Fatal(err)
	}
	data := erBody(d.t, resp)
	if resp.StatusCode != http.StatusOK {
		d.t.Fatalf("sync: %d %s", resp.StatusCode, data)
	}
	if tok := resp.Header.Get("x-kobo-synctoken"); tok != "" {
		d.token = tok
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		d.t.Fatal("an empty sync must answer [], not null")
	}
	var entries syncEntries
	if err := json.Unmarshal(data, &entries); err != nil {
		d.t.Fatalf("sync answer is not an array of objects: %v\n%s", err, data)
	}
	return entries, resp
}

func (e syncEntries) kinds() []string {
	var out []string
	for _, m := range e {
		for k := range m {
			out = append(out, k)
		}
	}
	return out
}

// find returns the inner object of the first entry of this kind for this book title.
func (e syncEntries) titled(kind, title string) map[string]json.RawMessage {
	for _, m := range e {
		raw, ok := m[kind]
		if !ok {
			continue
		}
		var inner map[string]json.RawMessage
		_ = json.Unmarshal(raw, &inner)
		var meta struct{ Title string }
		_ = json.Unmarshal(inner["BookMetadata"], &meta)
		if meta.Title == title {
			return inner
		}
	}
	return nil
}

func (e syncEntries) states(kind string) []map[string]any {
	var out []map[string]any
	for _, m := range e {
		if raw, ok := m[kind]; ok {
			var inner struct{ ReadingState map[string]any }
			_ = json.Unmarshal(raw, &inner)
			out = append(out, inner.ReadingState)
		}
	}
	return out
}

func newKoboFixture(t *testing.T) (*ereaderFixture, *koboDevice, *fakeKoboStore) {
	t.Helper()
	f := newEReaderFixture(t)
	store := newFakeKoboStore(t)
	f.server.TestSetKoboStore(store.URL)
	access := postJSON[ereaderAccess](t, f.admin, f.base+"/api/auth/ereader/kobo", nil, http.StatusOK)
	if !access.Kobo.On || !strings.HasPrefix(access.Kobo.Path, "/kobo/") || len(access.Kobo.Path) != len("/kobo/")+64 {
		t.Fatalf("kobo address: %+v", access.Kobo)
	}
	dev := &koboDevice{t: t, client: erClient(), endpoint: f.base + access.Kobo.Path}
	return f, dev, store
}

func TestKoboSyncFlow(t *testing.T) {
	f, dev, _ := newKoboFixture(t)
	hobbitUUID, duneUUID := kobo.BookUUID(f.hobbit.ID), kobo.BookUUID(f.dune.ID)

	// 1. The Kobo asks where everything is. With the store unreachable
	// (nothing signed in), Mediarium answers with the built-in list and its own
	// library and cover addresses.
	f.server.TestSetKoboStore("http://127.0.0.1:1")
	resp, data := dev.request(http.MethodGet, "/v1/initialization", nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("x-kobo-apitoken") != "e30=" {
		t.Fatalf("init: %d %v", resp.StatusCode, resp.Header)
	}
	res := erDecode[struct{ Resources map[string]any }](t, data).Resources
	if res["library_sync"] != dev.endpoint+"/v1/library/sync" || res["image_url_template"] != dev.endpoint+"/{ImageId}/{Width}/{Height}/false/image.jpg" {
		t.Fatalf("resources: %v / %v", res["library_sync"], res["image_url_template"])
	}
	if res["oauth_host"] == nil || len(res) < 100 {
		t.Fatalf("the built-in store list is missing: %d keys", len(res))
	}

	// 2. First sync (no token): both books, each as a new entitlement with
	// metadata the Kobo store would send, a KEPUB download and no reading state.
	entries, resp := dev.sync()
	if len(entries) != 2 || resp.Header.Get("x-kobo-sync") != "" || dev.token == "" {
		t.Fatalf("first sync: %v continue=%q token=%q", entries.kinds(), resp.Header.Get("x-kobo-sync"), dev.token)
	}
	hobbit := entries.titled("NewEntitlement", "The Hobbit")
	if hobbit == nil || entries.titled("NewEntitlement", "Dune") == nil {
		t.Fatalf("both books should be new: %v", entries)
	}
	var ent struct {
		Id, Status, Accessibility string
		IsRemoved                 bool
	}
	_ = json.Unmarshal(hobbit["BookEntitlement"], &ent)
	if ent.Id != hobbitUUID || ent.Status != "Active" || ent.Accessibility != "Full" || ent.IsRemoved {
		t.Fatalf("entitlement: %+v", ent)
	}
	var meta struct {
		Title, EntitlementId, CoverImageId, Description string
		Contributors                                    []string
		DownloadUrls                                    []struct {
			Format, Url, Platform string
			Size                  int64
		}
		Series struct {
			Name string
			Id   string
		}
	}
	_ = json.Unmarshal(hobbit["BookMetadata"], &meta)
	if meta.EntitlementId != hobbitUUID || meta.CoverImageId != hobbitUUID || len(meta.Contributors) != 1 || meta.Contributors[0] != "J.R.R. Tolkien" ||
		meta.Series.Name != "Middle-earth" || len(meta.DownloadUrls) != 1 || meta.DownloadUrls[0].Format != "KEPUB" || meta.DownloadUrls[0].Size <= 0 {
		t.Fatalf("metadata: %+v", meta)
	}
	if hobbit["ReadingState"] != nil {
		t.Errorf("a book never opened has no reading state")
	}

	// 3. The KEPUB download is a real KEPUB: an EPUB whose text is wrapped in
	// the koboSpan markup that gives the Kobo pages and positions.
	dl := meta.DownloadUrls[0].Url
	if !strings.HasPrefix(dl, dev.endpoint+"/download/"+hobbitUUID+"/") {
		t.Fatalf("download address: %s", dl)
	}
	dresp, kepub := erGet(t, dl)
	if dresp.StatusCode != http.StatusOK || dresp.Header.Get("Content-Type") != "application/epub+zip" {
		t.Fatalf("download: %d %s", dresp.StatusCode, dresp.Header.Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(kepub), int64(len(kepub)))
	if err != nil {
		t.Fatalf("the download is not a zip: %v", err)
	}
	var ch1 string
	for _, zf := range zr.File {
		if strings.HasSuffix(zf.Name, "ch1.xhtml") {
			rc, _ := zf.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			ch1 = string(b)
		}
	}
	if !strings.Contains(ch1, `class="koboSpan"`) || !strings.Contains(ch1, `id="kobo.1.1"`) || !strings.Contains(ch1, "hole in the ground") {
		t.Fatalf("not a KEPUB: %.400s", ch1)
	}
	// Made once, kept: the second download is the same file.
	if _, again := erGet(t, dl); !bytes.Equal(again, kepub) {
		t.Error("a second download differs")
	}
	// The library file itself was not touched.
	orig, _ := os.ReadFile(f.hobbit.EbookPath)
	if bytes.Contains(orig, []byte("koboSpan")) {
		t.Error("the library file was changed")
	}
	// A Range request works (the Kobo resumes downloads).
	req, _ := http.NewRequest(http.MethodGet, dl, nil)
	req.Header.Set("Range", "bytes=0-3")
	if rresp, err := dev.client.Do(req); err != nil || rresp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range: %v %v", rresp, err)
	}

	// 4. The cover is asked for by the template from the init answer.
	coverURL := strings.NewReplacer("{ImageId}", hobbitUUID, "{Width}", "355", "{Height}", "471").Replace(res["image_url_template"].(string))
	cresp, cdata := erGet(t, coverURL)
	img, _, err := image.Decode(bytes.NewReader(cdata))
	if cresp.StatusCode != http.StatusOK || cresp.Header.Get("Content-Type") != "image/jpeg" || err != nil || img.Bounds().Dy() > 600 {
		t.Fatalf("cover: %d %v %v", cresp.StatusCode, img, err)
	}
	qURL := strings.NewReplacer("{ImageId}", hobbitUUID, "{Width}", "355", "{Height}", "471", "{Quality}", "90", "{IsGreyscale}", "false").Replace(res["image_url_quality_template"].(string))
	if qresp, _ := erGet(t, qURL); qresp.StatusCode != http.StatusOK {
		t.Fatalf("cover with quality: %d", qresp.StatusCode)
	}
	if nresp, _ := erGet(t, strings.Replace(coverURL, hobbitUUID, duneUUID, 1)); nresp.StatusCode != http.StatusNotFound {
		t.Errorf("a book with no cover: %d", nresp.StatusCode)
	}
	// A cover of a book bought in the Kobo store is Kobo's own: redirect.
	other := strings.Replace(coverURL, hobbitUUID, "0e2f5a54-7f6d-4cf6-b4d6-0c0f1a2b3c4d", 1)
	if oresp, _ := erGet(t, other); oresp.StatusCode != http.StatusTemporaryRedirect || !strings.HasPrefix(oresp.Header.Get("Location"), "https://cdn.kobo.com/book-images/") {
		t.Fatalf("store cover: %d %s", oresp.StatusCode, oresp.Header.Get("Location"))
	}

	// 5. Metadata and state for one book.
	resp, data = dev.request(http.MethodGet, "/v1/library/"+hobbitUUID+"/metadata", nil)
	if list := erDecode[[]map[string]any](t, data); resp.StatusCode != http.StatusOK || len(list) != 1 || list[0]["Title"] != "The Hobbit" {
		t.Fatalf("metadata: %d %s", resp.StatusCode, data)
	}
	resp, data = dev.request(http.MethodGet, "/v1/library/"+hobbitUUID+"/state", nil)
	if list := erDecode[[]map[string]any](t, data); resp.StatusCode != http.StatusOK || len(list) != 1 || list[0]["EntitlementId"] != hobbitUUID {
		t.Fatalf("state: %d %s", resp.StatusCode, data)
	}

	// 6. Nothing changed: the next sync is empty.
	entries, _ = dev.sync()
	if len(entries) != 0 {
		t.Fatalf("second sync should be empty: %v", entries.kinds())
	}

	// 7. The Kobo reports it is 42.5% through the Hobbit.
	put := map[string]any{"ReadingStates": []any{map[string]any{
		"CurrentBookmark": map[string]any{
			"ProgressPercent": 42.5, "ContentSourceProgressPercent": 17.0,
			"Location": map[string]any{"Value": "kobo.12.1", "Type": "KoboSpan", "Source": "OEBPS/ch2.xhtml"},
		},
		"Statistics": map[string]any{"SpentReadingMinutes": 31, "RemainingTimeMinutes": 90},
		"StatusInfo": map[string]any{"Status": "Reading"},
	}}}
	resp, data = dev.request(http.MethodPut, "/v1/library/"+hobbitUUID+"/state", put)
	result := erDecode[struct {
		RequestResult string
		UpdateResults []map[string]any
	}](t, data)
	if resp.StatusCode != http.StatusOK || result.RequestResult != "Success" || len(result.UpdateResults) != 1 ||
		result.UpdateResults[0]["EntitlementId"] != hobbitUUID || result.UpdateResults[0]["CurrentBookmarkResult"] == nil ||
		result.UpdateResults[0]["StatisticsResult"] == nil || result.UpdateResults[0]["StatusInfoResult"] == nil {
		t.Fatalf("put state: %d %s", resp.StatusCode, data)
	}
	// It is the same place Mediarium Books shows.
	p := getJSON[map[string]any](t, f.admin, f.base+"/api/books/"+itoa64(f.hobbit.ID)+"/progress?format=ebook")
	if p["percent"] != 42.5 || p["finished"] != false || p["position"] != "" {
		t.Fatalf("shared progress: %v", p)
	}
	// And the Kobo gets its own detail back.
	_, data = dev.request(http.MethodGet, "/v1/library/"+hobbitUUID+"/state", nil)
	st := erDecode[[]struct {
		StatusInfo      struct{ Status string }
		CurrentBookmark struct {
			ProgressPercent float64
			Location        struct{ Value, Type, Source string }
		}
		Statistics struct{ SpentReadingMinutes int }
	}](t, data)[0]
	if st.StatusInfo.Status != "Reading" || st.CurrentBookmark.ProgressPercent != 42.5 || st.CurrentBookmark.Location.Value != "kobo.12.1" || st.Statistics.SpentReadingMinutes != 31 {
		t.Fatalf("state back: %s", data)
	}
	// What the Kobo reported is not sent back to it.
	if entries, _ = dev.sync(); len(entries) != 0 {
		t.Fatalf("the Kobo's own place came back: %v", entries.kinds())
	}

	// 8. Reading on in Mediarium Books moves the Kobo along.
	postJSONMethod[any](t, f.admin, http.MethodPut, f.base+"/api/books/"+itoa64(f.hobbit.ID)+"/progress",
		map[string]any{"format": "ebook", "position": "epubcfi(/6/4!/4/2/1:0)", "percent": 60.2}, http.StatusOK)
	entries, _ = dev.sync()
	changed := entries.states("ChangedReadingState")
	if len(entries) != 1 || len(changed) != 1 || changed[0]["EntitlementId"] != hobbitUUID {
		t.Fatalf("expected one changed reading state: %v", entries.kinds())
	}
	bm := changed[0]["CurrentBookmark"].(map[string]any)
	if bm["ProgressPercent"] != 60.2 || bm["Location"] != nil {
		t.Fatalf("bookmark from Mediarium Books: %v", bm)
	}
	if changed[0]["StatusInfo"].(map[string]any)["Status"] != "Reading" {
		t.Fatalf("status: %v", changed[0]["StatusInfo"])
	}
	if entries, _ = dev.sync(); len(entries) != 0 {
		t.Fatalf("sent twice: %v", entries.kinds())
	}

	// 9. Finishing the book on the Kobo.
	fin := map[string]any{"ReadingStates": []any{map[string]any{"StatusInfo": map[string]any{"Status": "Finished"}}}}
	if resp, data = dev.request(http.MethodPut, "/v1/library/"+hobbitUUID+"/state", fin); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish: %d %s", resp.StatusCode, data)
	}
	p = getJSON[map[string]any](t, f.admin, f.base+"/api/books/"+itoa64(f.hobbit.ID)+"/progress?format=ebook")
	if p["finished"] != true || p["percent"] != float64(100) {
		t.Fatalf("finished: %v", p)
	}
	// A bad update is refused, not guessed at.
	if resp, _ = dev.request(http.MethodPut, "/v1/library/"+hobbitUUID+"/state", map[string]any{"ReadingStates": []any{}}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty update: %d", resp.StatusCode)
	}

	// 10. Taking a book off the Kobo by hand keeps it off.
	if resp, _ = dev.request(http.MethodDelete, "/v1/library/"+duneUUID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	// A book taken off by hand stays off, even when its file changes.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(f.dune.EbookPath, later, later); err != nil {
		t.Fatal(err)
	}
	if entries, _ = dev.sync(); len(entries) != 0 {
		t.Fatalf("a book taken off the Kobo came back: %v", entries.kinds())
	}
	// "Start over" in the profile sends everything again.
	postJSON[ereaderAccess](t, f.admin, f.base+"/api/auth/ereader/kobo/resync", nil, http.StatusOK)
	entries, _ = dev.sync()
	if entries.titled("NewEntitlement", "Dune") == nil || entries.titled("NewEntitlement", "The Hobbit") == nil {
		t.Fatalf("after starting over: %v", entries.kinds())
	}
	if rs := entries.titled("NewEntitlement", "The Hobbit")["ReadingState"]; rs == nil {
		t.Error("a book with progress should come with its reading state")
	}

	// 11. A book that loses its ebook is taken off the Kobo; a new one arrives.
	if err := f.server.BookRepo.ClearFile(f.dune.ID, books.Ebook); err != nil {
		t.Fatal(err)
	}
	f.addEbook(t, "OL3W", "Emma", "Jane Austen", 1815, false, nil)
	entries, _ = dev.sync()
	if entries.titled("NewEntitlement", "Emma") == nil {
		t.Fatalf("new book: %v", entries.kinds())
	}
	gone := entries.titled("ChangedEntitlement", "Dune")
	if gone == nil {
		t.Fatalf("removal: %v", entries.kinds())
	}
	var goneEnt struct{ IsRemoved bool }
	_ = json.Unmarshal(gone["BookEntitlement"], &goneEnt)
	if !goneEnt.IsRemoved {
		t.Errorf("Dune should be marked removed: %s", gone["BookEntitlement"])
	}
	if entries, _ = dev.sync(); len(entries) != 0 {
		t.Fatalf("nothing more to say: %v", entries.kinds())
	}
}

func TestKoboSyncPagesAtOneHundred(t *testing.T) {
	f, dev, _ := newKoboFixture(t)
	f.server.TestSetKoboStore("http://127.0.0.1:1")
	for i := 0; i < 105; i++ {
		f.addEbook(t, "OLP"+itoa64(int64(i)), "Page Book "+itoa64(int64(1000+i)), "Many Writer", 2000, false, nil)
	}
	seen := map[string]bool{}
	rounds := 0
	for {
		entries, resp := dev.sync()
		rounds++
		for _, m := range entries {
			var inner struct{ BookMetadata struct{ Title string } }
			_ = json.Unmarshal(m["NewEntitlement"], &inner)
			if seen[inner.BookMetadata.Title] {
				t.Fatalf("%q sent twice", inner.BookMetadata.Title)
			}
			seen[inner.BookMetadata.Title] = true
		}
		if resp.Header.Get("x-kobo-sync") != "continue" {
			break
		}
		if rounds > 5 {
			t.Fatal("sync never finishes")
		}
	}
	if len(seen) != 107 || rounds != 2 {
		t.Fatalf("books=%d rounds=%d", len(seen), rounds)
	}
}

func TestKoboPassesTheStoreThrough(t *testing.T) {
	f, dev, store := newKoboFixture(t)
	dev.bearer = "device-kobo-token"
	token := strings.TrimPrefix(dev.endpoint, f.base+"/kobo/")

	// Sign-in goes to the real store, so the Kobo keeps real tokens.
	resp, data := dev.request(http.MethodPost, "/v1/auth/device", map[string]any{"UserKey": "u1", "DeviceId": "d1"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "real-access") {
		t.Fatalf("auth: %d %s", resp.StatusCode, data)
	}
	// Initialization uses the store's own list with Mediarium's addresses laid over it.
	_, data = dev.request(http.MethodGet, "/v1/initialization", nil)
	res := erDecode[struct{ Resources map[string]any }](t, data).Resources
	if res["oauth_host"] != "https://oauth.kobo.example" || res["store_host"] != "store.example" || res["library_sync"] != dev.endpoint+"/v1/library/sync" || len(res) > 20 {
		t.Fatalf("resources: %v", res)
	}
	// Anything else it asks the store is relayed, method, query and body intact.
	resp, data = dev.request(http.MethodGet, "/v1/user/profile?x=1", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "store-user") {
		t.Fatalf("profile: %d %s", resp.StatusCode, data)
	}
	if resp, _ = dev.request(http.MethodPost, "/v1/analytics/event", map[string]any{"Events": []any{}}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a store error is passed on as it is: %d", resp.StatusCode)
	}
	// A book from the store: state and metadata go to the store too.
	if resp, data = dev.request(http.MethodGet, "/v1/library/store-book-1/state", nil); resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "store-book-1") {
		t.Fatalf("store book state: %d %s", resp.StatusCode, data)
	}

	// The sync merges what the store has (books bought there) after ours.
	entries, _ := dev.sync()
	if len(entries) != 3 || entries[2]["NewEntitlement"] == nil || !strings.Contains(string(entries[2]["NewEntitlement"]), "store-book-1") {
		t.Fatalf("merged sync: %v", entries.kinds())
	}
	tok := kobo.ParseSyncToken(dev.token)
	if tok.RawStoreToken != "store.one" {
		t.Fatalf("the store's token is kept in ours: %+v", tok)
	}
	entries, _ = dev.sync()
	if len(entries) != 0 || kobo.ParseSyncToken(dev.token).RawStoreToken != "store.two" {
		t.Fatalf("second merged sync: %v %q", entries.kinds(), kobo.ParseSyncToken(dev.token).RawStoreToken)
	}

	// What reached the store: the Kobo's own sign-in, never Mediarium's token.
	calls := store.seen()
	if len(calls) == 0 {
		t.Fatal("nothing reached the store")
	}
	for _, c := range calls {
		if strings.Contains(c.Path, token) || strings.Contains(c.Body, token) || strings.Contains(c.Auth, token) || strings.Contains(c.SyncToken, token) {
			t.Errorf("Mediarium's token reached the store: %+v", c)
		}
		if c.Path != "/v1/initialization?" && c.Path != "/v1/auth/device?" && c.Auth != "Bearer device-kobo-token" {
			t.Errorf("the Kobo's own sign-in was not passed on: %+v", c)
		}
	}
	if !erContains(calls, func(c storeCall) bool { return c.Path == "/v1/user/profile?x=1" && c.Method == "GET" }) ||
		!erContains(calls, func(c storeCall) bool {
			return c.Path == "/v1/analytics/event?" && c.Method == "POST" && strings.Contains(c.Body, "Events")
		}) {
		t.Errorf("calls: %+v", calls)
	}

	// A Kobo that is not signed in to Kobo gets no store call from the sync.
	before := len(store.seen())
	dev.bearer, dev.token = "", ""
	if entries, _ = dev.sync(); len(entries) != 2 {
		t.Fatalf("sync without a Kobo account: %v", entries.kinds())
	}
	if len(store.seen()) != before {
		t.Error("the store was asked although the Kobo has no account")
	}

	// With the store out of reach, calls get an empty answer and sign-in
	// still gives the Kobo something to hold.
	f.server.TestSetKoboStore("http://127.0.0.1:1")
	if resp, data = dev.request(http.MethodGet, "/v1/user/profile", nil); resp.StatusCode != http.StatusOK || strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("store down: %d %s", resp.StatusCode, data)
	}
	if resp, data = dev.request(http.MethodGet, "/v1/user/loyalty/benefits", nil); resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"Benefits"`) {
		t.Fatalf("store down, benefits: %d %s", resp.StatusCode, data)
	}
	resp, data = dev.request(http.MethodPost, "/v1/auth/device", map[string]any{"UserKey": "u1"})
	auth := erDecode[map[string]any](t, data)
	if resp.StatusCode != http.StatusOK || auth["AccessToken"] == "" || auth["TokenType"] != "Bearer" || auth["UserKey"] != "u1" {
		t.Fatalf("local auth: %d %s", resp.StatusCode, data)
	}
}

// A reverse proxy in front of Mediarium adds headers of its own (who signed in,
// the visitor's address, a tunnel's ray id). None of that may reach Kobo; the
// Kobo's own headers still do.
func TestKoboStoreGetsOnlyTheKobosOwnHeaders(t *testing.T) {
	f := newEReaderFixture(t)
	var mu sync.Mutex
	var got http.Header
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"UserId":"store-user"}`)
	}))
	defer store.Close()
	f.server.TestSetKoboStore(store.URL)
	access := postJSON[ereaderAccess](t, f.admin, f.base+"/api/auth/ereader/kobo", nil, http.StatusOK)

	req, _ := http.NewRequest(http.MethodPost, f.base+access.Kobo.Path+"/v1/user/profile", strings.NewReader(`{"a":1}`))
	for k, v := range map[string]string{
		// What a Kobo sends.
		"Authorization": "Bearer device-kobo-token", "User-Agent": "Kobo Touch 0391/4.38.23171",
		"Accept": "application/json", "Accept-Language": "en-US", "Content-Type": "application/json",
		"X-Kobo-DeviceId": "device-1", "X-Kobo-Userkey": "key-1", "x-kobo-synctoken": "tok",
		// What a proxy, a tunnel or a client adds.
		"Remote-User": "ryan", "Remote-Groups": "admins", "X-Authentik-Username": "ryan", "X-Authentik-Uid": "42",
		"Cf-Connecting-Ip": "203.0.113.9", "Cf-Ray": "abc", "Cf-Ipcountry": "MT", "Cdn-Loop": "cloudflare",
		"Via": "1.1 proxy", "X-Forwarded-For": "203.0.113.9", "X-Forwarded-Host": "mediarium.example",
		"X-Forwarded-Proto": "https", "X-Real-Ip": "203.0.113.9", "Forwarded": "for=203.0.113.9",
		"X-Request-Id": "r1", "Traceparent": "00-aaa-bbb-01", "Cookie": "session=secret", "X-Api-Key": "mediarium-key",
		"X-Remote-User": "ryan", "True-Client-Ip": "203.0.113.9", "Referer": "https://mediarium.example/",
	} {
		req.Header.Set(k, v)
	}
	resp, err := erClient().Do(req) // no cookie jar: an e-reader has none
	if err != nil {
		t.Fatal(err)
	}
	data := erBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "store-user") {
		t.Fatalf("profile: %d %s", resp.StatusCode, data)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, k := range []string{"Authorization", "Accept", "Accept-Language", "Content-Type", "X-Kobo-Deviceid", "X-Kobo-Userkey", "X-Kobo-Synctoken"} {
		if got.Get(k) == "" {
			t.Errorf("the Kobo's own header %s did not reach the store: %v", k, got)
		}
	}
	if got.Get("User-Agent") != "Kobo Touch 0391/4.38.23171" {
		t.Errorf("User-Agent = %q", got.Get("User-Agent"))
	}
	allowed := map[string]bool{
		"Authorization": true, "Accept": true, "Accept-Language": true, "Content-Type": true, "User-Agent": true,
		"X-Kobo-Deviceid": true, "X-Kobo-Userkey": true, "X-Kobo-Synctoken": true,
		// Added by Go itself.
		"Content-Length": true, "Accept-Encoding": true,
	}
	for k := range got {
		if !allowed[k] {
			t.Errorf("header %s reached the store: %v", k, got[k])
		}
	}
}

// A book that unpacks to more than the limit is not converted: the Kobo gets
// the plain EPUB, as it does for any book that cannot be converted.
func TestKoboDoesNotConvertABookThatUnpacksTooBig(t *testing.T) {
	f, dev, _ := newKoboFixture(t)
	url := dev.endpoint + "/download/" + kobo.BookUUID(f.hobbit.ID) + "/kepub"

	// As it is, the book converts.
	if _, data := erGet(t, url); bytes.Equal(data, mustRead(t, f.hobbit.EbookPath)) {
		t.Fatal("the book should have been converted")
	}

	// With the limit below the size of its pages, it is sent as it is. (A new
	// book, because the converted copy above is kept.)
	defer api.LimitKepubEntryForTest(100)()
	f2 := f.addEbook(t, "OLBIG", "Big Book", "Some Writer", 2000, false, nil)
	url = dev.endpoint + "/download/" + kobo.BookUUID(f2.ID) + "/kepub"
	resp, data := erGet(t, url)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(data, mustRead(t, f2.EbookPath)) {
		t.Fatalf("a book over the limit: %d, %d bytes, not the original EPUB", resp.StatusCode, len(data))
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func erContains(calls []storeCall, f func(storeCall) bool) bool {
	for _, c := range calls {
		if f(c) {
			return true
		}
	}
	return false
}

func TestKoboTokensAreSecrets(t *testing.T) {
	f, dev, _ := newKoboFixture(t)
	f.server.TestSetKoboStore("http://127.0.0.1:1")
	if _, resp := dev.sync(); resp.StatusCode != http.StatusOK {
		t.Fatal("sync should work")
	}
	good := dev.endpoint

	// The address can be seen again by its owner, and only by the owner.
	again := getJSON[ereaderAccess](t, f.admin, f.base+"/api/auth/ereader")
	if f.base+again.Kobo.Path != good {
		t.Fatalf("address shown again: %s", again.Kobo.Path)
	}

	// Wrong tokens: 401, no hint, no work done.
	bad := f.base + "/kobo/" + strings.Repeat("a", 64)
	for _, u := range []string{bad + "/v1/library/sync", f.base + "/kobo/short/v1/initialization", f.base + "/kobo/" + strings.ToUpper(strings.TrimPrefix(good, f.base+"/kobo/")) + "/v1/library/sync"} {
		if resp, _ := erGet(t, u); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: %d", u[len(f.base):], resp.StatusCode)
		}
	}

	// Making a new address kills the old one.
	fresh := postJSON[ereaderAccess](t, f.admin, f.base+"/api/auth/ereader/kobo", nil, http.StatusOK)
	if resp, _ := erGet(t, good+"/v1/library/sync"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old address after a new one: %d", resp.StatusCode)
	}
	if resp, _ := erGet(t, f.base+fresh.Kobo.Path+"/v1/library/sync"); resp.StatusCode != http.StatusOK {
		t.Errorf("new address: %d", resp.StatusCode)
	}
	// Turning it off.
	off := postJSONMethod[ereaderAccess](t, f.admin, http.MethodDelete, f.base+"/api/auth/ereader/kobo", nil, http.StatusOK)
	if off.Kobo.On || off.Kobo.Path != "" {
		t.Errorf("after turning off: %+v", off.Kobo)
	}
	if resp, _ := erGet(t, f.base+fresh.Kobo.Path+"/v1/library/sync"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("address after turning off: %d", resp.StatusCode)
	}
}

func TestKoboGuessingIsLimited(t *testing.T) {
	f, dev, _ := newKoboFixture(t)
	limited := false
	for i := 0; i < 40; i++ {
		guess := f.base + "/kobo/" + strings.Repeat("b", 63) + string(rune('a'+i%6)) + "/v1/library/sync"
		resp, _ := erGet(t, guess)
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("guessing tokens was never slowed down")
	}
	// The owner's device waits too (same address); the web page does not.
	if resp, _ := erGet(t, dev.endpoint+"/v1/library/sync"); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("sync while limited: %d", resp.StatusCode)
	}
	if me := getJSON[map[string]any](t, f.admin, f.base+"/api/auth/me"); me["username"] != "ryan" {
		t.Errorf("web page affected: %v", me)
	}
}

func TestKoboNeedsPermission(t *testing.T) {
	server, base, admin, _, _ := familyServer(t)
	postJSONMethod[map[string]any](t, admin, http.MethodPut, base+"/api/modules", map[string]any{"ebooks": true}, http.StatusOK)
	postJSON[map[string]any](t, admin, base+"/api/users", map[string]any{
		"username": "kid", "password": "kids-password", "role": "member", "permissions": map[string]any{"play": false},
	}, http.StatusCreated)
	web := erLoginAs(t, base, "kid", "kids-password")
	access := postJSON[ereaderAccess](t, web, base+"/api/auth/ereader/kobo", nil, http.StatusOK)
	_ = server
	if resp, _ := erGet(t, base+access.Kobo.Path+"/v1/library/sync"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("no permission: %d", resp.StatusCode)
	}
	// The page says why it won't work.
	if me := getJSON[map[string]any](t, web, base+"/api/auth/ereader"); me["allowed"] != false {
		t.Errorf("allowed: %v", me["allowed"])
	}
}

func TestKoboSendsKindleBooksAsKepub(t *testing.T) {
	f, dev, _ := newKoboFixture(t)
	f.server.TestSetKoboStore("http://127.0.0.1:1")
	src, err := os.ReadFile(filepath.Join("..", "ebookconv", "testdata", "test.azw3"))
	if err != nil {
		t.Skipf("no sample Kindle book: %v", err)
	}
	file := filepath.Join(f.root, "Someone", "Kindle Book", "Kindle Book.azw3")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, src, 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := f.server.BookRepo.Add(books.Book{OLKey: "OL9W", Title: "Kindle Book", Author: "Someone", WantEbook: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.BookRepo.SetState(b.ID, books.Ebook, books.StatusDownloaded, "azw3", file); err != nil {
		t.Fatal(err)
	}
	// And a PDF, which a Kobo can't be given through sync.
	pdf := filepath.Join(f.root, "Someone", "Paper", "Paper.pdf")
	_ = os.MkdirAll(filepath.Dir(pdf), 0o755)
	_ = os.WriteFile(pdf, []byte("%PDF-1.4"), 0o644)
	p, _ := f.server.BookRepo.Add(books.Book{OLKey: "OL10W", Title: "Paper", Author: "Someone", WantEbook: true})
	_ = f.server.BookRepo.SetState(p.ID, books.Ebook, books.StatusDownloaded, "pdf", pdf)

	entries, _ := dev.sync()
	kindle := entries.titled("NewEntitlement", "Kindle Book")
	if kindle == nil || entries.titled("NewEntitlement", "Paper") != nil {
		t.Fatalf("sync: %v", entries.kinds())
	}
	var meta struct{ DownloadUrls []struct{ Url string } }
	_ = json.Unmarshal(kindle["BookMetadata"], &meta)
	resp, data := erGet(t, meta.DownloadUrls[0].Url)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: %d %s", resp.StatusCode, data)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	found := false
	for _, zf := range zr.File {
		if strings.HasSuffix(zf.Name, ".xhtml") || strings.HasSuffix(zf.Name, ".html") {
			rc, _ := zf.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			if strings.Contains(string(b), "koboSpan") {
				found = true
			}
		}
	}
	if !found {
		t.Error("the Kindle book did not come out as a KEPUB")
	}
}

// ---- helpers ----

func erGet(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := erClient().Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp, erBody(t, resp)
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
