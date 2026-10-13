package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rdborg/mediarium/internal/library"
)

// An app that changes a file after the import (Tdarr, for example) tells
// Radarr or Sonarr to rescan. Mediarium looks again: a file that has only
// changed its extension is found under its new name; one that is gone is left
// as it is, never marked missing.
func TestRescanCommandsFindAFileWithANewExtension(t *testing.T) {
	server, base, admin, _, _ := familyServer(t)
	key := postJSON[map[string]any](t, admin, base+"/api/auth/api-keys", map[string]any{"name": "Tdarr"}, http.StatusCreated)["key"].(string)
	radarr := base + "/radarr/api/v3"

	dir := t.TempDir()
	old := filepath.Join(dir, "Night Harbour (2021).mp4")
	if err := os.WriteFile(old, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 701, Title: "Night Harbour", Year: 2021, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MovieRepo.SetStatus(movie.ID, library.StatusDownloaded, "WEBDL-1080p", old); err != nil {
		t.Fatal(err)
	}
	other, _ := server.MovieRepo.Add(library.Movie{TMDBID: 702, Title: "Gone Film", Year: 2020, Monitored: true})
	gone := filepath.Join(dir, "Gone Film (2020).mkv")
	if err := server.MovieRepo.SetStatus(other.ID, library.StatusDownloaded, "WEBDL-1080p", gone); err != nil {
		t.Fatal(err)
	}

	// Tdarr turns the .mp4 into an .mkv with the same name.
	renamed := filepath.Join(dir, "Night Harbour (2021).mkv")
	if err := os.Rename(old, renamed); err != nil {
		t.Fatal(err)
	}
	arrCall(t, "POST", radarr+"/command", key, map[string]any{"name": "RescanMovie", "movieId": movie.ID}, http.StatusCreated)
	arrCall(t, "POST", radarr+"/command", key, map[string]any{"name": "RefreshMovie", "movieIds": []int64{other.ID}}, http.StatusCreated)
	server.TestWaitBackground()

	got, _ := server.MovieRepo.Get(movie.ID)
	if got.FilePath != renamed || got.Status != library.StatusDownloaded || got.Quality != "WEBDL-1080p" {
		t.Fatalf("the movie should follow its file to the new name and keep its quality: %+v", got)
	}
	still, _ := server.MovieRepo.Get(other.ID)
	if still.FilePath != gone || still.Status != library.StatusDownloaded {
		t.Fatalf("a file that is gone is left alone, not marked missing: %+v", still)
	}
}

func TestRescanSeriesFindsRenamedEpisodeFiles(t *testing.T) {
	env := newTVAutoEnv(t, []string{"Fixture.Show.S01E01.1080p.WEB-DL.x264-GRP"}, []episodeSpec{
		{1, 1, "2020-01-01", library.StatusDownloaded, "WEBDL-1080p"},
		{1, 2, "2020-01-08", library.StatusMissing, ""},
	})
	key := postJSON[map[string]any](t, env.client, env.baseURL+"/api/auth/api-keys", map[string]any{"name": "Tdarr"}, http.StatusCreated)["key"].(string)
	ep, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 1)
	old := ep.FilePath
	if old == "" {
		t.Fatal("the episode should have a file")
	}
	renamed := old[:len(old)-len(filepath.Ext(old))] + ".mkv"
	if filepath.Ext(old) == ".mkv" {
		renamed = old[:len(old)-len(filepath.Ext(old))] + ".mp4"
	}
	if err := os.Rename(old, renamed); err != nil {
		t.Fatal(err)
	}
	arrCall(t, "POST", env.baseURL+"/sonarr/api/v3/command", key, map[string]any{"name": "RescanSeries", "seriesId": env.seriesID}, http.StatusCreated)
	env.server.TestWaitBackground()
	got, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 1)
	if got.FilePath != renamed || got.Status != library.StatusDownloaded {
		t.Fatalf("the episode should follow its file: %q (was %q) %s", got.FilePath, old, got.Status)
	}
	missing, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 2)
	if missing.Status != library.StatusMissing {
		t.Fatalf("an episode with no file stays missing: %s", missing.Status)
	}
}

// The Rescan files button: a file that is gone makes the title wanted again,
// a renamed one is followed, and an unavailable library folder (a drive that
// isn't plugged in) never makes everything look missing.
func TestRescanFilesButton(t *testing.T) {
	env := newTVAutoEnv(t, []string{"Fixture.Show.S01E01.1080p.WEB-DL.x264-GRP"}, []episodeSpec{
		{1, 1, "2020-01-01", library.StatusDownloaded, "WEBDL-1080p"},
		{1, 2, "2020-01-08", library.StatusDownloaded, "WEBDL-1080p"},
		{1, 3, "2020-01-15", library.StatusDownloaded, "WEBDL-1080p"},
	})
	root := tvLibrary(t, env)
	e1, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 1)
	e2, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 2)
	e3, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 3)
	p1 := placeEpisodeFile(t, env, root, e1, "Fixture Show - S01E01.mp4")
	p2 := placeEpisodeFile(t, env, root, e2, "Fixture Show - S01E02.mkv")
	placeEpisodeFile(t, env, root, e3, "Fixture Show - S01E03.mkv")

	// Episode 1 is renamed by another program, 2 is deleted by hand, 3 is untouched.
	renamed := filepath.Join(filepath.Dir(p1), "Fixture Show - S01E01.mkv")
	if err := os.Rename(p1, renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p2); err != nil {
		t.Fatal(err)
	}
	post := func() map[string]any {
		return postJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/rescan", env.baseURL, env.seriesID), nil, http.StatusOK)
	}
	got := post()
	if got["found"] != float64(1) || got["renamed"] != float64(1) || got["missing"] != float64(1) {
		t.Fatalf("one found, one renamed, one missing: %v", got)
	}
	if ep, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 1); ep.FilePath != renamed || ep.Status != library.StatusDownloaded {
		t.Fatalf("episode 1 follows its file: %+v", ep)
	}
	if ep, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 2); ep.Status != library.StatusMissing || ep.FilePath != "" {
		t.Fatalf("episode 2 is wanted again: %+v", ep)
	}
	if ep, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 3); ep.Status != library.StatusDownloaded {
		t.Fatalf("episode 3 is untouched: %+v", ep)
	}

	// The library folder empties (a drive that is not plugged in): nothing is marked missing.
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	got = post()
	if got["missing"] != float64(0) || got["skipped"] != float64(2) {
		t.Fatalf("an empty library folder skips everything: %v", got)
	}
	if ep, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 3); ep.Status != library.StatusDownloaded {
		t.Fatalf("an unavailable folder must not make episodes wanted: %+v", ep)
	}
	postJSON[map[string]any](t, env.client, env.baseURL+"/api/series/999999/rescan", nil, http.StatusNotFound)
}
