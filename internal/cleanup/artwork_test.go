package cleanup_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/cleanup"
)

// The pictures and NFO written next to a video that shares its folder go with
// it, and nothing that only looks similar does.
func TestFileAndSidecarsIncludeArtworkNamedAfterTheVideo(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"Film (2020).mkv", "Film (2020).en.srt", "Film (2020).nfo",
		"Film (2020)-poster.jpg", "Film (2020)-fanart.jpg", "Film (2020)-clearlogo.png",
		"Film (2020) Extended.mkv", "Film (2020) Extended-poster.jpg", // another film
		"Film (2020)-poster-old.jpg", "Film (2020)-notes.jpg", "Film (2020)-poster.txt", // not artwork
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := cleanup.FileAndSidecars(filepath.Join(dir, "Film (2020).mkv"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, filepath.Base(p))
	}
	sort.Strings(names)
	want := "Film (2020)-clearlogo.png,Film (2020)-fanart.jpg,Film (2020)-poster.jpg,Film (2020).en.srt,Film (2020).mkv,Film (2020).nfo"
	if strings.Join(names, ",") != want {
		t.Fatalf("got %v, want %s", names, want)
	}
}
