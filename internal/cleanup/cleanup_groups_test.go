package cleanup_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/cleanup"
)

// TestScanLooksInsideCategoryFolders: a torrent category keeps its downloads in
// a folder of its own inside the working folder. The downloads in it are
// judged like any other, and the folder itself goes only once it is empty.
func TestScanLooksInsideCategoryFolders(t *testing.T) {
	l := newLayout(t)
	l.area.Groups = []string{"movies", "tv", "music"}
	write(t, filepath.Join(l.work, "movies", "queue-1", "movie.mkv"), "12345") // imported leftover
	write(t, filepath.Join(l.work, "movies", "queue-2", "part.mkv"), "busy")   // a running download
	write(t, filepath.Join(l.work, "tv", "queue-3", "ep.mkv"), "12345")        // imported leftover
	// A download that has just started and has no files yet.
	if err := os.MkdirAll(filepath.Join(l.work, "tv", "queue-4"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A category folder with nothing in it.
	if err := os.MkdirAll(filepath.Join(l.work, "music"), 0o755); err != nil {
		t.Fatal(err)
	}

	items, err := cleanup.Scan(l.area, func(name string, info fs.FileInfo) cleanup.Decision {
		switch name {
		case "queue-1", "queue-3":
			return cleanup.Decision{Remove: true, Reason: cleanup.ReasonImportedLeftover}
		case "queue-2", "queue-4":
			return cleanup.Decision{Active: true}
		}
		// A category folder must never be handed to the caller.
		t.Errorf("classify was asked about %q", name)
		return cleanup.Decision{Remove: true, Reason: cleanup.ReasonOrphaned}
	})
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, it := range items {
		found = append(found, it.Path+" "+string(it.Reason))
	}
	sort.Strings(found)
	want := []string{
		"incomplete/movies/queue-1 imported-leftover",
		"incomplete/music empty-folder",
		"incomplete/tv/queue-3 imported-leftover",
	}
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Fatalf("scan found\n%s\nwant\n%s", strings.Join(found, "\n"), strings.Join(want, "\n"))
	}
	if _, err := cleanup.Remove(l.area, items); err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"movies/queue-2/part.mkv", "tv/queue-4"} {
		if !exists(filepath.Join(l.work, filepath.FromSlash(kept))) {
			t.Errorf("%s must be kept", kept)
		}
	}
	if exists(filepath.Join(l.work, "music")) || exists(filepath.Join(l.work, "movies", "queue-1")) {
		t.Error("the removed entries are still there")
	}
	l.requirePrecious(t)
}

// TestCategoryFolderNamesMatchWithoutRegardToCase: on a disk that does not tell
// Movies from movies the category folder is still recognised.
func TestCategoryFolderNamesMatchWithoutRegardToCase(t *testing.T) {
	l := newLayout(t)
	l.area.Groups = []string{"Movies"}
	write(t, filepath.Join(l.work, "movies", "queue-1", "movie.mkv"), "12345")
	items, err := cleanup.Scan(l.area, func(name string, info fs.FileInfo) cleanup.Decision {
		return cleanup.Decision{Remove: true, Reason: cleanup.ReasonImportedLeftover}
	})
	if err != nil || len(items) != 1 || items[0].Path != "incomplete/movies/queue-1" {
		t.Fatalf("items = %+v, %v", items, err)
	}
}
