package api_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rdborg/mediarium/internal/library"
)

type fakeSubDL struct {
	srv       *httptest.Server
	searches  atomic.Int32
	downloads atomic.Int32
}

// newFakeSubDL answers every English search with one subtitle, as a zip.
func newFakeSubDL(t *testing.T) *fakeSubDL {
	t.Helper()
	f := &fakeSubDL{}
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	w, _ := zw.Create("Some.Movie.2001.1080p.WEB.srt")
	w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nFrom SubDL\n"))
	zw.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/subtitles", func(w http.ResponseWriter, r *http.Request) {
		f.searches.Add(1)
		if r.URL.Query().Get("languages") != "EN" {
			fmt.Fprint(w, `{"status":true,"subtitles":[]}`)
			return
		}
		fmt.Fprint(w, `{"status":true,"subtitles":[{"release_name":"Some.Movie.2001.1080p.WEB-GRP","name":"x.zip","url":"/subtitle/11-22.zip"}]}`)
	})
	mux.HandleFunc("/subtitle/11-22.zip", func(w http.ResponseWriter, r *http.Request) {
		f.downloads.Add(1)
		w.Write(zipped.Bytes())
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func addDownloadedMovie(t *testing.T, env *tvAutoEnv, video string) library.Movie {
	t.Helper()
	m, err := env.server.MovieRepo.Add(library.Movie{TMDBID: 9, Title: "Some Movie", Year: 2001, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = env.server.MovieRepo.SetStatus(m.ID, library.StatusDownloaded, "WEBDL-1080p", video)
	return m
}

func TestSubDLCarriesOnWhenOpenSubtitlesIsOutOfDownloads(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	os_ := newFakeOpenSubtitles(t)
	os_.quota.Store(true)
	env.server.TestSetSubtitlesBaseURL("fixture-key", os_.srv.URL)
	sdl := newFakeSubDL(t)
	env.server.TestSetSubDLBaseURL("subdl-key", sdl.srv.URL)

	dir := t.TempDir()
	video := filepath.Join(dir, "Some Movie (2001).mkv")
	os.WriteFile(video, []byte("v"), 0o644)
	addDownloadedMovie(t, env, video)

	res := postJSON[map[string]any](t, env.client, env.baseURL+"/api/subtitles/sweep", nil, http.StatusOK)
	if res["downloaded"] != float64(1) || strings.Contains(fmt.Sprint(res["message"]), "limit") {
		t.Fatalf("sweep: %+v", res)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Some Movie (2001).en.srt"))
	if err != nil || !strings.Contains(string(got), "From SubDL") {
		t.Fatalf("subtitle: %q %v", got, err)
	}
}

func TestSubDLOnItsOwnAndChosenByHand(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	env.server.TestSetSubtitlesBaseURL("", "http://127.0.0.1:1") // no OpenSubtitles key
	sdl := newFakeSubDL(t)
	env.server.TestSetSubDLBaseURL("subdl-key", sdl.srv.URL)

	dir := t.TempDir()
	video := filepath.Join(dir, "Some Movie (2001).mkv")
	os.WriteFile(video, []byte("v"), 0o644)
	m := addDownloadedMovie(t, env, video)

	list := getJSON[[]map[string]any](t, env.client, fmt.Sprintf("%s/api/movies/%d/subtitles?lang=en", env.baseURL, m.ID))
	if len(list) != 1 || list[0]["source"] != "subdl" || list[0]["link"] != "/subtitle/11-22.zip" {
		t.Fatalf("list: %+v", list)
	}
	dlURL := fmt.Sprintf("%s/api/movies/%d/subtitles/download", env.baseURL, m.ID)
	for _, bad := range []map[string]any{
		{"source": "subdl", "link": "https://elsewhere.example/x.zip", "language": "en"},
		{"source": "subdl", "link": "/subtitle/../../x", "language": "en"},
		{"source": "nowhere", "fileId": 7, "language": "en"},
		{"language": "en"},
	} {
		postJSON[map[string]any](t, env.client, dlURL, bad, http.StatusBadRequest)
	}
	postJSON[map[string]any](t, env.client, dlURL, map[string]any{"source": "subdl", "link": "/subtitle/11-22.zip", "language": "en"}, http.StatusOK)
	if _, err := os.Stat(filepath.Join(dir, "Some Movie (2001).en.srt")); err != nil {
		t.Fatal(err)
	}
	if sdl.downloads.Load() != 1 {
		t.Fatalf("downloads: %d", sdl.downloads.Load())
	}
}

func TestSubtitlesInsideTheVideoCount(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	os_ := newFakeOpenSubtitles(t)
	env.server.TestSetSubtitlesBaseURL("fixture-key", os_.srv.URL)
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleLanguages": []string{"en", "de"}}, http.StatusOK)

	// An MKV with an English track and a forced German one.
	src, err := os.ReadFile(filepath.Join("..", "subtitles", "testdata", "embedded.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(t.TempDir(), "Some Movie (2001).mkv")
	os.WriteFile(video, src, 0o644)
	m := addDownloadedMovie(t, env, video)

	st := getJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/movies/%d/subtitles/status", env.baseURL, m.ID))
	if fmt.Sprint(st["present"]) != "[en]" || fmt.Sprint(st["embedded"]) != "[en]" {
		t.Fatalf("status: %+v", st)
	}
	wanted := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/subtitles/wanted")
	if len(wanted) != 1 || fmt.Sprint(wanted[0]["missing"]) != "[de]" {
		t.Fatalf("a forced track isn't a full subtitle, so only German is wanted: %+v", wanted)
	}

	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleCountEmbedded": false}, http.StatusOK)
	wanted = getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/subtitles/wanted")
	if len(wanted) != 1 || fmt.Sprint(wanted[0]["missing"]) != "[en de]" {
		t.Fatalf("switched off, both are wanted: %+v", wanted)
	}
	if s := getJSON[map[string]any](t, env.client, env.baseURL+"/api/settings"); s["subtitleCountEmbedded"] != false {
		t.Fatalf("setting: %v", s["subtitleCountEmbedded"])
	}
}
