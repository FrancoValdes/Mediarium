package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rdborg/mediarium/internal/library"
)

// A smaller version takes the place of the movie's big file, and the big file
// goes only once the new one is in the library.
func TestReplaceWithSmallerVersion(t *testing.T) {
	const name = "The Fixture Movie (1999)"
	tests := []struct {
		name string
		// old is where the movie's file is, relative to the movies folder.
		old string
		// current says whether the movie still points at that file when the
		// download finishes (false: it was changed in the meantime).
		current bool
		// policy is "If a file already exists" ("" leaves the default).
		policy string
		// wantOldGone says the old file must be gone afterwards.
		wantOldGone bool
	}{
		{"another file name: the old file is removed", name + "/" + name + " [2160p].mkv", true, "", true},
		{"the same file name: replaced in place", name + "/" + name + ".mkv", true, "", true},
		{"the same file name, with the setting on skip", name + "/" + name + ".mkv", true, "skip", true},
		{"the movie's file changed meanwhile: nothing is removed", name + "/" + name + " [2160p].mkv", false, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newControlEnv(t, -1)
			if tc.policy != "" {
				putSettings(t, e, map[string]any{"importConflictPolicy": tc.policy})
			}
			oldPath := filepath.Join(e.movies, filepath.FromSlash(tc.old))
			putFile(t, oldPath, "the big old version")
			current := oldPath
			var other string
			if !tc.current {
				other = filepath.Join(e.movies, name, name+" [other].mkv")
				putFile(t, other, "another file")
				current = other
			}
			if err := e.server.MovieRepo.SetStatus(e.movie.ID, library.StatusDownloaded, "Remux-2160p", current); err != nil {
				t.Fatal(err)
			}

			id, err := e.server.TestGrabMovieReplacing(e.movie, releaseName, e.nzbURL, oldPath)
			if err != nil {
				t.Fatalf("grab: %v", err)
			}
			waitFor(t, "the download to end", func() bool { s := e.status(t, id); return s == "completed" || s == "failed" })
			if got := e.status(t, id); got != "completed" {
				t.Fatalf("the download ended %s: %v", got, e.item(t, id)["error"])
			}

			newPath := filepath.Join(e.movies, name, name+".mkv")
			if got, err := os.ReadFile(newPath); err != nil || string(got) != string(e.wantFile) {
				t.Fatalf("the new file: %q, %v", got, err)
			}
			m, _ := e.server.MovieRepo.Get(e.movie.ID)
			if m.FilePath != newPath || m.Quality != "WEBDL-1080p" {
				t.Errorf("the movie is %q at %s, want WEBDL-1080p at %s", m.Quality, m.FilePath, newPath)
			}
			if oldPath != newPath && exists(oldPath) == tc.wantOldGone {
				t.Errorf("old file still there = %v, want %v (library: %s)", exists(oldPath), !tc.wantOldGone, tree(t, e.movies))
			}
			if other != "" && !exists(other) {
				t.Errorf("a file the movie had meanwhile must stay (library: %s)", tree(t, e.movies))
			}
		})
	}
}

// A download that fails leaves the old file, and the movie as downloaded.
func TestReplaceWithSmallerVersionKeepsTheOldFileWhenTheDownloadFails(t *testing.T) {
	e := newControlEnv(t, -1)
	broken := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(broken.Close)
	oldPath := filepath.Join(e.movies, "The Fixture Movie (1999)", "The Fixture Movie (1999).mkv")
	putFile(t, oldPath, "the big old version")
	if err := e.server.MovieRepo.SetStatus(e.movie.ID, library.StatusDownloaded, "Remux-2160p", oldPath); err != nil {
		t.Fatal(err)
	}

	id, err := e.server.TestGrabMovieReplacing(e.movie, releaseName, broken.URL+"/x.nzb", oldPath)
	if err != nil {
		t.Fatalf("grab: %v", err)
	}
	waitFor(t, "the download to end", func() bool { s := e.status(t, id); return s == "completed" || s == "failed" })
	if got := e.status(t, id); got != "failed" {
		t.Fatalf("the download should fail, it ended %s", got)
	}
	if got, _ := os.ReadFile(oldPath); string(got) != "the big old version" {
		t.Fatalf("the old file must stay as it was, got %q", got)
	}
	m, _ := e.server.MovieRepo.Get(e.movie.ID)
	if m.Status != library.StatusDownloaded || m.FilePath != oldPath || m.Quality != "Remux-2160p" {
		t.Fatalf("the movie should still be downloaded with its old file: %+v", m)
	}
}

// The same lower-quality release, grabbed the ordinary way, is still turned
// away: only "Replace with a smaller version" swaps a better file for a worse one.
func TestAnOrdinaryGrabNeverSwapsInALowerQuality(t *testing.T) {
	e := newControlEnv(t, -1)
	oldPath := filepath.Join(e.movies, "The Fixture Movie (1999)", "The Fixture Movie (1999).mkv")
	putFile(t, oldPath, "the big old version")
	if err := e.server.MovieRepo.SetStatus(e.movie.ID, library.StatusDownloaded, "Remux-2160p", oldPath); err != nil {
		t.Fatal(err)
	}
	id := e.grab(t)
	waitFor(t, "the download to end", func() bool { s := e.status(t, id); return s == "completed" || s == "failed" })
	if got, _ := os.ReadFile(oldPath); string(got) != "the big old version" {
		t.Fatalf("the old file must stay, got %q", got)
	}
}
