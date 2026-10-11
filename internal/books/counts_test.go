package books_test

import (
	"path/filepath"
	"testing"

	"github.com/rdborg/mediarium/internal/books"
	"github.com/rdborg/mediarium/internal/store"
)

func newRepo(t *testing.T) *books.Repo {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return books.NewRepo(db)
}

func TestCountsByWantedFormat(t *testing.T) {
	r := newRepo(t)
	add := func(key string, ebook, audio bool) books.Book {
		b, err := r.Add(books.Book{OLKey: key, Title: key, WantEbook: ebook, WantAudiobook: audio})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	a := add("OL1W", true, false)
	b := add("OL2W", true, true)
	add("OL3W", false, true)
	add("OL4W", false, false)
	if err := r.SetState(a.ID, books.Ebook, books.StatusDownloaded, "epub", "/b/a.epub"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetState(b.ID, books.Ebook, books.StatusDownloading, "", ""); err != nil {
		t.Fatal(err)
	}
	// A format that is not wanted is not counted, whatever it says.
	if err := r.SetState(a.ID, books.Audiobook, books.StatusDownloaded, "m4b", "/b/a"); err != nil {
		t.Fatal(err)
	}
	ebooks, audio, err := r.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if want := (books.FormatCounts{Books: 2, Downloaded: 1, Missing: 0}); ebooks != want {
		t.Fatalf("ebooks = %+v, want %+v", ebooks, want)
	}
	if want := (books.FormatCounts{Books: 2, Downloaded: 0, Missing: 2}); audio != want {
		t.Fatalf("audiobooks = %+v, want %+v", audio, want)
	}
}

func TestListReleasingKeepsTheWindowAndWantedBooks(t *testing.T) {
	r := newRepo(t)
	for _, b := range []books.Book{
		{OLKey: "OL1W", Title: "In window", WantEbook: true, ReleaseDate: "2026-10-20"},
		{OLKey: "OL2W", Title: "Too late", WantEbook: true, ReleaseDate: "2027-12-20"},
		{OLKey: "OL3W", Title: "Not wanted", ReleaseDate: "2026-10-21"},
		{OLKey: "OL4W", Title: "Year only", WantEbook: true, ReleaseDate: "2026-10"},
		{OLKey: "OL5W", Title: "No date", WantAudiobook: true},
	} {
		if _, err := r.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.ListReleasing("2026-09-09", "2027-02-06")
	if err != nil || len(got) != 1 || got[0].Title != "In window" {
		t.Fatalf("releasing = %+v, %v", got, err)
	}
}
