package api_test

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/library"
)

func deleteFileReq(t *testing.T, e *tvAutoEnv, path string, want int) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, e.baseURL+path, nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE %s: status %d, want %d: %s", path, resp.StatusCode, want, body)
	}
	return nil
}

// tvLibrary points the TV folder at a fresh directory and puts an episode's
// file there (the fixture's own files are outside any library folder, which
// deleting refuses to touch).
func tvLibrary(t *testing.T, e *tvAutoEnv) string {
	t.Helper()
	root := t.TempDir()
	req, _ := http.NewRequest(http.MethodPut, e.baseURL+"/api/settings", strings.NewReader(fmt.Sprintf(`{"tvPath":%q}`, root)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setting the TV folder: %d", resp.StatusCode)
	}
	return root
}

func placeEpisodeFile(t *testing.T, e *tvAutoEnv, root string, ep library.Episode, name string) string {
	t.Helper()
	path := filepath.Join(root, "Fixture Show (2020)", "Season 01", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("episode "+name), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.server.MovieRepo.SetEpisodeStatus(ep.ID, library.StatusDownloaded, "WEBDL-1080p", path); err != nil {
		t.Fatal(err)
	}
	return path
}

// An episode's file can be deleted without removing the show: the file goes,
// the episode is wanted again, and the other episodes are not touched.
func TestDeleteOneEpisodeFile(t *testing.T) {
	env := newTVAutoEnv(t, []string{"Fixture.Show.S01E01.1080p.WEB-DL.x264-GRP"}, []episodeSpec{
		{1, 1, "2020-01-01", library.StatusDownloaded, "WEBDL-1080p"},
		{1, 2, "2020-01-08", library.StatusDownloaded, "WEBDL-1080p"},
		{1, 3, "2020-01-15", library.StatusMissing, ""},
	})
	root := tvLibrary(t, env)
	one, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 1)
	two, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 2)
	one.FilePath = placeEpisodeFile(t, env, root, one, "Fixture Show - S01E01.mkv")
	two.FilePath = placeEpisodeFile(t, env, root, two, "Fixture Show - S01E02.mkv")
	deleteFileReq(t, env, fmt.Sprintf("/api/episodes/%d/file", one.ID), http.StatusOK)

	if _, err := os.Stat(one.FilePath); err == nil {
		t.Fatal("the file should be gone from its place")
	}
	got, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 1)
	if got.Status != library.StatusMissing || got.FilePath != "" || got.Quality != "" {
		t.Fatalf("the episode should be wanted again with no file: %+v", got)
	}
	if !got.Monitored {
		t.Fatal("deleting a file must not change whether the episode is monitored")
	}
	if kept, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 2); kept.Status != library.StatusDownloaded || kept.FilePath != two.FilePath {
		t.Fatalf("another episode must not be touched: %+v", kept)
	}
	if _, err := os.Stat(two.FilePath); err != nil {
		t.Fatalf("the other file must still be there: %v", err)
	}
	// Nothing left to delete for the same episode.
	deleteFileReq(t, env, fmt.Sprintf("/api/episodes/%d/file", one.ID), http.StatusConflict)
	deleteFileReq(t, env, "/api/episodes/999999/file", http.StatusNotFound)
}

// A season's files go together, and a file that holds two episodes is deleted
// once and makes both wanted again.
func TestDeleteASeasonsFiles(t *testing.T) {
	env := newTVAutoEnv(t, []string{"Fixture.Show.S01E01.1080p.WEB-DL.x264-GRP"}, []episodeSpec{
		{1, 1, "2020-01-01", library.StatusDownloaded, "WEBDL-1080p"},
		{1, 2, "2020-01-08", library.StatusDownloaded, "WEBDL-1080p"},
		{2, 1, "2021-01-01", library.StatusDownloaded, "WEBDL-1080p"},
	})
	root := tvLibrary(t, env)
	one, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 1)
	two, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 1, 2)
	// Episodes 1 and 2 share one file, as a double episode does.
	shared := placeEpisodeFile(t, env, root, one, "Fixture Show - S01E01-E02.mkv")
	if err := env.server.MovieRepo.SetEpisodeStatus(two.ID, library.StatusDownloaded, "WEBDL-1080p", shared); err != nil {
		t.Fatal(err)
	}
	other, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 2, 1)
	placeEpisodeFile(t, env, root, other, "Fixture Show - S02E01.mkv")

	// Deleting just one of the two episodes takes the file, and so both episodes.
	deleteFileReq(t, env, fmt.Sprintf("/api/episodes/%d/file", one.ID), http.StatusOK)
	for _, id := range []int64{one.ID, two.ID} {
		ep, _ := env.server.MovieRepo.GetEpisodeByID(id)
		if ep.Status != library.StatusMissing || ep.FilePath != "" {
			t.Fatalf("both episodes of a shared file are wanted again: %+v", ep)
		}
	}
	if _, err := os.Stat(shared); err == nil {
		t.Fatal("the shared file should be gone")
	}
	if kept, _ := env.server.MovieRepo.GetEpisodeByID(other.ID); kept.Status != library.StatusDownloaded {
		t.Fatalf("another season must not be touched: %+v", kept)
	}

	deleteFileReq(t, env, fmt.Sprintf("/api/series/%d/seasons/2/files", env.seriesID), http.StatusOK)
	if gone, _ := env.server.MovieRepo.GetEpisodeByID(other.ID); gone.Status != library.StatusMissing || gone.FilePath != "" {
		t.Fatalf("the season's episode is wanted again: %+v", gone)
	}
	deleteFileReq(t, env, fmt.Sprintf("/api/series/%d/seasons/2/files", env.seriesID), http.StatusConflict)
}

// A movie's file can be deleted too: the file goes and the movie is wanted again.
func TestDeleteAMovieFile(t *testing.T) {
	e := newControlEnv(t, -1)
	file := filepath.Join(e.movies, "The Fixture Movie (1999)", "The Fixture Movie (1999).mkv")
	putFile(t, file, "the movie")
	if err := e.server.MovieRepo.SetStatus(e.movie.ID, library.StatusDownloaded, "WEBDL-1080p", file); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/movies/%d/file", e.base, e.movie.ID), nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("the file should be gone from its place")
	}
	m, _ := e.server.MovieRepo.Get(e.movie.ID)
	if m.Status != library.StatusMissing || m.FilePath != "" || m.Quality != "" {
		t.Fatalf("the movie is wanted again with no file: %+v", m)
	}
	// Nothing left to delete.
	req, _ = http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/movies/%d/file", e.base, e.movie.ID), nil)
	resp, _ = e.client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a movie with no file answers 409, got %d", resp.StatusCode)
	}
}

func TestTheDeleteEndpointsNeedAnAdministrator(t *testing.T) {
	_, base, _, member, _ := familyServer(t)
	for _, p := range []string{"/api/movies/1/file", "/api/episodes/1/file", "/api/series/1/seasons/1/files"} {
		req, _ := http.NewRequest(http.MethodDelete, base+p, nil)
		resp, err := member.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("DELETE %s as a member: %d, want 403", p, resp.StatusCode)
		}
	}
}
