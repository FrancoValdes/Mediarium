package metadata_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/rdborg/mediarium/internal/metadata"
)

func TestListEntries(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/list/8504994":
			pages = append(pages, r.URL.Query().Get("page"))
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`{"item_count":3,"total_pages":2,"items":[{"id":1396,"media_type":"tv","name":"Breaking Bad","first_air_date":"2008-01-20","vote_average":8.9}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"item_count":3,"total_pages":2,"items":[
				{"id":603,"media_type":"movie","title":"The Matrix","release_date":"1999-03-30","vote_average":8.2},
				{"id":99,"title":"No type given","release_date":"2001-01-01","vote_average":0},
				{"id":7,"media_type":"person","name":"Someone"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := metadata.NewWithBaseURL("fixture-key", srv.URL)

	got, err := c.ListEntries(context.Background(), 8504994)
	if err != nil {
		t.Fatal(err)
	}
	want := []metadata.ListEntry{
		{Kind: "movie", TMDBID: 603, Title: "The Matrix", Year: 1999, Rating: 8.2},
		{Kind: "movie", TMDBID: 99, Title: "No type given", Year: 2001},
		{Kind: "tv", TMDBID: 1396, Title: "Breaking Bad", Year: 2008, Rating: 8.9},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(pages, []string{"1", "2"}) {
		t.Fatalf("pages asked for: %v", pages)
	}
	if _, err := c.ListEntries(context.Background(), 5); err == nil {
		t.Fatal("a missing list should be an error")
	}
	if _, err := c.ListEntries(context.Background(), 0); err == nil {
		t.Fatal("list 0 should be refused")
	}
}

func TestFindByIMDB(t *testing.T) {
	var gotPath, gotSource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotSource = r.URL.Path, r.URL.Query().Get("external_source")
		if r.URL.Path == "/find/tt0903747" {
			_, _ = w.Write([]byte(`{"movie_results":[],"tv_results":[{"id":1396}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"movie_results":[{"id":603}],"tv_results":[]}`))
	}))
	defer srv.Close()
	c := metadata.NewWithBaseURL("fixture-key", srv.URL)

	movie, show, err := c.FindByIMDB(context.Background(), "tt0133093")
	if err != nil || movie != 603 || show != 0 || gotPath != "/find/tt0133093" || gotSource != "imdb_id" {
		t.Fatalf("movie: %d %d %v (%s %s)", movie, show, err, gotPath, gotSource)
	}
	movie, show, err = c.FindByIMDB(context.Background(), "tt0903747")
	if err != nil || movie != 0 || show != 1396 {
		t.Fatalf("show: %d %d %v", movie, show, err)
	}
	for _, bad := range []string{"", "603", "tt1/../x", "tt1?x=y"} {
		if _, _, err := c.FindByIMDB(context.Background(), bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestBrowseFilterAndTopRated(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Path+"?"+r.URL.Query().Get("sort_by")+"|"+r.URL.Query().Get("with_genres")+"|"+r.URL.Query().Get("with_original_language")+"|"+r.URL.Query().Get("vote_count.gte"))
		_, _ = w.Write([]byte(`{"page":1,"total_pages":1,"total_results":1,"results":[{"id":1,"title":"A","name":"A"}]}`))
	}))
	defer srv.Close()
	c := metadata.NewWithBaseURL("fixture-key", srv.URL)
	ctx := context.Background()

	if _, err := c.BrowseMovies(ctx, metadata.BrowseFilter{Sort: metadata.SortRating, Genre: 18, Language: " KO "}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.BrowseTV(ctx, metadata.BrowseFilter{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TopRatedMovies(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TopRatedTV(ctx, 1); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/discover/movie?vote_average.desc|18|ko|200",
		"/discover/tv?popularity.desc|||",
		"/movie/top_rated?|||",
		"/tv/top_rated?|||",
	}
	if !reflect.DeepEqual(queries, want) {
		t.Fatalf("got %v\nwant %v", queries, want)
	}
}
