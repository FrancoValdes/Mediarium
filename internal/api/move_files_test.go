package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// moveLibrary is the metadata test library with a second movie folder and a
// second TV folder next to the main ones.
type moveLibrary struct {
	*metaLibrary
	movies2, tv2 string
	showID       float64
}

func newMoveLibrary(t *testing.T) *moveLibrary {
	t.Helper()
	l := &moveLibrary{metaLibrary: newMetaLibrary(t)}
	root := filepath.Dir(l.movies)
	l.movies2, l.tv2 = filepath.Join(root, "movies2"), filepath.Join(root, "tv2")
	for _, d := range []string{l.movies2, l.tv2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	putJSONStatus(t, l.client, l.base+"/api/settings", map[string]any{"moviesExtraPaths": []string{l.movies2}, "tvExtraPaths": []string{l.tv2}}, http.StatusOK)
	shows := getJSON[[]map[string]any](t, l.client, l.base+"/api/series")
	l.showID = shows[0]["id"].(float64)
	// A subtitle the app keeps track of, next to the movie.
	sub := filepath.Join(filepath.Dir(l.movie), "The Matrix (1999).en.srt")
	putFile(t, sub, "subtitle")
	if err := l.server.TestExec(`INSERT INTO subtitle_files (kind, media_id, language, file_id, sub_path, downloaded_at) VALUES ('movie', ?, 'en', 1, ?, '2026-01-01')`, int64(l.movieID), sub); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *moveLibrary) items(kind string, id float64) []map[string]any {
	return []map[string]any{{"kind": kind, "id": id}}
}

func (l *moveLibrary) waitMove(t *testing.T) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st := getJSON[map[string]any](t, l.client, l.base+"/api/library/move-files/status")
		if st["running"] == false {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the move did not finish")
	return nil
}

func TestBulkFolderChangesOnlyWhereNewFilesGo(t *testing.T) {
	l := newMoveLibrary(t)
	url := l.base + "/api/library/bulk/folder"

	// Refused: nothing picked, a folder that isn't on the list, movies and shows together.
	postJSON[map[string]any](t, l.client, url, map[string]any{"items": []any{}, "rootPath": l.movies2}, http.StatusBadRequest)
	postJSON[map[string]any](t, l.client, url, map[string]any{"items": l.items("movie", l.movieID), "rootPath": "/not/a/library/folder"}, http.StatusBadRequest)
	mixed := append(l.items("movie", l.movieID), l.items("tv", l.showID)...)
	postJSON[map[string]any](t, l.client, url, map[string]any{"items": mixed, "rootPath": l.movies2}, http.StatusBadRequest)

	res := postJSON[map[string]any](t, l.client, url, map[string]any{"items": l.items("movie", l.movieID), "rootPath": l.movies2}, http.StatusOK)
	if res["updated"] != float64(1) {
		t.Fatalf("result = %v", res)
	}
	mv := getJSON[[]map[string]any](t, l.client, l.base+"/api/movies")[0]
	if mv["rootPath"] != l.movies2 {
		t.Fatalf("movie folder = %v, want %s", mv["rootPath"], l.movies2)
	}
	if !exists(l.movie) {
		t.Fatal("the file moved although only the folder setting was asked for")
	}
	// The main folder is stored as "no choice".
	postJSON[map[string]any](t, l.client, url, map[string]any{"items": l.items("movie", l.movieID), "rootPath": l.movies}, http.StatusOK)
	if mv := getJSON[[]map[string]any](t, l.client, l.base+"/api/movies")[0]; mv["rootPath"] != nil && mv["rootPath"] != "" {
		t.Fatalf("back in the main folder: %v", mv["rootPath"])
	}
	// A show.
	postJSON[map[string]any](t, l.client, url, map[string]any{"items": l.items("tv", l.showID), "rootPath": l.tv2}, http.StatusOK)
	if sh := getJSON[[]map[string]any](t, l.client, l.base+"/api/series")[0]; sh["rootPath"] != l.tv2 {
		t.Fatalf("show folder = %v", sh["rootPath"])
	}
	// A title that is gone is reported, not fatal.
	res = postJSON[map[string]any](t, l.client, url, map[string]any{"items": l.items("movie", 99999), "rootPath": l.movies2}, http.StatusOK)
	if failed, _ := res["failed"].([]any); len(failed) != 1 || res["updated"] != float64(0) {
		t.Fatalf("result = %v", res)
	}
}

func TestMoveMovieFilesToAnotherFolder(t *testing.T) {
	l := newMoveLibrary(t)
	oldDir := filepath.Dir(l.movie)

	prev := postJSON[map[string]any](t, l.client, l.base+"/api/library/move-files/preview", map[string]any{"items": l.items("movie", l.movieID), "rootPath": l.movies2}, http.StatusOK)
	titles, _ := prev["titles"].([]any)
	if prev["canMove"] != float64(1) || prev["blocked"] != float64(0) || len(titles) != 1 {
		t.Fatalf("preview = %v", prev)
	}
	first := titles[0].(map[string]any)
	if first["files"] != float64(2) || first["method"] != "rename" || first["to"] == "" {
		t.Fatalf("preview title = %v", first)
	}
	if !exists(l.movie) {
		t.Fatal("the preview moved something")
	}

	postJSON[map[string]any](t, l.client, l.base+"/api/library/move-files", map[string]any{"items": l.items("movie", l.movieID), "rootPath": l.movies2}, http.StatusAccepted)
	st := l.waitMove(t)
	if st["moved"] != float64(1) || st["left"] != float64(0) || st["canceled"] != false {
		t.Fatalf("status = %v", st)
	}

	newDir := filepath.Join(l.movies2, "The Matrix (1999)")
	newFile := filepath.Join(newDir, "The Matrix (1999).mkv")
	if !exists(newFile) || !exists(filepath.Join(newDir, "The Matrix (1999).en.srt")) {
		t.Fatalf("the files are not in the new folder")
	}
	if exists(oldDir) {
		t.Fatal("the old folder is still there")
	}
	mv := getJSON[[]map[string]any](t, l.client, l.base+"/api/movies")[0]
	if mv["filePath"] != newFile || mv["rootPath"] != l.movies2 {
		t.Fatalf("movie = %v", mv)
	}
	if sub, _ := l.server.TestScalar(`SELECT sub_path FROM subtitle_files WHERE kind = 'movie'`); sub != filepath.Join(newDir, "The Matrix (1999).en.srt") {
		t.Fatalf("the subtitle record still points to %s", sub)
	}

	// Moving it to the folder it is in does nothing to the files.
	postJSON[map[string]any](t, l.client, l.base+"/api/library/move-files", map[string]any{"items": l.items("movie", l.movieID), "rootPath": l.movies2}, http.StatusAccepted)
	if st := l.waitMove(t); st["moved"] != float64(1) || st["left"] != float64(0) {
		t.Fatalf("second status = %v", st)
	}
	if !exists(newFile) {
		t.Fatal("the file went missing")
	}
}

func TestMoveShowFilesToAnotherFolder(t *testing.T) {
	l := newMoveLibrary(t)
	postJSON[map[string]any](t, l.client, l.base+"/api/library/move-files", map[string]any{"items": l.items("tv", l.showID), "rootPath": l.tv2}, http.StatusAccepted)
	st := l.waitMove(t)
	if st["moved"] != float64(1) || st["left"] != float64(0) {
		t.Fatalf("status = %v", st)
	}
	moved1 := filepath.Join(l.tv2, "Fixture Show", "Season 01", "Fixture Show - S01E01-E02.mkv")
	moved2 := filepath.Join(l.tv2, "Fixture Show", "Season 02", "Fixture Show - S02E01.mkv")
	if !exists(moved1) || !exists(moved2) {
		t.Fatal("the episodes are not in the new folder")
	}
	if exists(filepath.Join(l.tv, "Fixture Show")) {
		t.Fatal("the old show folder is still there")
	}
	got, err := l.server.TestScalar(`SELECT file_path FROM episodes WHERE season = 2 AND episode = 1`)
	if err != nil || got != moved2 {
		t.Fatalf("episode file = %q (%v), want %q", got, err, moved2)
	}
	got, _ = l.server.TestScalar(`SELECT COUNT(*) FROM episodes WHERE file_path LIKE ?`, l.tv+"/%")
	if got != "0" {
		t.Fatalf("%s episodes still point into the old folder", got)
	}
	if sh := getJSON[[]map[string]any](t, l.client, l.base+"/api/series")[0]; sh["rootPath"] != l.tv2 {
		t.Fatalf("show folder = %v", sh["rootPath"])
	}
}

func TestMoveLeavesATitleAloneWhenSomethingIsInTheWay(t *testing.T) {
	l := newMoveLibrary(t)
	// A folder with the same name is already in the new place.
	inTheWay := filepath.Join(l.movies2, "The Matrix (1999)", "precious.txt")
	putFile(t, inTheWay, "do not touch")

	prev := postJSON[map[string]any](t, l.client, l.base+"/api/library/move-files/preview", map[string]any{"items": l.items("movie", l.movieID), "rootPath": l.movies2}, http.StatusOK)
	first := prev["titles"].([]any)[0].(map[string]any)
	if prev["blocked"] != float64(1) || prev["canMove"] != float64(0) || !strings.Contains(first["problem"].(string), "already") {
		t.Fatalf("preview = %v", prev)
	}

	postJSON[map[string]any](t, l.client, l.base+"/api/library/move-files", map[string]any{"items": l.items("movie", l.movieID), "rootPath": l.movies2}, http.StatusAccepted)
	st := l.waitMove(t)
	if st["moved"] != float64(0) || st["left"] != float64(1) {
		t.Fatalf("status = %v", st)
	}
	problems, _ := st["problems"].([]any)
	if len(problems) != 1 {
		t.Fatalf("problems = %v", st["problems"])
	}
	// The title is exactly as it was: file, record and folder setting.
	if !exists(l.movie) || slurp(t, inTheWay) != "do not touch" {
		t.Fatal("something was changed")
	}
	mv := getJSON[[]map[string]any](t, l.client, l.base+"/api/movies")[0]
	if mv["filePath"] != l.movie || (mv["rootPath"] != nil && mv["rootPath"] != "") {
		t.Fatalf("movie = %v", mv)
	}
}

func TestMoveRefusesATitleThatIsBeingDownloaded(t *testing.T) {
	for _, status := range []string{"queued", "downloading", "importing", "paused", "conflict"} {
		t.Run(status, func(t *testing.T) {
			l := newMoveLibrary(t)
			if err := l.server.TestExec(`INSERT INTO download_queue (movie_id, release_title, nzb_url, size_bytes, status) VALUES (?, 'x', 'http://example.invalid/x.nzb', 0, ?)`, int64(l.movieID), status); err != nil {
				t.Skipf("can't set up a download in this schema: %v", err)
			}
			prev := postJSON[map[string]any](t, l.client, l.base+"/api/library/move-files/preview", map[string]any{"items": l.items("movie", l.movieID), "rootPath": l.movies2}, http.StatusOK)
			first := prev["titles"].([]any)[0].(map[string]any)
			if prev["blocked"] != float64(1) || !strings.Contains(first["problem"].(string), "download") {
				t.Fatalf("preview = %v", prev)
			}
		})
	}
}

func TestMoveRequestsAreChecked(t *testing.T) {
	l := newMoveLibrary(t)
	for _, url := range []string{"/api/library/move-files/preview", "/api/library/move-files"} {
		postJSON[map[string]any](t, l.client, l.base+url, map[string]any{"items": []any{}, "rootPath": l.movies2}, http.StatusBadRequest)
		postJSON[map[string]any](t, l.client, l.base+url, map[string]any{"items": l.items("movie", l.movieID), "rootPath": "/not/a/library/folder"}, http.StatusBadRequest)
		mixed := append(l.items("movie", l.movieID), l.items("tv", l.showID)...)
		postJSON[map[string]any](t, l.client, l.base+url, map[string]any{"items": mixed, "rootPath": l.movies2}, http.StatusBadRequest)
	}
	// Stopping when nothing runs is harmless.
	postJSON[map[string]any](t, l.client, l.base+"/api/library/move-files/cancel", map[string]any{}, http.StatusOK)
}
