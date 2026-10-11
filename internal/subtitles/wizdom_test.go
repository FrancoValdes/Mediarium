package subtitles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// The two JSON files are answers saved from wizdom.xyz/api/releases (the
// artwork and trailer lists cut out, a few entries kept as sent; the last film
// entry is repeated on purpose).
func wizdomServer(t *testing.T, requests *[]string) *httptest.Server {
	t.Helper()
	movie, err := os.ReadFile("testdata/wizdom_movie.json")
	if err != nil {
		t.Fatal(err)
	}
	series, err := os.ReadFile("testdata/wizdom_series.json")
	if err != nil {
		t.Fatal(err)
	}
	// A Hebrew line, in the two ways Wizdom's zips have it.
	const line = "1\r\n00:00:01,000 --> 00:00:02,000\r\nשלום עולם\r\n\r\n"
	legacy, err := charmap.Windows1255.NewEncoder().String(line)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			*requests = append(*requests, r.URL.Path+" "+r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/api/releases/tt0133093":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Write(movie)
		case "/api/releases/tt0903747":
			w.Write(series)
		case "/api/files/sub/1":
			w.Write(zipOf(t, map[string]string{"The.Matrix.srt": legacy}))
		case "/api/files/sub/2":
			w.Write(zipOf(t, map[string]string{"The.Matrix.srt": line}))
		case "/api/files/sub/3": // both encodings in one zip
			w.Write(zipOf(t, map[string]string{"a.srt": legacy, "a.utf8.srt": line}))
		case "/api/files/sub/4": // a pack
			w.Write(zipOf(t, map[string]string{"Show.S01E01.srt": legacy, "Show.S01E02.srt": legacy + "x"})) // the bigger file is the wrong episode
		case "/api/files/sub/5": // not a subtitle
			w.Write([]byte("<html>no</html>"))
		case "/api/files/sub/6": // a plain file, not zipped
			w.Write([]byte(legacy))
		case "/api/files/sub/7":
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusInternalServerError) // what Wizdom sends for a title it doesn't have
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWizdomFindMovie(t *testing.T) {
	var reqs []string
	srv := wizdomServer(t, &reqs)
	c := NewWizdomWithBaseURL(srv.URL + "/api")

	res, err := c.Find(context.Background(), WizdomQuery{IMDBID: "tt0133093", Language: "he"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 5 { // six entries, one repeated
		t.Fatalf("got %d results: %+v", len(res), res)
	}
	first := res[0]
	if first.Link != "67081" || first.Source != SourceWizdom || first.Language != "he" || first.Release != "The.Matrix.1999.720p.HDTV.x264-THOR" {
		t.Fatalf("first result: %+v", first)
	}
	// The stray ":." the site put in front of a name is dropped.
	if res[3].Release != "The.Matrix.1999.iNTERNAL.DVDRip.XviD-DELITE" {
		t.Fatalf("release name: %q", res[3].Release)
	}
	if len(reqs) != 1 || !strings.Contains(reqs[0], "Mediarium") {
		t.Fatalf("requests should say who Mediarium is: %v", reqs)
	}

	// The best release for the file is picked by the usual scoring.
	if best := Pick(res, "The.Matrix.1999.1080p.BluRay.x264.DTS-WiKi.mkv"); best == nil || best.Link != "50187" {
		t.Fatalf("pick: %+v", best)
	}
}

func TestWizdomFindEpisode(t *testing.T) {
	srv := wizdomServer(t, nil)
	c := NewWizdomWithBaseURL(srv.URL + "/api")
	ctx := context.Background()

	tests := []struct {
		name     string
		q        WizdomQuery
		wantLink []string
	}{
		{"episode 1", WizdomQuery{IMDBID: "tt0903747", Series: true, Season: 1, Episode: 1, Language: "he"}, []string{"60186", "1066", "275863"}},
		{"episode 2", WizdomQuery{IMDBID: "tt0903747", Series: true, Season: 1, Episode: 2, Language: "he"}, []string{"10293", "62519"}},
		{"episode that has none", WizdomQuery{IMDBID: "tt0903747", Series: true, Season: 1, Episode: 3, Language: "he"}, nil},
		{"season that has none", WizdomQuery{IMDBID: "tt0903747", Series: true, Season: 9, Episode: 1, Language: "he"}, nil},
		{"a series asked as a film", WizdomQuery{IMDBID: "tt0903747", Language: "he"}, nil},
		{"a film asked as a series", WizdomQuery{IMDBID: "tt0133093", Series: true, Season: 1, Episode: 1, Language: "he"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := c.Find(ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range res {
				got = append(got, r.Link)
			}
			if strings.Join(got, ",") != strings.Join(tc.wantLink, ",") {
				t.Fatalf("got %v, want %v", got, tc.wantLink)
			}
		})
	}
}

func TestWizdomFindSkipsWhatItCannotAnswer(t *testing.T) {
	var reqs []string
	srv := wizdomServer(t, &reqs)
	c := NewWizdomWithBaseURL(srv.URL + "/api")
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		q    WizdomQuery
	}{
		{"not Hebrew", WizdomQuery{IMDBID: "tt0133093", Language: "en"}},
		{"no IMDb id", WizdomQuery{Language: "he"}},
		{"not an IMDb id", WizdomQuery{IMDBID: "../etc", Language: "he"}},
		{"title Wizdom doesn't have (HTTP 500)", WizdomQuery{IMDBID: "tt9999999", Language: "he"}},
	} {
		res, err := c.Find(ctx, tc.q)
		if err != nil || len(res) != 0 {
			t.Errorf("%s: got %v, %v", tc.name, res, err)
		}
	}
	if len(reqs) != 1 { // only the unknown title was worth asking
		t.Fatalf("requests: %v", reqs)
	}
	for _, code := range []string{"he", "HE", "he-IL"} {
		if !WizdomLanguage(code) {
			t.Errorf("%s should be Hebrew", code)
		}
	}
	if WizdomLanguage("ar") || WizdomLanguage("") {
		t.Error("only Hebrew")
	}
}

func TestWizdomFindErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/releases/tt0000001":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/api/releases/tt0000002":
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.Write([]byte("<html>maintenance</html>"))
		}
	}))
	defer srv.Close()
	c := NewWizdomWithBaseURL(srv.URL + "/api")
	for _, imdb := range []string{"tt0000001", "tt0000002", "tt0000003"} {
		if _, err := c.Find(context.Background(), WizdomQuery{IMDBID: imdb, Language: "he"}); err == nil {
			t.Errorf("%s: want an error", imdb)
		}
	}
}

func TestWizdomDownload(t *testing.T) {
	srv := wizdomServer(t, nil)
	c := NewWizdomWithBaseURL(srv.URL + "/api")
	ctx := context.Background()

	for _, link := range []string{"1", "2", "3", "6"} {
		got, err := c.Download(ctx, link, 0, 0)
		if err != nil {
			t.Fatalf("link %s: %v", link, err)
		}
		// windows-1255 is turned into UTF-8, UTF-8 is left alone, and the
		// Hebrew text comes out the same either way.
		if !strings.Contains(string(got), "שלום עולם") || !strings.Contains(string(got), "-->") {
			t.Errorf("link %s: %q", link, got)
		}
	}

	if _, err := c.Download(ctx, "5", 0, 0); err == nil || !strings.Contains(err.Error(), "SRT") {
		t.Errorf("not a subtitle: %v", err)
	}
	if _, err := c.Download(ctx, "7", 0, 0); err == nil {
		t.Error("an error status should be an error")
	}
	for _, bad := range []string{"", "../x", "1/2", "abc", "12345678901"} {
		if _, err := c.Download(ctx, bad, 0, 0); err == nil {
			t.Errorf("%q should not be a valid link", bad)
		}
	}
}

func TestWizdomDownloadPicksTheEpisodeFromAPack(t *testing.T) {
	srv := wizdomServer(t, nil)
	c := NewWizdomWithBaseURL(srv.URL + "/api")
	got, err := c.Download(context.Background(), "4", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(strings.TrimSpace(string(got)), "x") {
		t.Fatalf("took episode 2's file: %q", got)
	}
}

func TestWizdomTest(t *testing.T) {
	srv := wizdomServer(t, nil)
	if msg, err := NewWizdomWithBaseURL(srv.URL + "/api").Test(context.Background()); err != nil || msg == "" {
		t.Fatalf("%q %v", msg, err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer down.Close()
	if _, err := NewWizdomWithBaseURL(down.URL + "/api").Test(context.Background()); err == nil {
		t.Fatal("a down site should fail the test")
	}
}

func TestValidWizdomLink(t *testing.T) {
	for link, want := range map[string]bool{"67081": true, "1": true, "": false, "-1": false, "1 ": false, "0x10": false, "/subtitle/1.zip": false} {
		if ValidWizdomLink(link) != want {
			t.Errorf("%q: want %v", link, want)
		}
	}
}
