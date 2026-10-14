package organizer_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/organizer"
)

// manyEntries is a listing of n harmless files, long enough to pass 64 KB
// when n is a few hundred.
func manyEntries(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("extras/a-rather-long-folder-name/file-%04d.txt|1|A -rw-r--r--", i))
	}
	return out
}

// The whole listing is checked, however long: a link or a size far down the
// list is found before 7z unpacks anything.
func TestExtract7zChecksTheWholeListing(t *testing.T) {
	tests := []struct {
		name string
		last string
		want error
	}{
		{"a link after 64 KB", "x|11|A lrwxrwxrwx", organizer.ErrUnsafeArchive},
		{"too much data after 64 KB", "big.mkv|5000000000000|A -rw-r--r--", organizer.ErrArchiveTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listing := listing7z(append(manyEntries(600), tt.last)...)
			if len(listing) <= 64<<10 {
				t.Fatalf("the listing is only %d bytes", len(listing))
			}
			ex := fake7z(t, listing, "touch SHOULD-NOT-RUN")
			ex.MaxTotalBytes = 1 << 30
			archive, dest := fresh7zDest(t)
			if err := ex.Extract(archive, dest); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if left := entriesIn(t, dest); len(left) != 0 {
				t.Errorf("a refused archive must leave nothing behind, found %v", left)
			}
		})
	}
}

// A file name with a line break must not split one entry into two and hide
// half of it from the checks.
func TestExtract7zRefusesNamesThatBreakTheListing(t *testing.T) {
	head := "Listing archive: release.7z\n\n--\nPath = release.7z\nType = 7z\n\n----------\n"
	tests := []struct {
		name    string
		listing string
	}{
		{"fields with no name", head + "Path = a\n\nSize = 11\nAttributes = A lrwxrwxrwx\nSymbolic Link = /config\n\n"},
		{"a name over two lines", head + "Path = a\nb\nSize = 1\nAttributes = A -rw-r--r--\n\n"},
		{"a name with a tab", head + "Path = a\tb\nSize = 1\nAttributes = A -rw-r--r--\n\n"},
		{"a key twice", head + "Path = a\nSize = 1\nAttributes = A -rw-r--r--\nAttributes = A lrwxrwxrwx\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := fake7z(t, tt.listing, "touch SHOULD-NOT-RUN")
			archive, dest := fresh7zDest(t)
			if err := ex.Extract(archive, dest); !errors.Is(err, organizer.ErrUnsafeArchive) {
				t.Fatalf("err = %v, want ErrUnsafeArchive", err)
			}
			if left := entriesIn(t, dest); len(left) != 0 {
				t.Errorf("found %v", left)
			}
		})
	}

	// An empty value and text after the list (a warning) are fine.
	ok := head + "Path = a.mkv\nSize = 4\nPacked Size =\nAttributes = A -rw-r--r--\n\nWarnings: 1\n"
	ex := fake7z(t, ok, "printf data > a.mkv")
	archive, dest := fresh7zDest(t)
	if err := ex.Extract(archive, dest); err != nil {
		t.Fatal(err)
	}
}

// With the real 7z tool: a link stored after hundreds of other files is found.
func TestExtractReal7zFindsALinkAtTheEnd(t *testing.T) {
	ex := organizer.NewExtractor()
	if !ex.Available() {
		t.Skip("7z binary not available on this machine")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	files := filepath.Join(src, "a-rather-long-folder-name-for-the-extras")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 600 {
		writeTestFile(t, filepath.Join(files, fmt.Sprintf("file-%04d.txt", i)), []byte("x"))
	}
	if err := os.Symlink("/etc", filepath.Join(src, "zz-link")); err != nil {
		t.Skip("cannot make a symlink here")
	}
	archive := filepath.Join(dir, "release.7z")
	cmd := exec.Command("7z", "a", "-snl", archive, filepath.Base(files), "zz-link")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the fixture: %v: %s", err, out)
	}
	list, err := exec.Command("7z", "l", "-slt", archive).Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) <= 64<<10 || !strings.Contains(string(list[len(list)-2000:]), "zz-link") {
		t.Fatalf("the link should come after the first 64 KB of a %d byte listing", len(list))
	}
	dest := filepath.Join(dir, "dest")
	if err := ex.Extract(archive, dest); !errors.Is(err, organizer.ErrUnsafeArchive) {
		t.Fatalf("err = %v, want ErrUnsafeArchive", err)
	}
	if left := entriesIn(t, dest); len(left) != 0 {
		t.Errorf("found %d entries", len(left))
	}
}

// Unpacking again over a file that is hard-linked into the library replaces
// the download's name and never rewrites the library's copy, even when the
// second unpack fails.
func TestExtractNeverRewritesAnExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hard links behave differently on Windows")
	}
	root := t.TempDir()
	dest := filepath.Join(root, "download")
	library := filepath.Join(root, "library", "movie.mkv")
	if err := os.MkdirAll(filepath.Dir(library), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, library, []byte("the library copy"))
	if err := os.Link(library, filepath.Join(dest, "movie.mkv")); err != nil {
		t.Skip("no hard links here")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "movie.mkv", Method: zip.Store})
	_, _ = w.Write(bytes.Repeat([]byte("n"), 500))
	_ = zw.Close()
	archive := filepath.Join(root, "release.zip")
	writeTestFile(t, archive, buf.Bytes())

	small := &organizer.Extractor{MaxTotalBytes: 100}
	if err := small.Extract(archive, dest); !errors.Is(err, organizer.ErrArchiveTooLarge) {
		t.Fatalf("err = %v, want ErrArchiveTooLarge", err)
	}
	if got := readTestFile(t, library); got != "the library copy" {
		t.Fatalf("the library file was changed: %q", got)
	}

	if err := organizer.NewExtractor().Extract(archive, dest); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, library); got != "the library copy" {
		t.Fatalf("the library file was rewritten: %q", got)
	}
	if got := readTestFile(t, filepath.Join(dest, "movie.mkv")); len(got) != 500 {
		t.Fatalf("the download's file is %d bytes, want 500", len(got))
	}
	for _, e := range entriesIn(t, dest) {
		if strings.HasPrefix(filepath.Base(e), ".unpack-") {
			t.Errorf("a working file was left behind: %s", e)
		}
	}
}

// The format is read from the file's first bytes, whatever its name says.
func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name string
		head string
		want organizer.Format
		ok   bool
	}{
		{"zip", "PK\x03\x04rest", organizer.FormatZip, true},
		{"empty zip", "PK\x05\x06", organizer.FormatZip, true},
		{"rar 4", "Rar!\x1a\x07\x00", organizer.FormatRAR, true},
		{"rar 5", "Rar!\x1a\x07\x01\x00", organizer.FormatRAR, true},
		{"7z", "7z\xbc\xaf\x27\x1c\x00\x04", organizer.Format7z, true},
		{"a picture", "\x89PNG\r\n\x1a\n", "", false},
		{"too short", "PK", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := organizer.DetectFormat(strings.NewReader(tt.head))
			if got != tt.want || ok != tt.ok {
				t.Fatalf("DetectFormat = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// A comic named .cbr that is really a ZIP, and one named .cbz that is really
// a RAR, both unpack once the format is given.
func TestExtractAsIgnoresTheName(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("page1.jpg")
	_, _ = w.Write([]byte("zip page"))
	_ = zw.Close()
	cbr := filepath.Join(root, "Comic 01.cbr")
	writeTestFile(t, cbr, buf.Bytes())
	cbz := filepath.Join(root, "Comic 02.cbz")
	writeTestFile(t, cbz, rarSingle(rarEntry{name: "page1.jpg", data: []byte("rar page")}))

	if err := organizer.NewExtractor().Extract(cbr, filepath.Join(root, "x")); !errors.Is(err, organizer.ErrUnsupportedArchive) {
		t.Fatalf("Extract still goes by the name: %v", err)
	}
	for file, want := range map[string]string{cbr: "zip page", cbz: "rar page"} {
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		format, ok := organizer.DetectFormat(f)
		f.Close()
		if !ok {
			t.Fatalf("%s: format not found", file)
		}
		dest := filepath.Join(root, "out-"+filepath.Base(file))
		if err := organizer.NewExtractor().ExtractAs(file, dest, format); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if got := readTestFile(t, filepath.Join(dest, "page1.jpg")); got != want {
			t.Errorf("%s: page1.jpg = %q, want %q", file, got, want)
		}
	}
}
