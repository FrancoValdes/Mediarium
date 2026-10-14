package books_test

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rdborg/mediarium/internal/books"
)

func TestComicPagesComeInReadingOrder(t *testing.T) {
	got := books.ComicPages([]string{
		"ComicInfo.xml", "__MACOSX/._page1.jpg", "Thumbs.db", "pages/", "pages/page10.jpg", "pages/page2.JPG", "pages/Page1.jpg", ".hidden/a.png", "pages/page02b.png", "cover.webp",
	})
	want := []string{"cover.webp", "pages/Page1.jpg", "pages/page2.JPG", "pages/page02b.png", "pages/page10.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pages = %v, want %v", got, want)
	}
}

func writeCBZ(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestComicInfoNamesTheComic(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.cbz")
	writeCBZ(t, file, map[string]string{
		"ComicInfo.xml": `<?xml version="1.0"?><ComicInfo><Title>Origins</Title><Series>Night Harbour</Series><Number>3</Number><Writer>Ana Gray, Bo Lee</Writer><Year>2019</Year><Manga>YesAndRightToLeft</Manga></ComicInfo>`,
		"1.jpg":         "x",
	})
	ci, ok := books.ReadComicInfo(file)
	if !ok || ci.Series != "Night Harbour" || ci.Number != "3" || ci.Year != 2019 || ci.Manga != "YesAndRightToLeft" {
		t.Fatalf("info = %+v ok=%v", ci, ok)
	}
	if got := ci.DisplayTitle(); got != "Night Harbour #3: Origins" {
		t.Fatalf("title = %q", got)
	}
	if got := (books.ComicInfo{Series: "Night Harbour", Number: "4", Title: "Night Harbour #4"}).DisplayTitle(); got != "Night Harbour #4" {
		t.Fatalf("a title that repeats the name isn't added twice: %q", got)
	}
	if _, ok := books.ReadComicInfo(filepath.Join(dir, "missing.cbz")); ok {
		t.Fatal("a missing file has no info")
	}

	// A books folder with comics in it finds them, with what they say about themselves.
	root := filepath.Join(dir, "lib")
	if err := os.MkdirAll(filepath.Join(root, "Some Publisher"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCBZ(t, filepath.Join(root, "Some Publisher", "issue-03.cbz"), map[string]string{
		"ComicInfo.xml": `<ComicInfo><Title>Origins</Title><Series>Night Harbour</Series><Number>3</Number><Writer>Ana Gray</Writer></ComicInfo>`, "1.jpg": "x",
	})
	writeCBZ(t, filepath.Join(root, "Plain Comic.cbz"), map[string]string{"1.jpg": "x"})
	found, err := books.ScanEbooks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found %+v", found)
	}
	byTitle := map[string]books.LocalBook{}
	for _, b := range found {
		byTitle[b.Title] = b
	}
	got := byTitle["Night Harbour #3: Origins"]
	if got.Author != "Ana Gray" || got.Series != "Night Harbour" || got.Position != "3" || got.Format != "cbz" {
		t.Fatalf("comic with info: %+v", got)
	}
	if byTitle["Plain Comic"].Format != "cbz" {
		t.Fatalf("a comic with no info is named by its file: %+v", found)
	}
}

// An odd <Year> doesn't throw away the rest of what the comic says.
func TestComicInfoOddYear(t *testing.T) {
	cases := []struct {
		year string
		want int
	}{
		{"2019", 2019},
		{" 2021-05 ", 2021},
		{"c. 1990", 1990},
		{"unknown", 0},
		{"", 0},
		{"99999999999999999999", 0},
	}
	dir := t.TempDir()
	for i, tc := range cases {
		file := filepath.Join(dir, fmt.Sprintf("%d.cbz", i))
		writeCBZ(t, file, map[string]string{
			"ComicInfo.xml": `<ComicInfo><Series>Night Harbour</Series><Number>3</Number><Year>` + tc.year + `</Year></ComicInfo>`,
			"1.jpg":         "x",
		})
		ci, ok := books.ReadComicInfo(file)
		if !ok || ci.Series != "Night Harbour" || ci.Number != "3" || ci.Year != tc.want {
			t.Errorf("year %q: %+v ok=%v", tc.year, ci, ok)
		}
	}
}

// A folder of issues, the usual comic layout, is one book per issue.
func TestScanComicIssuesFolder(t *testing.T) {
	root := t.TempDir()
	run := filepath.Join(root, "Batman", "Batman (2016)")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Batman 001.cbr", "Batman 002.cbr", "Batman 002.cbz", "Batman #12.cb7", "Batman Annual Issue 4.cbr"} {
		writeCBZ(t, filepath.Join(run, name), map[string]string{"1.jpg": "x"})
	}
	numbers := filepath.Join(root, "Saga")
	if err := os.MkdirAll(numbers, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001.cbz", "002.cbz"} {
		writeCBZ(t, filepath.Join(numbers, name), map[string]string{"1.jpg": "x"})
	}
	// A single comic in its own folder still takes its name from the folder.
	if err := os.MkdirAll(filepath.Join(root, "Ana Gray", "Night Harbour"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCBZ(t, filepath.Join(root, "Ana Gray", "Night Harbour", "nh.cbz"), map[string]string{"1.jpg": "x"})

	found, err := books.ScanEbooks(root)
	if err != nil {
		t.Fatal(err)
	}
	type book struct {
		title, series, position, format string
		year, files                     int
	}
	got := map[string]book{}
	for _, b := range found {
		got[b.Title] = book{b.Title, b.Series, b.Position, b.Format, b.Year, b.Files}
	}
	want := map[string]book{
		"Batman 001":            {"Batman 001", "Batman", "1", "cbr", 2016, 1},
		"Batman 002":            {"Batman 002", "Batman", "2", "cbr", 2016, 2},
		"Batman #12":            {"Batman #12", "Batman", "12", "cb7", 2016, 1},
		"Batman Annual Issue 4": {"Batman Annual Issue 4", "Batman Annual", "4", "cbr", 2016, 1},
		"Saga #1":               {"Saga #1", "Saga", "1", "cbz", 0, 1},
		"Saga #2":               {"Saga #2", "Saga", "2", "cbz", 0, 1},
		"Night Harbour":         {"Night Harbour", "", "", "cbz", 0, 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestGuessComicIssue(t *testing.T) {
	cases := []struct {
		name, series, position string
	}{
		{"Batman 001", "Batman", "1"},
		{"Batman #12", "Batman", "12"},
		{"Batman # 0.5", "Batman", "0.5"},
		{"Saga v3", "Saga", "3"},
		{"Saga Vol. 3", "Saga", "3"},
		{"Batman Issue 4", "Batman", "4"},
		{"Batman_000 (2016) [digital]", "Batman", "0"},
		{"X-Men - 017", "X-Men", "17"},
		{"001", "", "1"},
		{"Watchmen", "", ""},
		{"Spider-Man 2099", "", ""},
	}
	for _, tc := range cases {
		s, p := books.GuessComicIssue(tc.name)
		if s != tc.series || p != tc.position {
			t.Errorf("GuessComicIssue(%q) = %q, %q", tc.name, s, p)
		}
	}
}
