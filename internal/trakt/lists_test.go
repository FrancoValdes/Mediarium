package trakt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/trakt"
)

// Answers shaped like Trakt's documented ones: a wrapper around the movie or
// show for lists, watchlists, trending and anticipated; the bare movie for
// popular.
const (
	listAnswer = `[
	 {"rank":1,"id":101,"listed_at":"2024-01-01T00:00:00.000Z","type":"movie","movie":{"title":"The Matrix","year":1999,"ids":{"trakt":481,"slug":"the-matrix-1999","imdb":"tt0133093","tmdb":603}}},
	 {"rank":2,"id":102,"type":"show","show":{"title":"Breaking Bad","year":2008,"ids":{"trakt":1,"imdb":"tt0903747","tmdb":1396}}},
	 {"rank":3,"id":103,"type":"season","season":{"number":1,"ids":{"tmdb":3572}},"show":{"title":"Breaking Bad","year":2008,"ids":{"tmdb":1396}}},
	 {"rank":4,"id":104,"type":"person","person":{"name":"Someone","ids":{"tmdb":5}}},
	 {"rank":5,"id":105,"type":"movie","movie":{"title":"Unmatched","year":2020,"ids":{"trakt":9,"imdb":null,"tmdb":null}}}
	]`
	trendingAnswer = `[{"watchers":120,"movie":{"title":"Dune","year":2021,"ids":{"imdb":"tt1160419","tmdb":438631}}}]`
	popularAnswer  = `[{"title":"Inception","year":2010,"ids":{"imdb":"tt1375666","tmdb":27205}},{"title":"No ids","year":2000,"ids":{}}]`
)

type call struct{ path, query, key, version, agent string }

func fakeTrakt(t *testing.T, calls *[]call) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, call{r.URL.Path, r.URL.RawQuery, r.Header.Get("trakt-api-key"), r.Header.Get("trakt-api-version"), r.Header.Get("User-Agent")})
		switch r.URL.Path {
		case "/users/someone/lists/favourites/items", "/lists/2142754/items", "/users/someone/watchlist":
			_, _ = w.Write([]byte(listAnswer))
		case "/movies/trending":
			_, _ = w.Write([]byte(trendingAnswer))
		case "/movies/popular":
			_, _ = w.Write([]byte(popularAnswer))
		case "/users/hidden/watchlist":
			w.WriteHeader(http.StatusUnauthorized)
		case "/users/busy/watchlist":
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestListMedia(t *testing.T) {
	var calls []call
	srv := fakeTrakt(t, &calls)
	defer srv.Close()
	trakt.UserAgent = "Mediarium/test (+https://mediarium.app)"
	c := trakt.NewWithBaseURL("fixture-id", srv.URL)

	want := []trakt.Media{
		{Kind: "movie", Title: "The Matrix", Year: 1999, TMDBID: 603, IMDBID: "tt0133093"},
		{Kind: "tv", Title: "Breaking Bad", Year: 2008, TMDBID: 1396, IMDBID: "tt0903747"},
	}
	for name, fn := range map[string]func() ([]trakt.Media, error){
		"user list":      func() ([]trakt.Media, error) { return c.UserListMedia(context.Background(), "someone", "favourites") },
		"list by number": func() ([]trakt.Media, error) { return c.UserListMedia(context.Background(), "", "2142754") },
		"watchlist":      func() ([]trakt.Media, error) { return c.WatchlistMedia(context.Background(), "someone") },
	} {
		got, err := fn()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %+v, want %+v", name, got, want)
		}
	}
	for _, cl := range calls {
		if cl.key != "fixture-id" || cl.version != "2" || cl.agent != trakt.UserAgent {
			t.Fatalf("headers on %s: %+v", cl.path, cl)
		}
	}
}

func TestChartMedia(t *testing.T) {
	var calls []call
	srv := fakeTrakt(t, &calls)
	defer srv.Close()
	c := trakt.NewWithBaseURL("fixture-id", srv.URL)

	got, err := c.ChartMedia(context.Background(), "movie", trakt.ChartTrending, 25)
	if err != nil || len(got) != 1 || got[0].TMDBID != 438631 || got[0].Kind != "movie" {
		t.Fatalf("trending: %+v, %v", got, err)
	}
	if calls[0].query != "limit=25&page=1" {
		t.Fatalf("trending query %q", calls[0].query)
	}
	got, err = c.ChartMedia(context.Background(), "movie", trakt.ChartPopular, 500)
	if err != nil || len(got) != 1 || got[0].TMDBID != 27205 || got[0].Title != "Inception" {
		t.Fatalf("popular: %+v, %v", got, err)
	}
	if calls[1].query != "limit=50&page=1" {
		t.Fatalf("a limit over 100 should fall back to 50: %q", calls[1].query)
	}
	if _, err := c.ChartMedia(context.Background(), "movie", "best", 10); err == nil {
		t.Fatal("an unknown chart was accepted")
	}
}

func TestListErrors(t *testing.T) {
	var calls []call
	srv := fakeTrakt(t, &calls)
	defer srv.Close()
	c := trakt.NewWithBaseURL("fixture-id", srv.URL)

	if _, err := c.WatchlistMedia(context.Background(), "hidden"); !errors.Is(err, trakt.ErrPrivate) {
		t.Fatalf("private: %v", err)
	}
	if _, err := c.WatchlistMedia(context.Background(), "nobody"); !errors.Is(err, trakt.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	_, err := c.WatchlistMedia(context.Background(), "busy")
	var rl *trakt.RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter.Seconds() != 30 || !strings.Contains(err.Error(), "30 seconds") {
		t.Fatalf("rate limit: %v", err)
	}
	if _, err := trakt.New("").WatchlistMedia(context.Background(), "x"); !errors.Is(err, trakt.ErrNoClientID) {
		t.Fatalf("no client id: %v", err)
	}
}

func TestParseAddresses(t *testing.T) {
	lists := []struct {
		in, user, list string
		err            bool
	}{
		{"https://trakt.tv/users/garycrawfordgeorge/lists/imdb-top-250", "garycrawfordgeorge", "imdb-top-250", false},
		{"trakt.tv/users/me/lists/to-watch/", "me", "to-watch", false},
		{"https://trakt.tv/lists/2142754", "", "2142754", false},
		{"https://trakt.tv/lists/official/some-list", "", "", true},
		{"https://trakt.tv/users/me/watchlist", "", "", true},
		{"https://trakt.tv/users/../lists/x", "", "", true},
		{"", "", "", true},
	}
	for _, tc := range lists {
		user, list, err := trakt.ParseAnyListAddress(tc.in)
		if (err != nil) != tc.err || user != tc.user || list != tc.list {
			t.Errorf("ParseAnyListAddress(%q) = %q, %q, %v", tc.in, user, list, err)
		}
	}
	watch := []struct {
		in, user string
		err      bool
	}{
		{"someone", "someone", false},
		{"https://trakt.tv/users/someone/watchlist", "someone", false},
		{"https://app.trakt.tv/users/some_one", "some_one", false},
		{"https://example.com/users/someone/watchlist", "", true},
		{"https://trakt.tv/movies/trending", "", true},
		{"has space", "", true},
		{"", "", true},
	}
	for _, tc := range watch {
		user, err := trakt.ParseWatchlistAddress(tc.in)
		if (err != nil) != tc.err || user != tc.user {
			t.Errorf("ParseWatchlistAddress(%q) = %q, %v", tc.in, user, err)
		}
	}
}
