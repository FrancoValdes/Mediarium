package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/api"
	"github.com/rdborg/mediarium/internal/books"
	"github.com/rdborg/mediarium/internal/config"
	"github.com/rdborg/mediarium/internal/crypto"
	"github.com/rdborg/mediarium/internal/store"
)

// newBaseServer is a running server configured with URL_BASE=base.
func newBaseServer(t *testing.T, base string) (string, *http.Client) {
	t.Helper()
	root, client, _ := newBaseServerWith(t, base)
	return root, client
}

// newBaseServerWith is newBaseServer that also hands back the server.
func newBaseServerWith(t *testing.T, base string) (string, *http.Client, *api.Server) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		ConfigDir:           filepath.Join(dir, "config"),
		DownloadsDir:        filepath.Join(dir, "downloads"),
		DownloadsIncomplete: filepath.Join(dir, "downloads", "incomplete"),
		DownloadsComplete:   filepath.Join(dir, "downloads", "complete"),
		MoviesDir:           filepath.Join(dir, "movies"),
		URLBase:             base,
	}
	for _, d := range []string{cfg.ConfigDir, cfg.DownloadsIncomplete, cfg.MoviesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(filepath.Join(cfg.ConfigDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := crypto.LoadOrCreateKey(filepath.Join(cfg.ConfigDir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := api.New(db, cfg, box, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { waitBackground(t, server) })
	srv := httptest.NewServer(server.Routes())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return srv.URL, &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, server
}

// The OPDS catalogue and the Kobo address put the URL base in every link they
// hand out, so an app that follows them stays under the prefix.
func TestEReaderAddressesCarryTheURLBase(t *testing.T) {
	const prefix = "/mediarium"
	root, client, server := newBaseServerWith(t, prefix)
	base := root + prefix
	if resp, body := basePost(t, client, base+"/api/onboarding/admin", `{"username":"ryan","password":"correct-horse-battery-staple","firstName":"R","lastName":"T"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("onboarding: %d %s", resp.StatusCode, body)
	}
	ebooks := filepath.Join(t.TempDir(), "ebooks")
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/modules", map[string]any{"ebooks": true}, http.StatusOK)
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/settings", map[string]any{"ebooksPath": ebooks}, http.StatusOK)
	file := filepath.Join(ebooks, "Frank Herbert", "Dune", "Dune.epub")
	buildEPUB(t, file, "Dune", false)
	added, err := server.BookRepo.Add(books.Book{OLKey: "OL2W", Title: "Dune", Author: "Frank Herbert", Year: 1965, WantEbook: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.BookRepo.SetState(added.ID, books.Ebook, books.StatusDownloaded, "epub", file); err != nil {
		t.Fatal(err)
	}

	// The addresses the profile page shows already hold the prefix.
	opdsAccess := postJSON[ereaderAccess](t, client, base+"/api/auth/ereader/opds", nil, http.StatusOK)
	if opdsAccess.OPDS.Path != prefix+"/opds/" {
		t.Fatalf("catalogue path = %q", opdsAccess.OPDS.Path)
	}
	koboAccess := postJSON[ereaderAccess](t, client, base+"/api/auth/ereader/kobo", nil, http.StatusOK)
	if !strings.HasPrefix(koboAccess.Kobo.Path, prefix+"/kobo/") {
		t.Fatalf("kobo path = %q", koboAccess.Kobo.Path)
	}

	// OPDS, Atom and JSON: every link on every page stays under the prefix.
	opdsGet := func(path string) []byte {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, root+path, nil)
		req.SetBasicAuth("ryan", opdsAccess.Password)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data := erBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, resp.StatusCode, data)
		}
		return data
	}
	hrefs := regexp.MustCompile(`(?:href="|"href":\s*")([^"]+)"`)
	for _, page := range []string{"/opds/", "/opds/books", "/opds/new", "/opds/authors", "/opds/search?q=dune", "/opds/v2/", "/opds/v2/books", "/opds/v2/new"} {
		data := opdsGet(prefix + page)
		found := hrefs.FindAllSubmatch(data, -1)
		if len(found) < 4 {
			t.Fatalf("%s: only %d links in %s", page, len(found), data)
		}
		for _, m := range found {
			href := string(m[1])
			if strings.HasPrefix(href, "http") || strings.HasPrefix(href, "urn:") {
				continue
			}
			if !strings.HasPrefix(href, prefix+"/opds") {
				t.Errorf("%s: link %q is outside the prefix", page, href)
			}
		}
	}
	// The book's own links reach the file through the prefix.
	if !bytes.Contains(opdsGet(prefix+"/opds/books"), []byte(prefix+"/opds/books/"+itoa64(added.ID)+"/download")) {
		t.Error("the download link of a book has no prefix")
	}
	if got := opdsGet(prefix + "/opds/books/" + itoa64(added.ID) + "/download"); len(got) == 0 {
		t.Error("the download link does not lead to the file")
	}
	// The bare address goes to the catalogue with the prefix.
	resp, _ := baseGet(t, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base+"/opds", nil)
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != prefix+"/opds/" {
		t.Errorf("/opds: %d, Location %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Kobo: the addresses it is told to use all lie under the prefix.
	server.TestSetKoboStore("http://127.0.0.1:1")
	endpoint := root + koboAccess.Kobo.Path
	resp, data := baseGet(t, &http.Client{}, endpoint+"/v1/initialization", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialization: %d %s", resp.StatusCode, data)
	}
	var init struct{ Resources map[string]any }
	if err := json.Unmarshal([]byte(data), &init); err != nil {
		t.Fatal(err)
	}
	if init.Resources["library_sync"] != endpoint+"/v1/library/sync" || init.Resources["image_url_template"] != endpoint+"/{ImageId}/{Width}/{Height}/false/image.jpg" {
		t.Fatalf("resources: %v / %v, want them under %s", init.Resources["library_sync"], init.Resources["image_url_template"], endpoint)
	}
	// The download address the Kobo is given for a book is under it too.
	resp, data = baseGet(t, &http.Client{}, endpoint+"/v1/library/sync", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(data, endpoint+"/download/") {
		t.Fatalf("sync: %d, download address not under %s: %.300s", resp.StatusCode, endpoint, data)
	}
}

func baseGet(t *testing.T, c *http.Client, target string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func basePost(t *testing.T, c *http.Client, target string, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestServedUnderAURLBase(t *testing.T) {
	const prefix = "/mediarium"
	root, client := newBaseServer(t, prefix)
	base := root + prefix

	// The bare prefix goes to the prefix with a slash.
	resp, _ := baseGet(t, client, root+prefix, nil)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != prefix+"/" {
		t.Fatalf("GET %s: %d, Location %q", prefix, resp.StatusCode, resp.Header.Get("Location"))
	}

	// The page and a page address inside the app both carry the prefix tag.
	for _, p := range []string{"/", "/library", "/settings/system"} {
		resp, body := baseGet(t, client, base+p, nil)
		if resp.StatusCode != 200 || !strings.Contains(body, `<meta name="mediarium-base" content="/mediarium" />`) {
			t.Fatalf("GET %s%s: %d %.150q", prefix, p, resp.StatusCode, body)
		}
	}

	// The web app manifest points inside the prefix.
	resp, body := baseGet(t, client, base+"/manifest.webmanifest", nil)
	var manifest map[string]any
	if resp.StatusCode != 200 && !strings.Contains(body, "<html") {
		t.Fatalf("manifest: %d", resp.StatusCode)
	}
	if json.Unmarshal([]byte(body), &manifest) == nil && manifest["start_url"] != nil {
		if manifest["start_url"] != prefix+"/" || manifest["scope"] != prefix+"/" {
			t.Errorf("manifest start/scope: %v", manifest)
		}
	}

	// Setting up and signing in under the prefix.
	resp, body = basePost(t, client, base+"/api/onboarding/admin", `{"username":"ryan","password":"correct-horse-battery-staple","firstName":"R","lastName":"T"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("onboarding: %d %s", resp.StatusCode, body)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "mediarium_session" {
			cookie = c
		}
	}
	if cookie == nil || cookie.Path != prefix {
		t.Fatalf("the session cookie must be limited to the prefix, got %+v", cookie)
	}
	// The cookie jar only sends it for addresses under the prefix.
	if resp, _ := baseGet(t, client, base+"/api/dashboard", nil); resp.StatusCode != 200 {
		t.Fatalf("signed in under the prefix: dashboard %d", resp.StatusCode)
	}
	u, _ := url.Parse(root)
	if cs := client.Jar.Cookies(&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/api/dashboard"}); len(cs) != 0 {
		t.Errorf("the cookie must not be sent outside the prefix: %v", cs)
	}

	// Signing out clears the cookie at the same path.
	resp, _ = basePost(t, client, base+"/api/auth/logout", `{}`)
	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == "mediarium_session" && c.Path == prefix && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("logout should clear the cookie at %s: %v", prefix, resp.Cookies())
	}
	if resp, _ := baseGet(t, client, base+"/api/dashboard", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("after logout the dashboard should need a sign-in, got %d", resp.StatusCode)
	}
	resp, body = basePost(t, client, base+"/api/auth/login", `{"username":"ryan","password":"correct-horse-battery-staple"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("login under the prefix: %d %s", resp.StatusCode, body)
	}

	// An API key works for the other apps' APIs and for the metrics, under the prefix.
	resp, body = basePost(t, client, base+"/api/auth/api-keys", `{"name":"other apps"}`)
	var created map[string]any
	_ = json.Unmarshal([]byte(body), &created)
	key, _ := created["key"].(string)
	if resp.StatusCode != http.StatusCreated || key == "" {
		t.Fatalf("api key: %d %s", resp.StatusCode, body)
	}
	plain := &http.Client{}
	resp, body = baseGet(t, plain, base+"/radarr/api/v3/system/status", map[string]string{"X-Api-Key": key})
	var status map[string]any
	_ = json.Unmarshal([]byte(body), &status)
	if resp.StatusCode != 200 || status["urlBase"] != prefix+"/radarr" {
		t.Fatalf("radarr status under the prefix: %d %s", resp.StatusCode, body)
	}
	resp, body = baseGet(t, plain, base+"/metrics", map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 200 || !strings.Contains(body, "mediarium_up 1") {
		t.Fatalf("metrics under the prefix: %d %.100s", resp.StatusCode, body)
	}
	if resp, _ := baseGet(t, plain, base+"/metrics", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics without a key: %d", resp.StatusCode)
	}

	// The calendar feed address includes the prefix.
	resp, body = basePost(t, client, base+"/api/calendar/feed", `{}`)
	var feed map[string]any
	_ = json.Unmarshal([]byte(body), &feed)
	if p, _ := feed["path"].(string); resp.StatusCode != 200 || !strings.HasPrefix(p, prefix+"/api/calendar/feed/") {
		t.Fatalf("calendar feed: %d %s", resp.StatusCode, body)
	}
	// ...and that address is served without signing in.
	if resp, body := baseGet(t, plain, root+feed["path"].(string), nil); resp.StatusCode != 200 || !strings.Contains(body, "BEGIN:VCALENDAR") {
		t.Fatalf("feed: %d %.100q", resp.StatusCode, body)
	}

	// A proxy that removes the prefix still works.
	if resp, _ := baseGet(t, client, root+"/api/version", nil); resp.StatusCode != 200 {
		t.Errorf("unprefixed /api/version: %d", resp.StatusCode)
	}
}

// With no URL base nothing changes: no tag in the page, the cookie at the root.
func TestNoURLBaseKeepsTheRoot(t *testing.T) {
	root, client := newBaseServer(t, "")
	resp, body := baseGet(t, client, root+"/library", nil)
	if resp.StatusCode != 200 || strings.Contains(body, "mediarium-base") {
		t.Fatalf("page: %d %.150q", resp.StatusCode, body)
	}
	resp, _ = basePost(t, client, root+"/api/onboarding/admin", `{"username":"ryan","password":"correct-horse-battery-staple","firstName":"R","lastName":"T"}`)
	for _, c := range resp.Cookies() {
		if c.Name == "mediarium_session" && c.Path != "/" {
			t.Errorf("cookie path %q, want /", c.Path)
		}
	}
	if resp, _ := baseGet(t, client, root+"/mediarium/api/version", nil); strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Error("/mediarium/... is not an API address when no URL base is set")
	}
}
