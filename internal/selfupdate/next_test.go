package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestNextProgram(t *testing.T) {
	dir := t.TempDir()
	self, _ := os.Executable()
	if got, err := NextProgram(dir, ""); err != nil || got != self {
		t.Fatalf("outside the image: %q %v", got, err)
	}
	image := "/app/app"
	if got, _ := NextProgram(dir, image); got != image {
		t.Fatalf("no update installed: %q", got)
	}
	app := filepath.Join(dir, FileApp)
	body := []byte("#!/bin/sh\necho mediarium\n")
	if err := os.WriteFile(app, body, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if err := os.WriteFile(filepath.Join(dir, FileSum), []byte(hex.EncodeToString(sum[:])+"  app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := NextProgram(dir, image); got != app || BootCount(dir) != 1 {
		t.Fatalf("installed update: %q, count %d", got, BootCount(dir))
	}
	// Three starts without proving itself: back to the image.
	_, _ = NextProgram(dir, image)
	_, _ = NextProgram(dir, image)
	if got, _ := NextProgram(dir, image); got != image {
		t.Fatalf("after 3 failed starts: %q", got)
	}
	MarkHealthy(dir)
	// A file that no longer matches its checksum is never run.
	if err := os.WriteFile(app, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := NextProgram(dir, image); got != image {
		t.Fatalf("tampered update: %q", got)
	}
}
