package importlists

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// What IMDb's "Export this list" gives, trimmed to a few rows.
const imdbExport = `Position,Const,Created,Modified,Description,Title,URL,Title Type,IMDb Rating,Runtime (mins),Year,Genres,Num Votes,Release Date,Directors
1,tt0111161,2024-01-01,2024-01-01,,The Shawshank Redemption,https://www.imdb.com/title/tt0111161/,Movie,9.3,142,1994,Drama,2900000,1994-09-23,Frank Darabont
2,tt0903747,2024-01-01,2024-01-01,,Breaking Bad,https://www.imdb.com/title/tt0903747/,TV Series,9.5,49,2008–2013,"Crime, Drama",2200000,2008-01-20,
3,tt1520211,2024-01-01,2024-01-01,,Walking Dead: Episode One,https://www.imdb.com/title/tt1520211/,TV Episode,8.0,45,2010,,1000,2010-10-31,
4,tt2395427,2024-01-01,2024-01-01,"A, with a comma",Avengers: Age of Ultron,https://www.imdb.com/title/tt2395427/,Movie,7.3,141,2015,Action,900000,2015-04-22,
5,tt0944947,2024-01-01,2024-01-01,,Game of Thrones,https://www.imdb.com/title/tt0944947/,TV Mini Series,9.2,57,2011–2019,Drama,2300000,2011-04-17,
6,tt0000001,2024-01-01,2024-01-01,,Some Game,https://www.imdb.com/title/tt0000001/,Video Game,5.0,,2001,,10,,
`

func TestParseCSV(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Item
		err  bool
	}{
		{
			name: "imdb export",
			in:   imdbExport,
			want: []Item{
				{Kind: "movie", IMDBID: "tt0111161", Title: "The Shawshank Redemption", Year: 1994},
				{Kind: "tv", IMDBID: "tt0903747", Title: "Breaking Bad", Year: 2008},
				{Kind: "movie", IMDBID: "tt2395427", Title: "Avengers: Age of Ultron", Year: 2015},
				{Kind: "tv", IMDBID: "tt0944947", Title: "Game of Thrones", Year: 2011},
			},
		},
		{
			name: "letterboxd watchlist with a byte order mark",
			in:   "\xef\xbb\xbfDate,Name,Year,Letterboxd URI\n2024-05-01,Alien,1979,https://boxd.it/1\n2024-05-02,\"Fantastic Mr. Fox\",2009,https://boxd.it/2\n",
			want: []Item{{Title: "Alien", Year: 1979}, {Title: "Fantastic Mr. Fox", Year: 2009}},
		},
		{
			name: "ids by name in any order, semicolons",
			in:   "Year;TMDB ID;Title\n1999;603;The Matrix\n;;\n",
			want: []Item{{TMDBID: 603, Title: "The Matrix", Year: 1999}},
		},
		{name: "imdb id column called imdbID", in: "imdbID\ntt0133093\nnot-an-id\n", want: []Item{{IMDBID: "tt0133093"}}},
		{name: "not a list", in: "foo,bar\n1,2\n", err: true},
		{name: "empty", in: "  \n", err: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCSV(tc.in)
			if (err != nil) != tc.err {
				t.Fatalf("error = %v, want error %v", err, tc.err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestFilterCheck(t *testing.T) {
	tests := []struct {
		name    string
		f       Filter
		rating  float64
		year    int
		details bool
		want    Verdict
	}{
		{"no filter passes anything", Filter{}, 0, 0, false, Pass},
		{"rating high enough", Filter{MinRating: 7}, 7.4, 0, false, Pass},
		{"rating exactly the minimum", Filter{MinRating: 7}, 7, 0, false, Pass},
		{"rating too low is held back at once", Filter{MinRating: 7}, 6.9, 0, false, Fail},
		{"unknown rating needs a look", Filter{MinRating: 7}, 0, 0, false, NeedDetails},
		{"still unknown after the look fails", Filter{MinRating: 7}, 0, 0, true, Fail},
		{"year too old is held back at once", Filter{MinYear: 2000}, 0, 1999, false, Fail},
		{"year ok", Filter{MinYear: 2000}, 0, 2000, false, Pass},
		{"unknown year needs a look", Filter{MinYear: 2000}, 0, 0, false, NeedDetails},
		{"both known and fine", Filter{MinRating: 6, MinYear: 2010}, 8, 2015, false, Pass},
		{"year fails before rating is looked up", Filter{MinRating: 6, MinYear: 2010}, 0, 2001, false, Fail},
		{"rating fails even if year unknown", Filter{MinRating: 6, MinYear: 2010}, 5, 0, false, Fail},
		{"year known, rating unknown", Filter{MinRating: 6, MinYear: 2010}, 0, 2020, false, NeedDetails},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.Check(tc.rating, tc.year, tc.details); got != tc.want {
				t.Fatalf("Check(%v, %d, %v) = %d, want %d", tc.rating, tc.year, tc.details, got, tc.want)
			}
		})
	}
	if (Filter{}).Active() || !(Filter{MinYear: 1}).Active() || !(Filter{MinRating: 1}).Active() {
		t.Fatal("Active is wrong")
	}
}

func TestNormalizeTitleAndBestMatch(t *testing.T) {
	norm := []struct{ in, want string }{
		{"The Lord of the Rings: The Two Towers", "lord of the rings the two towers"},
		{"lord of the rings - the two towers", "lord of the rings the two towers"},
		{"Fast & Furious", "fast and furious"},
		{"  Amélie  ", "amélie"},
		{"...", ""},
	}
	for _, tc := range norm {
		if got := NormalizeTitle(tc.in); got != tc.want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	cands := []Candidate{{1, "Alien Nation", 1988}, {2, "Alien", 1979}, {3, "Alien", 2020}, {4, "Alien", 1980}}
	tests := []struct {
		title string
		year  int
		want  int
		ok    bool
	}{
		{"Alien", 1979, 2, true},         // the exact year wins over the one a year off
		{"alien", 1980, 4, true},         // within a year of the list's year
		{"The Alien", 1979, 2, true},     // "the" is ignored
		{"Alien", 0, 2, true},            // no year: the first that matches
		{"Alien", 2005, 0, false},        // nothing near that year
		{"Aliens", 1986, 0, false},       // a different title
		{"", 1979, 0, false},             // nothing to look for
		{"Alien Nation", 1988, 1, true},  // exact
		{"Alien Nation", 1995, 0, false}, // right title, wrong year
	}
	for _, tc := range tests {
		got, ok := BestMatch(tc.title, tc.year, cands)
		if got != tc.want || ok != tc.ok {
			t.Errorf("BestMatch(%q, %d) = %d, %v; want %d, %v", tc.title, tc.year, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCSVAddress(t *testing.T) {
	tests := []struct {
		in, want string
		err      bool
	}{
		{"https://www.imdb.com/list/ls055592025/", "https://www.imdb.com/list/ls055592025/export", false},
		{"imdb.com/list/ls055592025/?sort=rank", "https://www.imdb.com/list/ls055592025/export", false},
		{"ls055592025", "https://www.imdb.com/list/ls055592025/export", false},
		{"https://example.com/my-list.csv", "https://example.com/my-list.csv", false},
		{"https://www.imdb.com/user/ur1234/watchlist", "", true},
		{"https://www.imdb.com/chart/top/", "", true},
		{"ftp://example.com/a.csv", "", true},
		{"", "", true},
	}
	for _, tc := range tests {
		got, err := CSVAddress(tc.in)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("CSVAddress(%q) = %q, %v; want %q, error %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestDownloadCSV(t *testing.T) {
	var gotUA string
	mux := http.NewServeMux()
	mux.HandleFunc("/ok.csv", func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(imdbExport))
	})
	mux.HandleFunc("/imdb-challenge", func(w http.ResponseWriter, r *http.Request) { // what IMDb answers a program
		w.Header().Set("x-amzn-waf-action", "challenge")
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/cloudflare", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html><title>Just a moment...</title>"))
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>Sign in</body></html>"))
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "90")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	UserAgent = "Mediarium/test (+https://mediarium.app)"

	text, err := downloadCSV(context.Background(), srv.Client(), srv.URL+"/ok.csv")
	if err != nil || !strings.HasPrefix(text, "Position,Const") {
		t.Fatalf("ok: %q, %v", text[:min(20, len(text))], err)
	}
	if gotUA != UserAgent {
		t.Fatalf("user agent %q", gotUA)
	}
	for _, path := range []string{"/imdb-challenge", "/cloudflare", "/login"} {
		if _, err := downloadCSV(context.Background(), srv.Client(), srv.URL+path); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: error %v, want ErrBlocked", path, err)
		}
	}
	_, err = downloadCSV(context.Background(), srv.Client(), srv.URL+"/slow")
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter.Seconds() != 90 {
		t.Fatalf("rate limit: %v", err)
	}
	if _, err := downloadCSV(context.Background(), srv.Client(), srv.URL+"/missing"); err == nil || !strings.Contains(err.Error(), "nothing at that address") {
		t.Fatalf("missing: %v", err)
	}
}

func TestParseMDBListAddress(t *testing.T) {
	tests := []struct {
		in, user, list string
		err            bool
	}{
		{"https://mdblist.com/lists/linaspurinis/top-watched-movies-of-the-week", "linaspurinis", "top-watched-movies-of-the-week", false},
		{"mdblist.com/lists/someone/list/", "someone", "list", false},
		{"someone/my.list_1", "someone", "my.list_1", false},
		{"https://mdblist.com/lists/someone", "", "", true},
		{"https://example.com/lists/a/b", "", "", true},
		{"../x", "", "", true},
		{"https://mdblist.com/lists/../b", "", "", true},
		{"", "", "", true},
	}
	for _, tc := range tests {
		user, list, err := ParseMDBListAddress(tc.in)
		if (err != nil) != tc.err || user != tc.user || list != tc.list {
			t.Errorf("ParseMDBListAddress(%q) = %q, %q, %v", tc.in, user, list, err)
		}
	}
}

func TestMDBListItems(t *testing.T) {
	var keys []string
	var offsets []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.URL.Query().Get("apikey"))
		offsets = append(offsets, r.URL.Query().Get("offset"))
		if r.URL.Path == "/lists/someone/missing/items" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("apikey") != "fixture-key" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"Invalid API key"}`))
			return
		}
		_, _ = w.Write([]byte(`{"movies":[
			{"id":603,"rank":1,"adult":0,"title":"The Matrix","imdb_id":"tt0133093","tvdb_id":null,"language":"en","mediatype":"movie","release_year":1999,"spoken_language":"en"},
			{"id":999,"rank":2,"title":"No Imdb Id","imdb_id":"","mediatype":"movie","release_year":2001}],
			"shows":[{"id":1396,"rank":3,"title":"Breaking Bad","imdb_id":"tt0903747","tvdb_id":81189,"mediatype":"show","release_year":2008}]}`))
	}))
	defer srv.Close()
	c := NewMDBListClientAt(srv.URL)

	got, err := c.Items(context.Background(), "fixture-key", "someone", "top")
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{
		{Kind: "movie", IMDBID: "tt0133093", Title: "The Matrix", Year: 1999},
		{Kind: "movie", TMDBID: 999, Title: "No Imdb Id", Year: 2001},
		{Kind: "tv", IMDBID: "tt0903747", Title: "Breaking Bad", Year: 2008},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if len(offsets) != 1 || offsets[0] != "0" {
		t.Fatalf("paging: %v", offsets)
	}
	if _, err := c.Items(context.Background(), "wrong", "someone", "top"); !errors.Is(err, ErrMDBListKey) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := c.Items(context.Background(), "fixture-key", "someone", "missing"); err == nil || !strings.Contains(err.Error(), "no public list") {
		t.Fatalf("missing list: %v", err)
	}
	if _, err := c.Items(context.Background(), "", "someone", "top"); err == nil {
		t.Fatal("no key should be refused before any call")
	}
	// The key must not leak into an error about an unreachable server.
	dead := NewMDBListClientAt("http://127.0.0.1:1")
	if _, err := dead.Items(context.Background(), "fixture-key", "a", "b"); err == nil || strings.Contains(err.Error(), "fixture-key") {
		t.Fatalf("unreachable: %v", err)
	}
}
