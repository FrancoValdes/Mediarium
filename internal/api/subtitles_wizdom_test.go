package api_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/subtitles"
	"golang.org/x/text/encoding/charmap"
)

// Wizdom is off until it is switched on; then Hebrew searches ask it, by the
// title's IMDb id (read from TMDB), and its windows-1255 files arrive as UTF-8.
func TestWizdomIsOffUntilSwitchedOnThenFindsHebrewSubtitles(t *testing.T) {
	server, base, admin := loginNewServer(t)

	tmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 603, "title": "The Matrix", "release_date": "1999-03-31", "imdb_id": "tt0133093"})
	}))
	t.Cleanup(tmdb.Close)
	server.TestSetTMDBBaseURL("k", tmdb.URL)

	legacy, err := charmap.Windows1255.NewEncoder().String("1\r\n00:00:01,000 --> 00:00:02,000\r\nשלום עולם\r\n")
	if err != nil {
		t.Fatal(err)
	}
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	f, _ := zw.Create("The.Matrix.1999.720p.HDTV.x264-THOR.srt")
	_, _ = f.Write([]byte(legacy))
	_ = zw.Close()

	var asked []string
	wz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch r.URL.Path {
		case "/api/releases/tt0133093":
			_, _ = w.Write([]byte(`{"imdb":"tt0133093","subs":[{"id":67081,"version":"The.Matrix.1999.720p.HDTV.x264-THOR"}]}`))
		case "/api/files/sub/67081":
			_, _ = w.Write(zipped.Bytes())
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(wz.Close)
	server.TestSetWizdomBaseURL(wz.URL + "/api")
	ctx := context.Background()

	// Off by default: nothing is asked, and there is no source to search.
	if s := getJSON[map[string]any](t, admin, base+"/api/settings"); s["subtitleWizdom"] != false {
		t.Fatalf("default: %v", s["subtitleWizdom"])
	}
	if _, _, err := server.TestWizdomRoundTrip(ctx, 603, "he"); !errors.Is(err, subtitles.ErrNoAPIKey) {
		t.Fatalf("while off: %v", err)
	}
	if len(asked) != 0 {
		t.Fatalf("asked while off: %v", asked)
	}

	putJSONStatus(t, admin, base+"/api/settings", map[string]any{"subtitleWizdom": true}, http.StatusOK)
	if s := getJSON[map[string]any](t, admin, base+"/api/settings"); s["subtitleWizdom"] != true {
		t.Fatalf("after switching on: %v", s["subtitleWizdom"])
	}

	results, data, err := server.TestWizdomRoundTrip(ctx, 603, "he")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Source != "wizdom" || results[0].Link != "67081" || results[0].Language != "he" {
		t.Fatalf("results: %+v", results)
	}
	if !strings.Contains(string(data), "שלום עולם") {
		t.Fatalf("the windows-1255 file should arrive as UTF-8: %q", data)
	}
	if len(asked) != 2 || asked[0] != "/api/releases/tt0133093" {
		t.Fatalf("asked: %v", asked)
	}

	// Another language is not Wizdom's to answer: not asked, and with no other
	// source there is nothing to search.
	asked = nil
	if _, _, err := server.TestWizdomRoundTrip(ctx, 603, "en"); !errors.Is(err, subtitles.ErrNoAPIKey) {
		t.Fatalf("English: %v", err)
	}
	if len(asked) != 0 {
		t.Fatalf("asked for English: %v", asked)
	}

	// The test button.
	res := postJSON[map[string]any](t, admin, base+"/api/settings/test-service", map[string]any{"service": "wizdom"}, http.StatusOK)
	if res["ok"] != true {
		t.Fatalf("test: %v", res)
	}
}
