package organizer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Two imports to the same place each stage under a name of their own, so one
// can't remove or overwrite the other's half-written file.
func TestStagingNamesAreUnique(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "Movie (2020).mkv")
	a, b := stagingPath(dest), stagingPath(dest)
	if a == b {
		t.Fatalf("two imports got the same staging name %q", a)
	}
	for _, p := range []string{a, b} {
		if filepath.Dir(p) != dir || !strings.HasSuffix(p, stagingSuffix) || !strings.HasPrefix(filepath.Base(p), "Movie (2020).mkv.") {
			t.Errorf("staging name %q", p)
		}
	}
	long := filepath.Join(dir, strings.Repeat("日", MaxNameBytes/3)+".mkv")
	if n := len(filepath.Base(stagingPath(long))); n > 255 {
		t.Errorf("staging name is %d bytes", n)
	}
}

// A link at the destination counts as something already there, even when it
// leads nowhere.
func TestImportSeesADanglingLinkAtTheDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mkv")
	if err := os.WriteFile(src, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "Movie.mkv")
	if err := os.Symlink(filepath.Join(dir, "gone.mkv"), dest); err != nil {
		t.Skip("cannot make a symlink here")
	}
	res, err := Import(src, dest, ConflictSkip, nil)
	if err != nil || !res.Skipped {
		t.Fatalf("skip policy: %+v, %v", res, err)
	}
	res, err = Import(src, dest, ConflictOverwrite, nil)
	if err != nil || res.Skipped {
		t.Fatalf("overwrite policy: %+v, %v", res, err)
	}
	if fi, err := os.Lstat(dest); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the link should be replaced by the file: %v, %v", fi, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "gone.mkv")); !os.IsNotExist(err) {
		t.Fatalf("nothing is written through the link: %v", err)
	}
}
