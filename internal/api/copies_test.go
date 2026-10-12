package api_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/api"
	"github.com/rdborg/mediarium/internal/config"
	"github.com/rdborg/mediarium/internal/crypto"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/store"
)

// A second copy of a film is grabbed, downloaded and put in the copies
// folder, and the main film stays as it was.
func TestSecondCopyOfAFilm(t *testing.T) {
	content := []byte("A second copy of the fixture film. " + strings.Repeat("4k-", 300))
	nntpSrv := newFakeNNTPServer(t, content)
	indexerSrv := newFakeIndexerServer(t)

	dir := t.TempDir()
	cfg := config.Config{
		ConfigDir: filepath.Join(dir, "config"), DownloadsDir: filepath.Join(dir, "downloads"),
		DownloadsIncomplete: filepath.Join(dir, "downloads", "incomplete"), DownloadsComplete: filepath.Join(dir, "downloads", "complete"),
		MoviesDir: filepath.Join(dir, "movies"),
	}
	copiesDir := filepath.Join(dir, "movies-4k")
	for _, d := range []string{cfg.ConfigDir, cfg.DownloadsIncomplete, cfg.MoviesDir, copiesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(filepath.Join(cfg.ConfigDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	box, err := crypto.LoadOrCreateKey(filepath.Join(cfg.ConfigDir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := api.New(db, cfg, box, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := httptest.NewServer(server.Routes())
	defer httpSrv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	base := httpSrv.URL
	postJSON[map[string]any](t, client, base+"/api/onboarding/admin", map[string]string{
		"username": "ryan", "password": "correct-horse-battery-staple", "firstName": "Ryan", "lastName": "Tester",
	}, http.StatusCreated)
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"moviesPath": cfg.MoviesDir, "namingPreset": "minimal"}, http.StatusOK)
	postJSON[map[string]any](t, client, base+"/api/usenet-servers", map[string]any{
		"name": "Fixture Usenet", "host": nntpSrv.addr, "port": nntpSrv.port, "useSsl": false, "connections": 2,
	}, http.StatusCreated)

	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 603, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	profiles := getJSON[map[string]any](t, client, base+"/api/quality-profiles")
	defaultID := profiles["defaultId"].(float64)
	var fourK float64
	for _, p := range profiles["profiles"].([]any) {
		if m := p.(map[string]any); strings.HasPrefix(m["name"].(string), "4K") {
			fourK = m["id"].(float64)
		}
	}
	copiesURL := fmt.Sprintf("%s/api/movies/%d/copies", base, movie.ID)

	// No copies folder yet: refused, and so is a folder inside the movies one.
	postJSON[map[string]any](t, client, copiesURL, map[string]any{"profileId": fourK}, http.StatusConflict)
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"movieCopiesPath": filepath.Join(cfg.MoviesDir, "4k")}, http.StatusBadRequest)
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"movieCopiesPath": copiesDir}, http.StatusOK)

	// The main film's own profile, or a second copy in the same one: refused.
	postJSON[map[string]any](t, client, copiesURL, map[string]any{"profileId": defaultID}, http.StatusBadRequest)
	made := postJSON[map[string]any](t, client, copiesURL, map[string]any{"profileId": fourK}, http.StatusCreated)
	postJSON[map[string]any](t, client, copiesURL, map[string]any{"profileId": fourK}, http.StatusConflict)
	copyID := int64(made["id"].(float64))

	listed := getJSON[map[string]any](t, client, copiesURL)
	if items := listed["items"].([]any); len(items) != 1 || listed["folder"] != copiesDir {
		t.Fatalf("copies: %+v", listed)
	}
	// The Library lists the film once, saying it has a copy.
	lib := getJSON[[]map[string]any](t, client, base+"/api/movies")
	if len(lib) != 1 || lib[0]["copies"] != float64(1) {
		t.Fatalf("library: %+v", lib)
	}
	if got := getJSON[map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d", base, copyID)); got["copyOf"] != float64(movie.ID) {
		t.Fatalf("copy: %+v", got)
	}

	// Grab a release for the copy and let it download.
	postJSON[map[string]any](t, client, base+"/api/indexers", map[string]any{
		"name": "Fixture Indexer", "definitionId": "fixture", "baseUrl": indexerSrv.URL, "apiKey": "fixture-key",
	}, http.StatusCreated)
	res := getJSON[[]map[string]any](t, client, base+"/api/search?q=fixture")
	postJSON[map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d/grab", base, copyID), map[string]any{
		"releaseTitle": res[0]["title"], "downloadUrl": res[0]["downloadUrl"], "sizeBytes": int64(len(content)),
	}, http.StatusAccepted)
	deadline := time.Now().Add(20 * time.Second)
	var got map[string]any
	for time.Now().Before(deadline) {
		got = getJSON[map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d", base, copyID))
		if got["status"] == "downloaded" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	file, _ := got["filePath"].(string)
	if got["status"] != "downloaded" || !strings.HasPrefix(file, copiesDir+string(filepath.Separator)) {
		t.Fatalf("the copy should be downloaded into %s: %+v", copiesDir, got)
	}
	if b, _ := os.ReadFile(file); !bytes.Equal(b, content) {
		t.Fatal("the copy's file isn't what was downloaded")
	}
	if main := getJSON[map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d", base, movie.ID)); main["status"] != "missing" || main["filePath"] != nil {
		t.Fatalf("the main film changed: %+v", main)
	}

	// Removing the film with its files takes the copy and its file too.
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/movies/%d?deleteFiles=true", base, movie.ID), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("remove: %d", resp.StatusCode)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("the copy's file is still there")
	}
	if _, err := server.MovieRepo.Get(copyID); err == nil {
		t.Fatal("the copy is still in the library")
	}
}

func TestSecondCopyOfAShow(t *testing.T) {
	env := newTVAutoEnv(t, []string{"Fixture.Show.S01E01.1080p.WEB-DL.x264-GRP"}, []episodeSpec{
		{1, 1, "2020-01-01", library.StatusDownloaded, "WEBDL-1080p"},
		{1, 2, "2020-01-08", library.StatusMissing, ""},
	})
	ep2, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 2)
	_ = env.server.MovieRepo.SetEpisodeMonitored(ep2.ID, false)
	copiesDir := filepath.Join(t.TempDir(), "tv-4k")
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"tvCopiesPath": copiesDir}, http.StatusOK)

	profiles := getJSON[map[string]any](t, env.client, env.baseURL+"/api/quality-profiles")
	var any720 float64
	for _, p := range profiles["profiles"].([]any) {
		if m := p.(map[string]any); m["name"] == "Any" {
			any720 = m["id"].(float64)
		}
	}
	made := postJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/copies", env.baseURL, env.seriesID), map[string]any{"profileId": any720}, http.StatusCreated)
	copyID := int64(made["id"].(float64))

	eps, _ := env.server.MovieRepo.ListEpisodes(copyID)
	// Adding the copy starts looking for it, so by now the first episode may
	// already be on its way: what matters is that none of it is "downloaded".
	if len(eps) != 2 || eps[0].Status == library.StatusDownloaded || eps[1].Status != library.StatusMissing || !eps[0].Monitored || eps[1].Monitored {
		t.Fatalf("the copy's episodes should not be downloaded, monitored as on the main show: %+v", eps)
	}
	// The show list has the show once; the copy has a page of its own.
	shows := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/series")
	if len(shows) != 1 || shows[0]["copies"] != float64(1) {
		t.Fatalf("shows: %+v", shows)
	}
	// Adding the copy starts looking for it: the grab is for the copy, not
	// for the main show, and only for its monitored episode.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ep, _ := env.server.MovieRepo.GetEpisode(copyID, 1, 1); ep.Status == library.StatusDownloading {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ep, _ := env.server.MovieRepo.GetEpisode(copyID, 1, 1); ep.Status != library.StatusDownloading {
		t.Fatalf("the copy's episode should be downloading: %s", ep.Status)
	}
	if q := env.queued(t); len(q) != 1 {
		t.Fatalf("queued: %+v", q)
	}
	if ep, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 1); ep.Status != library.StatusDownloaded {
		t.Fatalf("the main show's episode changed: %s", ep.Status)
	}
}
