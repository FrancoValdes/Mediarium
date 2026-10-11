package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A subtitle add-on is added by its address, asked for subtitles of a title by
// its IMDb id (read from TMDB), and its files are fetched through a sealed link.
func TestSubtitleAddonsAreAddedSearchedAndDownloaded(t *testing.T) {
	server, base, admin := loginNewServer(t)

	tmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 603, "title": "The Matrix", "release_date": "1999-03-31", "imdb_id": "tt0133093"})
	}))
	t.Cleanup(tmdb.Close)
	server.TestSetTMDBBaseURL("k", tmdb.URL)

	var asked []string
	mux := http.NewServeMux()
	mux.HandleFunc("/mykey/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"ro.subs","name":"Romanian Subs","version":"1","resources":["subtitles"],"types":["movie","series"]}`))
	})
	mux.HandleFunc("/streams/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"s","name":"Streams","resources":["stream"]}`))
	})
	mux.HandleFunc("/mykey/subtitles/", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		_, _ = w.Write([]byte(`{"subtitles":[{"id":"9","url":"http://` + r.Host + `/file.srt","lang":"ron","title":"Matrix.1999.1080p.BluRay-GRP"}]}`))
	})
	mux.HandleFunc("/file.srt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nBun\xe3 ziua\n"))
	})
	addon := httptest.NewServer(mux)
	t.Cleanup(addon.Close)

	// A page that is not a subtitle add-on is refused, and says why.
	status, body := doStatus(t, admin, http.MethodPost, base+"/api/subtitles/addons")
	_ = body
	if status != http.StatusBadRequest {
		t.Fatalf("an empty request should be refused, got %d", status)
	}
	postJSON[map[string]any](t, admin, base+"/api/subtitles/addons", map[string]string{"url": addon.URL + "/streams/manifest.json"}, http.StatusBadRequest)

	added := postJSON[map[string]any](t, admin, base+"/api/subtitles/addons", map[string]string{"url": addon.URL + "/mykey/manifest.json"}, http.StatusCreated)
	if added["name"] != "Romanian Subs" || added["id"] == "" {
		t.Fatalf("added = %v", added)
	}
	// The address can hold a key, so only where it lives is shown.
	if v, _ := json.Marshal(added); strings.Contains(string(v), "mykey") {
		t.Fatalf("the address should not be shown: %s", v)
	}
	postJSON[map[string]any](t, admin, base+"/api/subtitles/addons", map[string]string{"url": addon.URL + "/mykey/manifest.json"}, http.StatusConflict)

	list := getJSON[[]map[string]any](t, admin, base+"/api/subtitles/addons")
	if len(list) != 1 {
		t.Fatalf("list = %v", list)
	}
	tested := postJSON[map[string]any](t, admin, base+"/api/subtitles/addons/"+added["id"].(string)+"/test", map[string]string{}, http.StatusOK)
	if tested["ok"] != true {
		t.Fatalf("test = %v", tested)
	}

	results, data, err := server.TestAddonRoundTrip(context.Background(), 603, "ro")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Source != "addon" || results[0].Release != "Matrix.1999.1080p.BluRay-GRP" || results[0].Language != "ro" {
		t.Fatalf("results = %+v", results)
	}
	if strings.Contains(results[0].Link, "file.srt") {
		t.Errorf("the link should be sealed, not the address itself: %q", results[0].Link)
	}
	if len(asked) == 0 || !strings.HasPrefix(asked[0], "/mykey/subtitles/movie/tt0133093") {
		t.Errorf("asked %v", asked)
	}
	if !strings.Contains(string(data), "Bună ziua") {
		t.Errorf("the Windows-1250 file should arrive as UTF-8: %q", data)
	}

	if status, _ := doStatus(t, admin, http.MethodDelete, base+"/api/subtitles/addons/"+added["id"].(string)); status != http.StatusOK {
		t.Fatalf("delete answered %d", status)
	}
	if list := getJSON[[]map[string]any](t, admin, base+"/api/subtitles/addons"); len(list) != 0 {
		t.Fatalf("list after delete = %v", list)
	}
}
