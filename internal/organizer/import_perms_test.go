package organizer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rdborg/mediarium/internal/fileperms"
)

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestImportAppliesTheConfiguredModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	t.Cleanup(func() { fileperms.Set(fileperms.Modes{}) })
	fileperms.Set(fileperms.Modes{File: 0o664, Dir: 0o775})

	work := t.TempDir()
	lib := filepath.Join(work, "movies")
	if err := os.Mkdir(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(work, "download.mkv")
	if err := os.WriteFile(src, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(lib, "Film (2020)", "Film (2020).mkv")
	res, err := Import(src, dest, ConflictSkip, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatal("the import was skipped")
	}
	if got := perm(t, dest); got != 0o664 {
		t.Errorf("file has %o, want 664", got)
	}
	if got := perm(t, filepath.Dir(dest)); got != 0o775 {
		t.Errorf("folder has %o, want 775", got)
	}
	if got := perm(t, lib); got != 0o755 {
		t.Errorf("the library folder was there already and has %o, want it left at 755", got)
	}

	// Replacing a file puts the new one in with the mode too.
	src2 := filepath.Join(work, "better.mkv")
	if err := os.WriteFile(src2, []byte("better video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(src2, dest, ConflictOverwrite, nil); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, dest); got != 0o664 {
		t.Errorf("replaced file has %o, want 664", got)
	}

	// A file that is skipped is not touched.
	other := filepath.Join(lib, "Other (2021)", "Other (2021).mkv")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = Import(src, other, ConflictSkip, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatal("expected the existing file to be kept")
	}
	if got := perm(t, other); got != 0o600 {
		t.Errorf("a skipped file was changed to %o", got)
	}
}

func TestImportWithNoModesLeavesThemToTheUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	fileperms.Set(fileperms.Modes{})
	work := t.TempDir()
	src := filepath.Join(work, "download.mkv")
	if err := os.WriteFile(src, []byte("video"), 0o640); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(work, "lib", "Film", "Film.mkv")
	if _, err := Import(src, dest, ConflictSkip, nil); err != nil {
		t.Fatal(err)
	}
	// Hardlinked: still the mode the download had. Nothing was changed.
	if got := perm(t, dest); got != 0o640 {
		t.Errorf("file has %o, want the download's 640", got)
	}
	if got := perm(t, filepath.Dir(dest)); got&^0o755 != 0 {
		t.Errorf("folder has %o, want at most 755", got)
	}
}

// A copy (another drive) is created by copyFile, which asks for 0666 and so
// ends up with whatever the umask leaves, then gets the configured mode.
func TestImportedCopyGetsTheFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	t.Cleanup(func() { fileperms.Set(fileperms.Modes{}) })
	fileperms.Set(fileperms.Modes{File: 0o640})
	work := t.TempDir()
	src := filepath.Join(work, "a.mkv")
	if err := os.WriteFile(src, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(src)
	staging := filepath.Join(work, "b.mkv"+stagingSuffix)
	if err := copyFile(src, staging, info); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(work, "b.mkv")
	if err := publishNew(staging, dest); err != nil {
		t.Fatal(err)
	}
	if err := fileperms.ApplyFile(dest); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, dest); got != 0o640 {
		t.Errorf("copied file has %o, want 640", got)
	}
}
