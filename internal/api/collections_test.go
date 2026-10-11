package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// newCollectionTMDB serves a three-film collection (and one film on its own).
// extra counts the films added to the collection later: 0 or 1 (a fourth film).
func newCollectionTMDB(t *testing.T, extra *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	reply := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		}
	}
	col := `"belongs_to_collection":{"id":10,"name":"Star Wars Collection","poster_path":"/cp.jpg","backdrop_path":"/cb.jpg"}`
	mux.HandleFunc("/movie/11", reply(`{"id":11,"title":"Star Wars","release_date":"1977-05-25",`+col+`}`))
	mux.HandleFunc("/movie/12", reply(`{"id":12,"title":"The Empire Strikes Back","release_date":"1980-05-21",`+col+`}`))
	mux.HandleFunc("/movie/13", reply(`{"id":13,"title":"Return of the Jedi","release_date":"1983-05-25",`+col+`}`))
	mux.HandleFunc("/movie/14", reply(`{"id":14,"title":"A New Chapter","release_date":"2999-01-01",`+col+`}`))
	mux.HandleFunc("/movie/603", reply(`{"id":603,"title":"The Matrix","release_date":"1999-03-31","belongs_to_collection":null}`))
	mux.HandleFunc("/collection/10", func(w http.ResponseWriter, r *http.Request) {
		parts := `{"id":13,"title":"Return of the Jedi","release_date":"1983-05-25"},
			{"id":11,"title":"Star Wars","release_date":"1977-05-25"},
			{"id":12,"title":"The Empire Strikes Back","release_date":"1980-05-21"}`
		if extra.Load() > 0 {
			parts += `,{"id":14,"title":"A New Chapter","release_date":"2999-01-01"}`
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":10,"name":"Star Wars Collection","overview":"A long time ago.","poster_path":"/cp.jpg","backdrop_path":"/cb.jpg","parts":[%s]}`, parts)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCollectionsFromTheMoviePage(t *testing.T) {
	var extra atomic.Int32
	server, base, client := loginNewServer(t)
	tmdb := newCollectionTMDB(t, &extra)
	server.TestSetTMDBBaseURL("k", tmdb.URL)

	// The movie page names the collection a film belongs to; a film on its own has none.
	d := getJSON[map[string]any](t, client, base+"/api/tmdb/movies/11")
	c, _ := d["collection"].(map[string]any)
	if c == nil || c["id"] != float64(10) || c["name"] != "Star Wars Collection" || c["posterUrl"] == "" {
		t.Fatalf("movie page collection = %v", d["collection"])
	}
	if d := getJSON[map[string]any](t, client, base+"/api/tmdb/movies/603"); d["collection"] != nil {
		t.Fatalf("a film on its own has collection %v", d["collection"])
	}

	// Adding a film remembers its collection; the list names it with the film.
	one := postJSON[map[string]any](t, client, base+"/api/movies", map[string]any{"tmdbId": 11}, http.StatusCreated)
	postJSON[map[string]any](t, client, base+"/api/movies", map[string]any{"tmdbId": 603}, http.StatusCreated)
	list := getJSON[[]map[string]any](t, client, base+"/api/collections")
	if len(list) != 1 || list[0]["id"] != float64(10) {
		t.Fatalf("collections = %v", list)
	}
	ids, _ := list[0]["movieIds"].([]any)
	if len(ids) != 1 || ids[0] != one["id"] {
		t.Fatalf("movie ids = %v, want [%v]", ids, one["id"])
	}

	// The collection page: oldest first, with what is already in the library.
	got := getJSON[map[string]any](t, client, base+"/api/collections/10")
	parts, _ := got["parts"].([]any)
	if len(parts) != 3 || got["inLibrary"] != float64(1) || got["follow"] != false {
		t.Fatalf("collection = %v", got)
	}
	first := parts[0].(map[string]any)
	if first["tmdbId"] != float64(11) || first["libraryId"] != one["id"] || first["released"] != true {
		t.Fatalf("first part = %v", first)
	}
	if parts[1].(map[string]any)["libraryId"] != nil {
		t.Fatalf("a film not in the library has a library id: %v", parts[1])
	}

	// Bad choices are refused before anything is added.
	postJSON[map[string]any](t, client, base+"/api/collections/10/add", map[string]any{"sources": "carrier-pigeon"}, http.StatusBadRequest)
	postJSON[map[string]any](t, client, base+"/api/collections/10/add", map[string]any{"profileId": 9999}, http.StatusBadRequest)
	postJSON[map[string]any](t, client, base+"/api/collections/10/add", map[string]any{"rootPath": "/not/a/library/folder"}, http.StatusBadRequest)
	postJSON[map[string]any](t, client, base+"/api/collections/nope/add", map[string]any{}, http.StatusBadRequest)
	if movies := getJSON[[]map[string]any](t, client, base+"/api/movies"); len(movies) != 2 {
		t.Fatalf("a refused add changed the library: %v", movies)
	}

	// Add the whole collection, unmonitored, and follow it.
	res := postJSON[map[string]any](t, client, base+"/api/collections/10/add", map[string]any{"monitored": false, "sources": "usenet", "follow": true}, http.StatusOK)
	if res["added"] != float64(2) || res["alreadyHad"] != float64(1) || res["following"] != true {
		t.Fatalf("add result = %v", res)
	}
	movies := getJSON[[]map[string]any](t, client, base+"/api/movies")
	if len(movies) != 4 {
		t.Fatalf("expected 4 films in the library, got %d", len(movies))
	}
	got = getJSON[map[string]any](t, client, base+"/api/collections/10")
	if got["inLibrary"] != float64(3) || got["follow"] != true || got["followMonitored"] != false {
		t.Fatalf("collection after adding = %v", got)
	}
	// Adding again changes nothing.
	res = postJSON[map[string]any](t, client, base+"/api/collections/10/add", map[string]any{}, http.StatusOK)
	if res["added"] != float64(0) || res["alreadyHad"] != float64(3) {
		t.Fatalf("second add result = %v", res)
	}

	// Nothing new yet: the job adds nothing.
	server.TestMakeCollectionsDue()
	server.TestRunCollectionsJob()
	if movies := getJSON[[]map[string]any](t, client, base+"/api/movies"); len(movies) != 4 {
		t.Fatalf("the job added films that were already known: %d", len(movies))
	}

	// A fourth film joins the collection: the followed collection takes it, once.
	extra.Store(1)
	server.TestSetTMDBBaseURL("k", tmdb.URL) // a fresh client: the collection it kept for a few minutes is gone
	server.TestMakeCollectionsDue()
	server.TestRunCollectionsJob()
	server.TestMakeCollectionsDue()
	server.TestRunCollectionsJob()
	movies = getJSON[[]map[string]any](t, client, base+"/api/movies")
	if len(movies) != 5 {
		t.Fatalf("expected the new film to be added once, got %d films", len(movies))
	}
	found := false
	for _, m := range movies {
		if m["tmdbId"] == float64(14) {
			found = true
			if m["monitored"] != false {
				t.Errorf("the new film should follow the choice made (unmonitored): %v", m)
			}
		}
	}
	if !found {
		t.Fatalf("the new film is not in the library: %v", movies)
	}

	// Stop following: later films are left alone.
	putJSONStatus(t, client, base+"/api/collections/10/follow", map[string]any{"follow": false}, http.StatusOK)
	got = getJSON[map[string]any](t, client, base+"/api/collections/10")
	if got["follow"] != false {
		t.Fatalf("still following: %v", got)
	}
}

// Films added before collections were stored get theirs from the job.
func TestCollectionsJobFillsInOlderMovies(t *testing.T) {
	var extra atomic.Int32
	server, base, client := loginNewServer(t)
	server.TestSetTMDBBaseURL("k", newCollectionTMDB(t, &extra).URL)
	postJSON[map[string]any](t, client, base+"/api/movies", map[string]any{"tmdbId": 12}, http.StatusCreated)
	// As an older version would have left it.
	if err := server.TestExec(`UPDATE movies SET collection_id = 0, collection_checked = 0`); err != nil {
		t.Fatal(err)
	}
	if list := getJSON[[]map[string]any](t, client, base+"/api/collections"); len(list) != 0 {
		t.Fatalf("collections before the job: %v", list)
	}
	server.TestRunCollectionsJob()
	list := getJSON[[]map[string]any](t, client, base+"/api/collections")
	if len(list) != 1 || list[0]["name"] != "Star Wars Collection" {
		t.Fatalf("collections after the job: %v", list)
	}
}
