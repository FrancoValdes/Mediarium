package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// After a title's folder is changed, only its next files go to the new
// folder. Work on the files it already has (rename, rescan, delete, remove)
// uses the folder those files are in, not the new one.
func TestFolderChangeKeepsExistingFilesWorking(t *testing.T) {
	l := newMoveLibrary(t)
	putJSONStatus(t, l.client, fmt.Sprintf("%s/api/movies/%d/folder", l.base, int64(l.movieID)), map[string]any{"rootPath": l.movies2}, http.StatusOK)
	putJSONStatus(t, l.client, fmt.Sprintf("%s/api/series/%d/folder", l.base, int64(l.showID)), map[string]any{"rootPath": l.tv2}, http.StatusOK)

	// Rename: the preview never proposes moving a file to the other folder.
	putFile(t, filepath.Join(l.movies, "The Matrix (1999)", "the.matrix.1999.mkv"), "video")
	if err := l.server.TestExec(`UPDATE movies SET file_path = ? WHERE id = ?`, filepath.Join(l.movies, "The Matrix (1999)", "the.matrix.1999.mkv"), int64(l.movieID)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(l.movie); err != nil {
		t.Fatal(err)
	}
	type item struct {
		Kind string `json:"kind"`
		ID   int64  `json:"id"`
		To   string `json:"to"`
	}
	for _, p := range getJSON[[]item](t, l.client, l.base+"/api/rename?kind=tv") {
		if !strings.HasPrefix(p.To, l.tv+string(filepath.Separator)) {
			t.Fatalf("the rename would move an episode out of its folder: %+v", p)
		}
	}
	plan := getJSON[[]item](t, l.client, l.base+"/api/rename?kind=movie")
	if len(plan) != 1 {
		t.Fatalf("the movie's file should be in the rename preview: %+v", plan)
	}
	for _, p := range plan {
		if !strings.HasPrefix(p.To, l.movies+string(filepath.Separator)) {
			t.Fatalf("the rename would move the movie out of its folder: %+v", p)
		}
	}
	var picked []map[string]any
	for _, p := range plan {
		picked = append(picked, map[string]any{"kind": p.Kind, "id": p.ID})
	}
	res := postJSON[map[string]any](t, l.client, l.base+"/api/rename", map[string]any{"items": picked}, http.StatusOK)
	if res["renamed"] != float64(len(plan)) {
		t.Fatalf("rename: %v", res)
	}
	movie, _ := l.server.MovieRepo.Get(int64(l.movieID))
	file := movie.FilePath
	if !strings.HasPrefix(file, l.movies+string(filepath.Separator)) || !exists(file) {
		t.Fatalf("the movie's file should be renamed where it is: %q", file)
	}

	// Rescan files: the old folder (not the new, empty one) decides whether the
	// library is there, so a file that is gone is marked missing.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	got := postJSON[map[string]any](t, l.client, fmt.Sprintf("%s/api/movies/%d/rescan", l.base, int64(l.movieID)), nil, http.StatusOK)
	if got["missing"] != float64(1) || got["skipped"] != float64(0) {
		t.Fatalf("rescan after a folder change: %v", got)
	}

	// Delete one episode's file, then remove the show with its files.
	eps, err := l.server.MovieRepo.ListEpisodes(int64(l.showID))
	if err != nil {
		t.Fatal(err)
	}
	var s2 int64
	for _, e := range eps {
		if e.Season == 2 && e.Episode == 1 {
			s2 = e.ID
		}
	}
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/episodes/%d/file", l.base, s2), nil)
	resp, err := l.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deleting an episode's file after a folder change: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/series/%d?deleteFiles=true", l.base, int64(l.showID)), nil)
	resp, err = l.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("removing the show with its files after a folder change: %d", resp.StatusCode)
	}
	if exists(l.ep1) || exists(l.ep2) {
		t.Fatal("the show's files should be gone")
	}
}
