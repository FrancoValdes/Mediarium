package libimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A folder with more video files than the limit is refused, not read whole.
func TestWalkVideosLimit(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.mkv", "b.mkv", "c.mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if files, err := walkVideos(root, 3); err != nil || len(files) != 3 {
		t.Fatalf("at the limit: %d %v", len(files), err)
	}
	if _, err := walkVideos(root, 2); err == nil || !strings.Contains(err.Error(), "more than 2 video files") {
		t.Fatalf("over the limit: %v", err)
	}
}
