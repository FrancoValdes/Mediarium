package organizer_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rdborg/mediarium/internal/organizer"
)

// A file from an external download client: plain files are imported as
// usual and left where they are; a link is imported as a new link only when
// it leads into the folder links may point into.
func TestImportClientFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	mount := filepath.Join(root, "mnt", "remote") // the cloud drive links may point into
	client := filepath.Join(root, "client", "Movie.2020")
	secret := filepath.Join(root, "config", "secret.key")
	for _, d := range []string{filepath.Join(mount, "__all__", "Movie.2020"), client, filepath.Dir(secret), filepath.Join(mount, "escape")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, at string) {
		t.Helper()
		if err := os.Symlink(target, at); err != nil {
			t.Skip("cannot make a symlink here")
		}
	}
	onMount := filepath.Join(mount, "__all__", "Movie.2020", "Movie.2020.mkv")
	write(onMount, "movie on the cloud drive")
	write(secret, "do not link me")
	write(filepath.Join(client, "real.mkv"), "a real file")

	link(onMount, filepath.Join(client, "allowed.mkv"))
	link(secret, filepath.Join(client, "outside.mkv"))
	// A link inside the mount that leads back out of it, and a link through it.
	link(filepath.Dir(secret), filepath.Join(mount, "escape", "out"))
	link(filepath.Join(mount, "escape", "out", "secret.key"), filepath.Join(client, "through.mkv"))
	// A chain: a link to a link that ends outside.
	link(filepath.Join(client, "outside.mkv"), filepath.Join(client, "chain.mkv"))
	// "..": starts inside the mount and climbs out.
	link(filepath.Join(mount, "__all__", "..", "..", "..", "config", "secret.key"), filepath.Join(client, "dotdot.mkv"))
	// Relative, and inside: allowed.
	rel, _ := filepath.Rel(client, onMount)
	link(rel, filepath.Join(client, "relative.mkv"))
	// A link to the mount folder itself, not a file.
	link(filepath.Join(mount, "__all__"), filepath.Join(client, "folder.mkv"))
	link(filepath.Join(root, "nowhere"), filepath.Join(client, "dangling.mkv"))

	cases := []struct {
		name     string
		src      string
		linkRoot string
		wantErr  error
		wantLink string // where the new link must point
		wantFile string // the contents of a plain imported file
	}{
		{"plain file", "real.mkv", "", nil, "", "a real file"},
		{"plain file with a link folder set", "real.mkv", mount, nil, "", "a real file"},
		{"allowed link", "allowed.mkv", mount, nil, onMount, ""},
		{"relative link inside", "relative.mkv", mount, nil, onMount, ""},
		{"link without a link folder", "allowed.mkv", "", organizer.ErrLinkNotAllowed, "", ""},
		{"link outside the folder", "outside.mkv", mount, organizer.ErrLinkNotAllowed, "", ""},
		{"link through a link that leaves", "through.mkv", mount, organizer.ErrLinkNotAllowed, "", ""},
		{"chain ending outside", "chain.mkv", mount, organizer.ErrLinkNotAllowed, "", ""},
		{"dot-dot out of the folder", "dotdot.mkv", mount, organizer.ErrLinkNotAllowed, "", ""},
		{"link to a folder", "folder.mkv", mount, organizer.ErrLinkNotAllowed, "", ""},
		{"dangling link", "dangling.mkv", mount, organizer.ErrLinkNotAllowed, "", ""},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(client, tc.src)
			dest := filepath.Join(root, "library", string(rune('a'+i)), "Movie (2020).mkv")
			res, err := organizer.ImportClientFile(src, dest, organizer.ConflictSkip, nil, tc.linkRoot)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if _, err := os.Lstat(dest); !os.IsNotExist(err) {
					t.Fatalf("nothing may be created for a refused link, Lstat: %v", err)
				}
				return
			}
			if err != nil || res.DestPath != dest || res.Skipped {
				t.Fatalf("import: %+v, %v", res, err)
			}
			info, err := os.Lstat(dest)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantLink != "" {
				got, err := os.Readlink(dest)
				if info.Mode()&os.ModeSymlink == 0 || err != nil || got != tc.wantLink {
					t.Fatalf("dest should be a link to %s, got %q (%v)", tc.wantLink, got, err)
				}
				// Importing again is a no-op, not a conflict.
				again, err := organizer.ImportClientFile(src, dest, organizer.ConflictSkip, nil, tc.linkRoot)
				if err != nil || again.Skipped {
					t.Fatalf("second import: %+v, %v", again, err)
				}
				return
			}
			if !info.Mode().IsRegular() {
				t.Fatalf("a plain file should be imported as a plain file, mode %v", info.Mode())
			}
			got, _ := os.ReadFile(dest)
			if string(got) != tc.wantFile {
				t.Fatalf("contents %q", got)
			}
			// The client's file is left in place for seeding.
			if _, err := os.Stat(src); err != nil {
				t.Fatalf("the client's file is gone: %v", err)
			}
		})
	}

	// Plain Import still refuses every link, even an allowed one.
	if _, err := organizer.Import(filepath.Join(client, "allowed.mkv"), filepath.Join(root, "library", "x.mkv"), organizer.ConflictSkip, nil); !errors.Is(err, organizer.ErrNotRegularFile) {
		t.Fatalf("Import of a link: %v", err)
	}
}

// Replacing an existing library file with a link follows the conflict rules.
func TestImportClientFileReplaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	mount := filepath.Join(root, "mnt")
	if err := os.MkdirAll(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(mount, "new.mkv")
	if err := os.WriteFile(target, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "client.mkv")
	if err := os.Symlink(target, src); err != nil {
		t.Skip("cannot make a symlink here")
	}
	dest := filepath.Join(root, "library", "Movie.mkv")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := organizer.ImportClientFile(src, dest, organizer.ConflictSkip, nil, mount)
	if err != nil || !res.Skipped {
		t.Fatalf("skip: %+v %v", res, err)
	}
	res, err = organizer.ImportClientFile(src, dest, organizer.ConflictOverwriteIfBetter, func() bool { return true }, mount)
	if err != nil || res.Skipped {
		t.Fatalf("replace: %+v %v", res, err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "new" {
		t.Fatalf("dest now reads %q", got)
	}
}

// With FollowLinks the finders see a folder of links to the client's files;
// without it they keep ignoring links.
func TestFindersFollowLinksOnlyWhenAsked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	client := filepath.Join(root, "client")
	view := filepath.Join(root, "view")
	for _, d := range []string{client, view} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	big := filepath.Join(client, "Show.S01E01.mkv")
	small := filepath.Join(client, "Show.S01E02.mkv")
	if err := os.WriteFile(big, make([]byte, 2000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(small, make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{big, small} {
		if err := os.Symlink(f, filepath.Join(view, filepath.Base(f))); err != nil {
			t.Skip("cannot make a symlink here")
		}
	}
	if err := os.Symlink(client, filepath.Join(view, "folder.mkv")); err != nil {
		t.Fatal(err)
	}
	if _, err := organizer.FindLargestVideoFile(view); err == nil {
		t.Fatal("links must not count unless asked")
	}
	got, err := organizer.FindLargestVideoFile(view, organizer.FollowLinks)
	if err != nil || got != filepath.Join(view, "Show.S01E01.mkv") {
		t.Fatalf("largest = %q, %v", got, err)
	}
	files, err := organizer.FindVideoFiles(view, organizer.FollowLinks)
	if err != nil || len(files) != 2 {
		t.Fatalf("files = %v, %v", files, err)
	}
}
