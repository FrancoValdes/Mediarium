package fileperms

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		want    os.FileMode
		wantErr bool
	}{
		{"", 0, false},
		{"  ", 0, false},
		{"644", 0o644, false},
		{"0644", 0o644, false},
		{"0o755", 0o755, false},
		{" 775 ", 0o775, false},
		{"600", 0o600, false},
		{"64", 0, true},
		{"6444", 0, true},
		{"1755", 0, true}, // sticky bit
		{"4755", 0, true}, // setuid
		{"688", 0, true},
		{"rw-r--r--", 0, true},
		{"-644", 0, true},
		{"0x1a4", 0, true},
		{"6 4 4", 0, true},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("Parse(%q) error = %v, want error %v", c.in, err, c.wantErr)
			continue
		}
		if err != nil && !errors.Is(err, ErrBadMode) {
			t.Errorf("Parse(%q) error %v does not wrap ErrBadMode", c.in, err)
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %o, want %o", c.in, got, c.want)
		}
	}
}

func TestFormat(t *testing.T) {
	for in, want := range map[os.FileMode]string{0: "", 0o644: "644", 0o755: "755", 0o600: "600", 0o070: "070"} {
		if got := Format(in); got != want {
			t.Errorf("Format(%o) = %q, want %q", in, got, want)
		}
	}
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name      string
		mode      os.FileMode
		dir       bool
		wantWorld bool
		wantErr   bool
	}{
		{"unset", 0, false, false, false},
		{"file 644", 0o644, false, false, false},
		{"file 664", 0o664, false, false, false},
		{"file 666 is world-writable", 0o666, false, true, false},
		{"file 646 is world-writable", 0o646, false, true, false},
		{"file 444 owner can't write", 0o444, false, false, true},
		{"file 044 owner has nothing", 0o044, false, false, true},
		{"dir 755", 0o755, true, false, false},
		{"dir 775", 0o775, true, false, false},
		{"dir 777 is world-writable", 0o777, true, true, false},
		{"dir 757 is world-writable", 0o757, true, true, false},
		{"dir 644 owner can't enter", 0o644, true, false, true},
		{"dir 555 owner can't write", 0o555, true, false, true},
		{"setuid bit", os.ModeSetuid | 0o755, true, false, true},
	}
	for _, c := range cases {
		world, err := Check(c.mode, c.dir)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: error = %v, want error %v", c.name, err, c.wantErr)
		}
		if world != c.wantWorld {
			t.Errorf("%s: worldWritable = %v, want %v", c.name, world, c.wantWorld)
		}
	}
}

func TestUnsetChangesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	Set(Modes{})
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Whatever the umask gave: 0755 with a normal umask. Just not changed to
	// something else by us.
	if info.Mode().Perm()&^0o755 != 0 {
		t.Errorf("folder got %o with no mode set, want at most 755", info.Mode().Perm())
	}
	f := filepath.Join(dir, "x.mkv")
	if err := os.WriteFile(f, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := ApplyFile(f); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(f)
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("ApplyFile with no mode set changed the file to %o", fi.Mode().Perm())
	}
}

func TestMkdirAllAppliesModeOnlyToNewFolders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	t.Cleanup(func() { Set(Modes{}) })
	Set(Modes{Dir: 0o770})

	root := t.TempDir()
	if err := os.Chmod(root, 0o751); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(root, "Movies")
	if err := os.Mkdir(existing, 0o711); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o711); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(existing, "Film (2020)", "Extras")
	if err := MkdirAll(target); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		root:                                   0o751, // the library root was there: untouched
		existing:                               0o711, // so was this one
		filepath.Join(existing, "Film (2020)"): 0o770,
		target:                                 0o770,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has %o, want %o", path, got, want)
		}
	}

	// Calling it again on a folder that exists changes nothing.
	if err := os.Chmod(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAll(target); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o750 {
		t.Errorf("an existing folder was changed to %o", info.Mode().Perm())
	}
}

func TestApplyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	t.Cleanup(func() { Set(Modes{}) })
	Set(Modes{File: 0o664})

	dir := t.TempDir()
	f := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyFile(f); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(f); info.Mode().Perm() != 0o664 {
		t.Errorf("file has %o, want 664", info.Mode().Perm())
	}

	// A link is left alone, and so is the file it leads to.
	target := filepath.Join(dir, "client-file.mkv")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.mkv")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}
	if err := ApplyFile(link); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o600 {
		t.Errorf("the file behind a link was changed to %o", info.Mode().Perm())
	}

	// A missing file is an error the caller can ignore.
	if err := ApplyFile(filepath.Join(dir, "nope.mkv")); err == nil {
		t.Error("expected an error for a missing file")
	}
}
