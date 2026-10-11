package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The language titles are read in is a choice; the English title is what
// names files and folders. This checks both halves against a fake TMDB that
// notes which language each request asked for.
func TestInfoLanguageShowsOnScreenButAddingStaysEnglish(t *testing.T) {
	server, base, admin := loginNewServer(t)

	var (
		mu    sync.Mutex
		asked []string // "path language"
	)
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	note := func(r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path+" "+r.URL.Query().Get("language"))
		mu.Unlock()
	}
	title := func(r *http.Request) string {
		if r.URL.Query().Get("language") == "de-DE" {
			return "Die Matrix"
		}
		return "The Matrix"
	}
	mux.HandleFunc("/movie/603", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		genre := "Action"
		if r.URL.Query().Get("language") == "de-DE" {
			genre = "Aktion"
		}
		write(w, map[string]any{"id": 603, "title": title(r), "release_date": "1999-03-31", "overview": "Ein Hacker.",
			"genres": []map[string]any{{"id": 28, "name": genre}}})
	})
	mux.HandleFunc("/movie/603/credits", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"cast": []any{}, "crew": []any{}})
	})
	mux.HandleFunc("/movie/603/videos", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{"results": []any{}}) })
	mux.HandleFunc("/movie/603/release_dates", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{"results": []any{}}) })
	mux.HandleFunc("/genre/movie/list", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		name := "Action"
		if r.URL.Query().Get("language") == "de-DE" {
			name = "Aktion"
		}
		write(w, map[string]any{"genres": []map[string]any{{"id": 28, "name": name}}})
	})
	tmdb := httptest.NewServer(mux)
	t.Cleanup(tmdb.Close)
	server.TestSetTMDBBaseURL("k", tmdb.URL)

	st := getJSON[map[string]any](t, admin, base+"/api/settings")
	if st["infoLanguage"] != "en-US" {
		t.Fatalf("the language starts as English, got %v", st["infoLanguage"])
	}
	options, _ := st["infoLanguages"].([]any)
	if len(options) < 10 {
		t.Fatalf("expected a list of languages to choose from, got %d", len(options))
	}
	putJSONStatus(t, admin, base+"/api/settings", map[string]any{"infoLanguage": "xx-XX"}, http.StatusBadRequest)
	putJSONStatus(t, admin, base+"/api/settings", map[string]any{"infoLanguage": "de-DE"}, http.StatusOK)
	if got := getJSON[map[string]any](t, admin, base+"/api/settings")["infoLanguage"]; got != "de-DE" {
		t.Fatalf("the choice should be saved, got %v", got)
	}

	// What is read on screen comes in German.
	detail := getJSON[map[string]any](t, admin, base+"/api/tmdb/movies/603")
	if detail["title"] != "Die Matrix" {
		t.Fatalf("the title page should be in German, got %v", detail["title"])
	}
	if g, _ := detail["genres"].([]any); len(g) != 1 || g[0] != "Aktion" {
		t.Fatalf("genres should be in German, got %v", detail["genres"])
	}

	// Adding a title files it under its English name.
	root := t.TempDir()
	putJSONStatus(t, admin, base+"/api/settings", map[string]any{"moviesPath": root}, http.StatusOK)
	added := postJSON[map[string]any](t, admin, base+"/api/movies", map[string]any{"tmdbId": 603}, http.StatusCreated)
	if added["title"] != "The Matrix" {
		t.Fatalf("a title is stored under its English name, got %v", added["title"])
	}
}
