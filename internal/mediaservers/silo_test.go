package mediaservers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeSilo answers the way a Silo server does (checked against a real one):
// its own API under /api/v1, administrator keys starting with sa_.
type fakeSilo struct {
	*httptest.Server
	mu    sync.Mutex
	scans []string // "path:/x" or "library:1"
	items map[string]bool
}

func (f *fakeSilo) scanned() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.scans...)
}

func newFakeSilo(t *testing.T) *fakeSilo {
	t.Helper()
	f := &fakeSilo{items: map[string]bool{"movie-tmdb-10378": true, "series-tvdb-170551": true, "series-tmdb-555": true}}
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer sa_admin" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"unauthorized","message":"Invalid API key"}`)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/v1/admin/system/build", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			fmt.Fprint(w, `{"display":"74158b4a","build_number":1144,"available":true}`)
		}
	})
	mux.HandleFunc("GET /api/v1/libraries/", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			fmt.Fprint(w, `[{"id":1,"paths":["/mnt/media/Movies"],"type":"movie","name":"Movies","enabled":true},{"id":2,"paths":["/mnt/media/TV"],"type":"tv","name":"TV","enabled":true}]`)
		}
	})
	mux.HandleFunc("POST /api/v1/scan", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var req struct {
			Path      string `json:"path"`
			LibraryID int    `json:"library_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case req.LibraryID > 0:
			f.scans = append(f.scans, fmt.Sprintf("library:%d", req.LibraryID))
		case strings.HasPrefix(req.Path, "/mnt/media/"):
			f.scans = append(f.scans, "path:"+req.Path)
		default:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"bad_request","message":"No library matches the given path"}`)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"status":"accepted"}`)
	})
	mux.HandleFunc("GET /api/v1/catalog/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if !f.items[r.PathValue("id")] {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"not_found","message":"Item not found"}`)
			return
		}
		fmt.Fprintf(w, `{"content_id":%q}`, r.PathValue("id"))
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func TestSiloTestListsLibraries(t *testing.T) {
	f := newFakeSilo(t)
	c := &Client{HTTP: f.Client(), Version: "test"}
	res, err := c.Test(context.Background(), Server{Kind: KindSilo, BaseURL: f.URL, Token: "sa_admin"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ServerName != "Silo" || res.Version != "build 1144" || len(res.Libraries) != 2 || res.Libraries[1].Locations[0] != "/mnt/media/TV" {
		t.Fatalf("result = %+v", res)
	}
	_, err = c.Test(context.Background(), Server{Kind: KindSilo, BaseURL: f.URL, Token: "sa_wrong"})
	if err == nil || !strings.Contains(err.Error(), "administrator") {
		t.Fatalf("a wrong key: %v", err)
	}
}

func TestSiloTestSpotsTheJellyfinPort(t *testing.T) {
	jf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/System/Info/Public" {
			fmt.Fprint(w, `{"ServerName":"Silo","Version":"12.1.0","ProductName":"Jellyfin Server","Id":"b2d9e6c9"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer jf.Close()
	c := &Client{HTTP: jf.Client(), Version: "test"}
	_, err := c.Test(context.Background(), Server{Kind: KindSilo, BaseURL: jf.URL, Token: "sa_admin"})
	if err == nil || !strings.Contains(err.Error(), "Jellyfin-compatible port") {
		t.Fatalf("err = %v", err)
	}
}

func TestSiloScansTheFoldersThatChanged(t *testing.T) {
	f := newFakeSilo(t)
	c := &Client{HTTP: f.Client(), Version: "test"}
	s := Server{Kind: KindSilo, BaseURL: f.URL, Token: "sa_admin", PathMap: []PathMapping{{From: "/movies", To: "/mnt/media/Movies"}}}

	done, err := c.RefreshFolders(context.Background(), s, MediaMovie, []string{"/movies/Some Film (2008)", "/movies/Some Film (2008)"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.scanned(); len(got) != 1 || got[0] != "path:/mnt/media/Movies/Some Film (2008)" || len(done) != 1 {
		t.Fatalf("scans = %v, done = %v", got, done)
	}

	// A folder Silo has no library for: every library is scanned instead.
	if _, err := c.RefreshFolders(context.Background(), s, MediaTV, []string{"/elsewhere/Show (2010)"}); err != nil {
		t.Fatal(err)
	}
	if got := f.scanned(); len(got) != 3 || got[1] != "library:1" || got[2] != "library:2" {
		t.Fatalf("scans = %v", got)
	}
	// ...but not for a book.
	if _, err := c.RefreshFolders(context.Background(), s, MediaBook, []string{"/books/Someone/A Book"}); err != nil {
		t.Fatal(err)
	}
	if got := f.scanned(); len(got) != 3 {
		t.Fatalf("a book outside every library started a scan: %v", got)
	}
}

func TestSiloFindsTitlesByTheirIDs(t *testing.T) {
	f := newFakeSilo(t)
	c := &Client{HTTP: f.Client(), Version: "test", SeriesIDs: func(tmdbID int) (int, string) {
		if tmdbID == 33050 {
			return 170551, "tt1748166"
		}
		return 0, ""
	}}
	s := Server{Kind: KindSilo, BaseURL: f.URL, PublicURL: "https://silo.example.com/", Token: "sa_admin"}

	item, found, err := c.Find(context.Background(), s, MediaMovie, 10378)
	if err != nil || !found || item.URL != "https://silo.example.com/item/movie-tmdb-10378" {
		t.Fatalf("movie: %+v %v %v", item, found, err)
	}
	item, found, err = c.Find(context.Background(), s, MediaTV, 33050)
	if err != nil || !found || item.ID != "series-tvdb-170551" {
		t.Fatalf("show by TheTVDB id: %+v %v %v", item, found, err)
	}
	item, found, err = c.Find(context.Background(), s, MediaTV, 555)
	if err != nil || !found || item.ID != "series-tmdb-555" {
		t.Fatalf("show without a TheTVDB id: %+v %v %v", item, found, err)
	}
	if _, found, err = c.Find(context.Background(), s, MediaMovie, 1); err != nil || found {
		t.Fatalf("missing movie: %v %v", found, err)
	}
}
