package api

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStillThere(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("v"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	here := write("a/Here (2020).mkv")
	moved := write("b/Moved (2020).mkv")
	write("c/Twice (2020).mkv")
	write("c/Twice (2020).mp4")
	write("d/Other (2020).mkv")

	cases := []struct {
		name     string
		path     string
		wantNow  string
		wantGone bool
	}{
		{"still there", here, here, false},
		{"new extension", filepath.Join(dir, "b", "Moved (2020).mp4"), moved, false},
		{"two look-alikes: can't tell", filepath.Join(dir, "c", "Twice (2020).avi"), "", false},
		{"gone, folder still there", filepath.Join(dir, "d", "Gone (2020).mkv"), "", true},
		{"gone with its folder", filepath.Join(dir, "e", "Gone (2020).mkv"), "", true},
		{"no path", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now, gone := stillThere(c.path)
			if now != c.wantNow || gone != c.wantGone {
				t.Fatalf("stillThere(%q) = %q, %v; want %q, %v", c.path, now, gone, c.wantNow, c.wantGone)
			}
		})
	}

	// A folder that can't be read is not "gone".
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked")
		write("locked/Locked (2020).mkv")
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		if now, gone := stillThere(filepath.Join(locked, "Locked (2020).mkv")); now != "" || gone {
			t.Fatalf("an unreadable folder: %q, %v; want \"\", false", now, gone)
		}
	}
}

// A side file that can't be renamed stops the rename before the video moves,
// and what was already renamed is put back, so the library never points at a
// file that isn't there.
func TestRenameWithSidecarsPutsBackOnFailure(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "old", "Movie.mkv")
	files := []string{from, filepath.Join(root, "old", "Movie.en.srt"), filepath.Join(root, "old", "Movie.nfo")}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(filepath.Base(f)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	to := filepath.Join(root, "Movie (2020)", "Movie (2020).mkv")

	var renamed []string
	renameFile = func(a, b string) error {
		if strings.HasSuffix(a, ".nfo") {
			return errors.New("disk said no")
		}
		renamed = append(renamed, filepath.Base(a))
		return os.Rename(a, b)
	}
	t.Cleanup(func() { renameFile = os.Rename })

	if err := renameWithSidecars(root, from, to); err == nil {
		t.Fatal("the rename should fail")
	}
	for _, n := range renamed {
		if n == "Movie.mkv" {
			t.Fatal("the video must not move before its side files")
		}
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s should be back in its place: %v", filepath.Base(f), err)
		}
	}
	if _, err := os.Stat(filepath.Dir(to)); err == nil {
		t.Fatal("the new folder, left empty, should be removed")
	}

	// With nothing failing, everything moves and the video goes last.
	renamed = nil
	renameFile = func(a, b string) error {
		renamed = append(renamed, filepath.Base(a))
		return os.Rename(a, b)
	}
	if err := renameWithSidecars(root, from, to); err != nil {
		t.Fatal(err)
	}
	if len(renamed) != 3 || renamed[2] != "Movie.mkv" {
		t.Fatalf("renamed in order %v, want the video last", renamed)
	}
	for _, n := range []string{"Movie (2020).mkv", "Movie (2020).en.srt", "Movie (2020).nfo"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(to), n)); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
}
