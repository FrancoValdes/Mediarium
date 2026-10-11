package metadata_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/rdborg/mediarium/internal/metadata"
)

// The answers below follow TMDB's documentation of GET /movie/{id}
// (belongs_to_collection), GET /collection/{id} and GET /movie/{id}/images.

func TestMovieNamesItsCollection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/11":
			w.Write([]byte(`{"id":11,"title":"Star Wars","original_title":"Star Wars","release_date":"1977-05-25",
				"belongs_to_collection":{"id":10,"name":"Star Wars Collection","poster_path":"/pWVLFh4OuejTpUaDQbB1C4zoS2p.jpg","backdrop_path":"/iY2ujEY2m68OTTlPFTiHub9joHS.jpg"}}`))
		case "/movie/603":
			w.Write([]byte(`{"id":603,"title":"The Matrix","belongs_to_collection":null}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := metadata.NewWithBaseURL("k", srv.URL)

	m, err := c.GetMovie(context.Background(), 11)
	if err != nil {
		t.Fatal(err)
	}
	col := m.BelongsToCollection
	if col == nil || col.ID != 10 || col.Name != "Star Wars Collection" || col.PosterPath != "/pWVLFh4OuejTpUaDQbB1C4zoS2p.jpg" {
		t.Fatalf("collection = %+v", col)
	}
	if m.OriginalTitle != "Star Wars" {
		t.Errorf("original title = %q", m.OriginalTitle)
	}

	m, err = c.GetMovie(context.Background(), 603)
	if err != nil {
		t.Fatal(err)
	}
	if m.BelongsToCollection != nil {
		t.Errorf("a film on its own has collection %+v", m.BelongsToCollection)
	}
}

func TestGetCollection(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collection/10" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("api_key") != "k" || r.URL.Query().Get("language") == "" {
			t.Fatalf("missing key or language in %s", r.URL.RawQuery)
		}
		hits.Add(1)
		// Parts come in no order; one has no date yet; one carries "name"
		// instead of "title" as the documentation shows; one is not a movie.
		w.Write([]byte(`{"id":10,"name":"Star Wars Collection","overview":"An epic space opera.","poster_path":"/p.jpg","backdrop_path":"/b.jpg","parts":[
			{"id":1895,"title":"Revenge of the Sith","release_date":"2005-05-17","poster_path":"/c.jpg","media_type":"movie"},
			{"id":999,"title":"Untitled sequel","release_date":"","poster_path":null,"media_type":"movie"},
			{"id":11,"name":"Star Wars","release_date":"1977-05-25","poster_path":"/a.jpg","media_type":"movie"},
			{"id":5,"title":"A TV special","release_date":"1978-11-17","media_type":"tv"},
			{"id":1892,"title":"Return of the Jedi","release_date":"1983-05-25","poster_path":"/d.jpg"}
		]}`))
	}))
	defer srv.Close()
	c := metadata.NewWithBaseURL("k", srv.URL)

	col, err := c.GetCollection(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if col.Name != "Star Wars Collection" || col.Overview == "" || col.PosterPath != "/p.jpg" {
		t.Fatalf("collection = %+v", col)
	}
	var got []int
	for _, p := range col.Parts {
		got = append(got, p.TMDBID)
	}
	want := []int{11, 1892, 1895, 999} // oldest first, no date last, the TV special left out
	if len(got) != len(want) {
		t.Fatalf("parts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parts = %v, want %v", got, want)
		}
	}
	if col.Parts[0].DisplayTitle() != "Star Wars" || col.Parts[0].Year() != 1977 {
		t.Errorf("first part = %q (%d)", col.Parts[0].DisplayTitle(), col.Parts[0].Year())
	}
	if col.Parts[3].Year() != 0 {
		t.Errorf("a part without a date has year %d", col.Parts[3].Year())
	}

	// Asked again soon: from the cache.
	if _, err := c.GetCollection(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Errorf("TMDB was asked %d times, want 1", hits.Load())
	}
}

func TestGetCollectionNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"status_code":34,"status_message":"The resource you requested could not be found."}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := metadata.NewWithBaseURL("k", srv.URL)
	if _, err := c.GetCollection(context.Background(), 1); err == nil {
		t.Fatal("expected an error for a collection TMDB doesn't know")
	}
}

func TestBestLogo(t *testing.T) {
	logos := []metadata.Image{
		{FilePath: "/fr.png", Language: "fr", VoteAverage: 9},
		{FilePath: "/en-low.png", Language: "en", VoteAverage: 3},
		{FilePath: "/en-high.png", Language: "en", VoteAverage: 5.5},
		{FilePath: "/none.png", Language: "", VoteAverage: 7},
		{FilePath: "/svg.svg", Language: "en", VoteAverage: 10},
	}
	cases := []struct {
		name  string
		logos []metadata.Image
		lang  string
		want  string
	}{
		{"preferred language first", logos, "fr-FR", "/fr.png"},
		{"then English, best rated", logos, "de-DE", "/en-high.png"},
		{"English reader", logos, "en-US", "/en-high.png"},
		{"no language when no English", logos[3:4], "de-DE", "/none.png"},
		{"any language as a last resort", logos[:1], "de-DE", "/fr.png"},
		{"svg is not used", logos[4:], "en-US", ""},
		{"nothing", nil, "en-US", ""},
	}
	for _, c := range cases {
		if got := metadata.BestLogo(c.logos, c.lang); got != c.want {
			t.Errorf("%s: BestLogo = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestGetImagesAsksForTheLanguagesNeeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/603/images" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("include_image_language"); got != "en,null" {
			t.Errorf("include_image_language = %q, want en,null", got)
		}
		w.Write([]byte(`{"id":603,"logos":[{"file_path":"/logo.png","iso_639_1":"en","vote_average":5.3,"vote_count":3,"width":500,"height":200}],"posters":[],"backdrops":[]}`))
	}))
	defer srv.Close()
	c := metadata.NewWithBaseURL("k", srv.URL)
	img, err := c.GetImages(context.Background(), "movie", 603)
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.BestLogo(img.Logos, "en-US"); got != "/logo.png" {
		t.Errorf("logo = %q", got)
	}
	if _, err := c.GetImages(context.Background(), "person", 1); err == nil {
		t.Error("expected an error for an unknown kind")
	}
}

func TestImagePaths(t *testing.T) {
	ok := []string{"/pWVLFh4OuejTpUaDQbB1C4zoS2p.jpg", "/a-b_c.PNG", "/x.jpeg"}
	bad := []string{"", "pWVLF.jpg", "/../etc/passwd.jpg", "/a/b.jpg", "/a.jpg?x=1", "/a.svg", "/.jpg", "/a b.jpg", "//evil.com/x.jpg", "/a.jpg/"}
	for _, p := range ok {
		if !metadata.ValidImagePath(p) {
			t.Errorf("%q should be valid", p)
		}
		if metadata.OriginalImageURL(p) != "https://image.tmdb.org/t/p/original"+p {
			t.Errorf("url for %q = %q", p, metadata.OriginalImageURL(p))
		}
	}
	for _, p := range bad {
		if metadata.ValidImagePath(p) {
			t.Errorf("%q should not be valid", p)
		}
		if metadata.OriginalImageURL(p) != "" {
			t.Errorf("url for %q should be empty", p)
		}
	}
}
