package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCachedFileSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if got := cachedFileSize(path, now); got != 5 {
		t.Fatalf("size %d, want 5", got)
	}
	// Within the cache time the disk isn't asked again.
	if err := os.WriteFile(path, []byte("1234567890"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cachedFileSize(path, now.Add(time.Minute)); got != 5 {
		t.Fatalf("cached size %d, want 5", got)
	}
	// Once it is out of date the old size is still handed out at once, and the
	// disk is read again in the background.
	late := now.Add(sizeCacheFor + time.Second)
	if got := cachedFileSize(path, late); got != 5 {
		t.Fatalf("size after the cache time %d, want the old 5 at once", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for cachedFileSize(path, late) != 10 {
		if time.Now().After(deadline) {
			t.Fatalf("the size was not read again in the background")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := cachedFileSize(filepath.Join(t.TempDir(), "gone.mkv"), now); got != 0 {
		t.Fatalf("a missing file: %d", got)
	}
	if got := cachedFileSize("", now); got != 0 {
		t.Fatalf("no path: %d", got)
	}
}
