package api_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"image"
	_ "image/jpeg"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// opdsGet is what an OPDS app does: a plain request with HTTP Basic.
func erOPDSGet(t *testing.T, client *http.Client, url, user, pass string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp, erBody(t, resp)
}

type atomFeed struct {
	Title   string     `xml:"title"`
	Total   int        `xml:"totalResults"`
	Links   []atomLink `xml:"link"`
	Entries []struct {
		Title   string `xml:"title"`
		ID      string `xml:"id"`
		Authors []struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Summary string     `xml:"summary"`
		Issued  string     `xml:"issued"`
		Content string     `xml:"content"`
		Links   []atomLink `xml:"link"`
	} `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr"`
}

func (f atomFeed) link(rel string) string {
	for _, l := range f.Links {
		if l.Rel == rel {
			return l.Href
		}
	}
	return ""
}

func parseAtom(t *testing.T, data []byte) atomFeed {
	t.Helper()
	var f atomFeed
	if err := xml.Unmarshal(data, &f); err != nil {
		t.Fatalf("not an Atom feed: %v\n%s", err, data)
	}
	return f
}

func TestOPDSCatalogue(t *testing.T) {
	f := newEReaderFixture(t)
	opdsURL := f.base + "/opds/"
	client := erClient()

	// Nothing is on until an account makes its e-reader password.
	resp, _ := erOPDSGet(t, client, opdsURL, "", "")
	if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("no sign-in: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if resp, _ := erOPDSGet(t, client, opdsURL, "ryan", "correct-horse-battery-staple"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the account password must not work for the catalogue: %d", resp.StatusCode)
	}

	access := postJSON[ereaderAccess](t, f.admin, f.base+"/api/auth/ereader/opds", nil, http.StatusOK)
	if access.Password == "" || !access.OPDS.On || access.OPDS.Path != "/opds/" || access.Username != "ryan" {
		t.Fatalf("access: %+v", access)
	}
	// The password is shown once: a second look doesn't have it.
	if again := getJSON[ereaderAccess](t, f.admin, f.base+"/api/auth/ereader"); again.Password != "" || !again.OPDS.On {
		t.Fatalf("second look: %+v", again)
	}
	pw := access.Password

	// The root: navigation feed with the four sections and both search links.
	resp, data := erOPDSGet(t, client, opdsURL, "ryan", pw)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/atom+xml;profile=opds-catalog;kind=navigation" {
		t.Fatalf("root: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	root := parseAtom(t, data)
	if len(root.Entries) != 4 || root.link("start") != "/opds/" || root.link("search") == "" {
		t.Fatalf("root feed: %+v", root)
	}
	// The user name is not case sensitive, the password ignores dashes and capitals.
	if resp, _ := erOPDSGet(t, client, opdsURL, "RYAN", strings.ToUpper(strings.ReplaceAll(pw, "-", ""))); resp.StatusCode != http.StatusOK {
		t.Fatalf("lenient password: %d", resp.StatusCode)
	}

	// Recently added: newest first, with the links an app needs.
	resp, data = erOPDSGet(t, client, f.base+"/opds/new", "ryan", pw)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/atom+xml;profile=opds-catalog;kind=acquisition" {
		t.Fatalf("new: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	newest := parseAtom(t, data)
	if len(newest.Entries) != 2 || newest.Entries[0].Title != "Dune" || newest.Total != 2 {
		t.Fatalf("new feed: %+v", newest)
	}
	hobbit := newest.Entries[1]
	if len(hobbit.Authors) != 1 || hobbit.Authors[0].Name != "J.R.R. Tolkien" || hobbit.Issued != "1937" || hobbit.Summary != "About The Hobbit." {
		t.Fatalf("entry: %+v", hobbit)
	}
	rels := map[string]atomLink{}
	for _, l := range hobbit.Links {
		rels[l.Rel] = l
	}
	if rels["http://opds-spec.org/acquisition"].Type != "application/epub+zip" || rels["http://opds-spec.org/image"].Href == "" || rels["http://opds-spec.org/image/thumbnail"].Href == "" {
		t.Fatalf("entry links: %+v", hobbit.Links)
	}

	// The download is the book's own file.
	resp, data = erOPDSGet(t, client, f.base+rels["http://opds-spec.org/acquisition"].Href, "ryan", pw)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/epub+zip" || !bytes.HasPrefix(data, []byte("PK")) ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "The Hobbit.epub") {
		t.Fatalf("download: %d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"))
	}
	// A download needs the sign-in too.
	if resp, _ := erOPDSGet(t, client, f.base+rels["http://opds-spec.org/acquisition"].Href, "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("download without sign-in: %d", resp.StatusCode)
	}

	// The cover comes out of the book's file, cut down for the thumbnail.
	resp, data = erOPDSGet(t, client, f.base+rels["http://opds-spec.org/image/thumbnail"].Href, "ryan", pw)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	thumb, _, err := image.Decode(bytes.NewReader(data))
	if err != nil || thumb.Bounds().Dx() > 200 || thumb.Bounds().Dy() > 300 || thumb.Bounds().Dx() < 150 {
		t.Fatalf("thumbnail size: %v %v", thumb.Bounds(), err)
	}
	resp, data = erOPDSGet(t, client, f.base+rels["http://opds-spec.org/image"].Href, "ryan", pw)
	full, _, err := image.Decode(bytes.NewReader(data))
	if resp.StatusCode != http.StatusOK || err != nil || full.Bounds().Dx() != 600 {
		t.Fatalf("cover: %d %v %v", resp.StatusCode, full.Bounds(), err)
	}
	// Dune has no cover anywhere: a plain 404, not an error.
	if resp, _ := erOPDSGet(t, client, f.base+"/opds/books/"+strconv.FormatInt(f.dune.ID, 10)+"/cover", "ryan", pw); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no cover: %d", resp.StatusCode)
	}

	// Authors and series lead to their books.
	_, data = erOPDSGet(t, client, f.base+"/opds/authors", "ryan", pw)
	authors := parseAtom(t, data)
	if len(authors.Entries) != 2 || authors.Entries[0].Title != "Frank Herbert" || authors.Entries[0].Content != "1 book" {
		t.Fatalf("authors: %+v", authors.Entries)
	}
	_, data = erOPDSGet(t, client, f.base+authors.Entries[1].Links[0].Href, "ryan", pw)
	if byAuthor := parseAtom(t, data); len(byAuthor.Entries) != 1 || byAuthor.Entries[0].Title != "The Hobbit" {
		t.Fatalf("by author: %+v", byAuthor.Entries)
	}
	_, data = erOPDSGet(t, client, f.base+"/opds/series", "ryan", pw)
	series := parseAtom(t, data)
	if len(series.Entries) != 1 || series.Entries[0].Title != "Middle-earth" {
		t.Fatalf("series: %+v", series.Entries)
	}
	_, data = erOPDSGet(t, client, f.base+series.Entries[0].Links[0].Href, "ryan", pw)
	if inSeries := parseAtom(t, data); len(inSeries.Entries) != 1 || inSeries.Entries[0].Title != "The Hobbit" {
		t.Fatalf("books in series: %+v", inSeries.Entries)
	}

	// Search, and the OpenSearch description that points at it.
	_, data = erOPDSGet(t, client, f.base+"/opds/search?q=herbert+dune", "ryan", pw)
	if found := parseAtom(t, data); len(found.Entries) != 1 || found.Entries[0].Title != "Dune" {
		t.Fatalf("search: %+v", found.Entries)
	}
	_, data = erOPDSGet(t, client, f.base+"/opds/search?q=nothing+like+it", "ryan", pw)
	if found := parseAtom(t, data); len(found.Entries) != 0 {
		t.Fatalf("empty search: %+v", found.Entries)
	}
	resp, data = erOPDSGet(t, client, f.base+"/opds/opensearch.xml", "ryan", pw)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/opensearchdescription+xml") ||
		!strings.Contains(string(data), `template="/opds/search?q={searchTerms}"`) {
		t.Fatalf("opensearch: %d %s", resp.StatusCode, data)
	}

	// OPDS 2.0 is the same catalogue as JSON.
	resp, data = erOPDSGet(t, client, f.base+"/opds/v2/books", "ryan", pw)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/opds+json" {
		t.Fatalf("v2: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	v2 := erDecode[struct {
		Metadata struct {
			NumberOfItems int `json:"numberOfItems"`
		} `json:"metadata"`
		Publications []struct {
			Metadata struct{ Title string } `json:"metadata"`
		} `json:"publications"`
	}](t, data)
	if v2.Metadata.NumberOfItems != 2 || len(v2.Publications) != 2 || v2.Publications[0].Metadata.Title != "Dune" {
		t.Fatalf("v2 feed: %s", data)
	}
	resp, data = erOPDSGet(t, client, f.base+"/opds/v2/", "ryan", pw)
	if nav := erDecode[struct{ Navigation []struct{ Href string } }](t, data); resp.StatusCode != http.StatusOK || len(nav.Navigation) != 4 || !strings.HasPrefix(nav.Navigation[0].Href, "/opds/v2/") {
		t.Fatalf("v2 root: %s", data)
	}

	// Read-only: nothing but GET and HEAD.
	req, _ := http.NewRequest(http.MethodPost, opdsURL, strings.NewReader("{}"))
	req.SetBasicAuth("ryan", pw)
	if resp, err := client.Do(req); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("post: %v %v", resp, err)
	}

	// A new password replaces the old; removing it turns the catalogue off.
	second := postJSON[ereaderAccess](t, f.admin, f.base+"/api/auth/ereader/opds", nil, http.StatusOK).Password
	if resp, _ := erOPDSGet(t, client, opdsURL, "ryan", pw); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password after a new one: %d", resp.StatusCode)
	}
	if resp, _ := erOPDSGet(t, client, opdsURL, "ryan", second); resp.StatusCode != http.StatusOK {
		t.Fatalf("new password: %d", resp.StatusCode)
	}
	postJSONMethod[ereaderAccess](t, f.admin, http.MethodDelete, f.base+"/api/auth/ereader/opds", nil, http.StatusOK)
	if resp, _ := erOPDSGet(t, client, opdsURL, "ryan", second); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after turning it off: %d", resp.StatusCode)
	}
}

func TestOPDSPagesAndGuards(t *testing.T) {
	f := newEReaderFixture(t)
	// 60 more books: the feed pages at 50.
	for i := 0; i < 60; i++ {
		f.addEbook(t, "OLB"+strconv.Itoa(i), "Book "+strconv.Itoa(1000+i), "Many Writer", 2000, false, nil)
	}
	pw := postJSON[ereaderAccess](t, f.admin, f.base+"/api/auth/ereader/opds", nil, http.StatusOK).Password
	client := erClient()
	_, data := erOPDSGet(t, client, f.base+"/opds/books", "ryan", pw)
	first := parseAtom(t, data)
	if len(first.Entries) != 50 || first.Total != 62 || first.link("next") != "/opds/books?page=2" || first.link("previous") != "" {
		t.Fatalf("page 1: entries=%d total=%d next=%q", len(first.Entries), first.Total, first.link("next"))
	}
	_, data = erOPDSGet(t, client, f.base+first.link("next"), "ryan", pw)
	second := parseAtom(t, data)
	if len(second.Entries) != 12 || second.link("next") != "" || second.link("previous") != "/opds/books" || second.link("last") != "/opds/books?page=2" {
		t.Fatalf("page 2: entries=%d next=%q prev=%q", len(second.Entries), second.link("next"), second.link("previous"))
	}
	// A page far past the end is the last page, not an error. A number too big
	// for an int must not overflow the offset (it used to panic), in the Atom
	// feed and in the JSON one, and the same goes for a negative one.
	items := func(path string) int {
		resp, data := erOPDSGet(t, client, f.base+path, "ryan", pw)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, data)
		}
		if strings.Contains(path, "/v2/") {
			feed := erDecode[struct {
				Publications []json.RawMessage
				Navigation   []json.RawMessage
			}](t, data)
			return len(feed.Publications) + len(feed.Navigation)
		}
		return len(parseAtom(t, data).Entries)
	}
	for _, path := range []string{"/opds/books", "/opds/v2/books", "/opds/authors", "/opds/v2/authors"} {
		first := items(path) // the first page: no page asked for
		last := first
		if strings.HasSuffix(path, "books") {
			last = 12 // 62 books on two pages, the second holds 12
		}
		if first == 0 {
			t.Fatalf("%s: the first page is empty", path)
		}
		for page, want := range map[string]int{
			"99": last, "9223372036854775807": last, "9223372036854775808": last, "99999999999999999999999": last,
			"-9223372036854775808": first, "-9223372036854775809": first, "-5": first, "0": first, "abc": first, "": first,
		} {
			if got := items(path + "?page=" + page); got != want {
				t.Errorf("%s?page=%s: %d items, want %d", path, page, got, want)
			}
		}
	}
	resp, data := erOPDSGet(t, client, f.base+"/opds/books?page=99", "ryan", pw)
	if resp.StatusCode != http.StatusOK || len(parseAtom(t, data).Entries) != 12 {
		t.Fatalf("far page: %d", resp.StatusCode)
	}
	// Odd input is a plain not-found, never a crash.
	for _, p := range []string{"/opds/books/abc/download", "/opds/books/99999/cover", "/opds/books/1/other", "/opds/nothing", "/opds/books/-1/download"} {
		if resp, _ := erOPDSGet(t, client, f.base+p, "ryan", pw); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestOPDSRefusesWhatItShould(t *testing.T) {
	server, base, admin, _, _ := familyServer(t)
	_ = server
	// Ebooks are off: the catalogue is not there, whoever asks.
	pw := postJSON[ereaderAccess](t, admin, base+"/api/auth/ereader/opds", nil, http.StatusOK).Password
	client := erClient()
	if resp, _ := erOPDSGet(t, client, base+"/opds/", "ryan", pw); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ebooks off: %d", resp.StatusCode)
	}
	postJSONMethod[map[string]any](t, admin, http.MethodPut, base+"/api/modules", map[string]any{"ebooks": true}, http.StatusOK)
	if resp, _ := erOPDSGet(t, client, base+"/opds/", "ryan", pw); resp.StatusCode != http.StatusOK {
		t.Fatalf("ebooks on: %d", resp.StatusCode)
	}

	// An account that may not read books gets nothing, even with its password.
	created := postJSON[map[string]any](t, admin, base+"/api/users", map[string]any{
		"username": "kid", "password": "kids-password", "role": "member", "permissions": map[string]any{"play": false},
	}, http.StatusCreated)
	_ = created
	kid := erClient()
	// The kid signs in on the web to make an e-reader password.
	web := erLoginAs(t, base, "kid", "kids-password")
	kidPW := postJSON[ereaderAccess](t, web, base+"/api/auth/ereader/opds", nil, http.StatusOK).Password
	if resp, _ := erOPDSGet(t, kid, base+"/opds/", "kid", kidPW); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no play permission: %d", resp.StatusCode)
	}
	// And someone else's password isn't theirs.
	if resp, _ := erOPDSGet(t, kid, base+"/opds/", "kid", pw); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("another account's password: %d", resp.StatusCode)
	}
}

func TestOPDSWrongPasswordsAreLimited(t *testing.T) {
	f := newEReaderFixture(t)
	pw := postJSON[ereaderAccess](t, f.admin, f.base+"/api/auth/ereader/opds", nil, http.StatusOK).Password
	client := erClient()
	var last int
	for i := 0; i < 40; i++ {
		resp, _ := erOPDSGet(t, client, f.base+"/opds/", "ryan", "wrong-guess-"+strconv.Itoa(i))
		last = resp.StatusCode
		if last == http.StatusTooManyRequests {
			if resp.Header.Get("Retry-After") == "" {
				t.Error("429 without Retry-After")
			}
			break
		}
		if last != http.StatusUnauthorized {
			t.Fatalf("try %d: %d", i, last)
		}
	}
	if last != http.StatusTooManyRequests {
		t.Fatal("guessing was never slowed down")
	}
	// While limited, even the right password waits.
	if resp, _ := erOPDSGet(t, client, f.base+"/opds/", "ryan", pw); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("right password while limited: %d", resp.StatusCode)
	}
	// The web sign-in is a different counter: the owner is not locked out of the page.
	web := erLoginAs(t, f.base, "ryan", "correct-horse-battery-staple")
	if me := getJSON[map[string]any](t, web, f.base+"/api/auth/me"); me["username"] != "ryan" {
		t.Fatalf("web sign-in after catalogue guesses: %v", me)
	}
}
