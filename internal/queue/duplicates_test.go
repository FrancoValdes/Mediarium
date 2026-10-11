package queue_test

import (
	"testing"

	"github.com/rdborg/mediarium/internal/queue"
)

func TestFindDuplicate(t *testing.T) {
	db := openDB(t)
	for _, q := range []string{
		`INSERT INTO movies (id, tmdb_id, title, year, status) VALUES (1, 101, 'A', 2000, 'missing'), (2, 102, 'B', 2001, 'missing')`,
		`INSERT INTO series (id, tmdb_id, title) VALUES (7, 107, 'Show')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	repo := queue.NewRepo(db)
	add := func(it queue.Item, st queue.Status, hash string) int64 {
		t.Helper()
		id, err := repo.Enqueue(it)
		if err != nil {
			t.Fatal(err)
		}
		if st != queue.StatusQueued {
			if err := repo.SetStatus(id, st, ""); err != nil {
				t.Fatal(err)
			}
		}
		if hash != "" {
			if err := repo.SetNZBHash(id, hash); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	done := add(queue.Item{MovieID: 1, ReleaseTitle: "Movie.A.2000.1080p", SizeBytes: 5000}, queue.StatusCompleted, "hash-a")
	failed := add(queue.Item{MovieID: 2, ReleaseTitle: "Movie.B.2001.720p", SizeBytes: 3000}, queue.StatusFailed, "hash-b")
	stopped := add(queue.Item{MovieID: 2, ReleaseTitle: "Movie.B.2001.480p", SizeBytes: 1000}, queue.StatusStopped, "hash-b2")
	episode := add(queue.Item{SeriesID: 7, Season: 1, Episode: 2, ReleaseTitle: "Show.S01E02.1080p", SizeBytes: 800}, queue.StatusDownloading, "")
	pack := add(queue.Item{SeriesID: 7, Season: 1, ReleaseTitle: "Show.S01.1080p", SizeBytes: 9000}, queue.StatusQueued, "")
	_, _ = failed, stopped
	twinA := add(queue.Item{MovieID: 2, ReleaseTitle: "Movie.B.Twin.1080p", SizeBytes: 77}, queue.StatusQueued, "")
	twinB := add(queue.Item{MovieID: 2, ReleaseTitle: "Movie.B.Twin.1080p", SizeBytes: 77}, queue.StatusQueued, "")

	tests := []struct {
		name string
		item queue.Item
		hash string
		want int64 // 0 = no duplicate
	}{
		{"same name and size for the same movie", queue.Item{MovieID: 1, ReleaseTitle: "Movie.A.2000.1080p", SizeBytes: 5000}, "", done},
		{"name compared without case", queue.Item{MovieID: 1, ReleaseTitle: "movie.a.2000.1080P", SizeBytes: 5000}, "", done},
		{"same name, other size", queue.Item{MovieID: 1, ReleaseTitle: "Movie.A.2000.1080p", SizeBytes: 5001}, "", 0},
		{"same size, other name", queue.Item{MovieID: 1, ReleaseTitle: "Movie.A.2000.2160p", SizeBytes: 5000}, "", 0},
		{"same name and size but another movie", queue.Item{MovieID: 2, ReleaseTitle: "Movie.A.2000.1080p", SizeBytes: 5000}, "", 0},
		{"same NZB under another name", queue.Item{MovieID: 1, ReleaseTitle: "Totally.Different.Name", SizeBytes: 1}, "hash-a", done},
		{"another NZB", queue.Item{MovieID: 1, ReleaseTitle: "Totally.Different.Name", SizeBytes: 1}, "hash-z", 0},
		{"same NZB but for another movie", queue.Item{MovieID: 2, ReleaseTitle: "Other", SizeBytes: 1}, "hash-a", 0},
		{"a failed earlier try is not a duplicate", queue.Item{MovieID: 2, ReleaseTitle: "Movie.B.2001.720p", SizeBytes: 3000}, "hash-b", 0},
		{"a stopped earlier try is not a duplicate", queue.Item{MovieID: 2, ReleaseTitle: "Movie.B.2001.480p", SizeBytes: 1000}, "hash-b2", 0},
		{"same episode in the queue", queue.Item{SeriesID: 7, Season: 1, Episode: 2, ReleaseTitle: "Show.S01E02.1080p", SizeBytes: 800}, "", episode},
		{"other episode", queue.Item{SeriesID: 7, Season: 1, Episode: 3, ReleaseTitle: "Show.S01E02.1080p", SizeBytes: 800}, "", 0},
		{"other season", queue.Item{SeriesID: 7, Season: 2, Episode: 2, ReleaseTitle: "Show.S01E02.1080p", SizeBytes: 800}, "", 0},
		{"season pack waiting in line", queue.Item{SeriesID: 7, Season: 1, ReleaseTitle: "Show.S01.1080p", SizeBytes: 9000}, "", pack},
		{"the item is not its own duplicate", queue.Item{ID: done, MovieID: 1, ReleaseTitle: "Movie.A.2000.1080p", SizeBytes: 5000}, "hash-a", 0},
		{"the later of two identical items is the duplicate", queue.Item{ID: twinB, MovieID: 2, ReleaseTitle: "Movie.B.Twin.1080p", SizeBytes: 77}, "", twinA},
		{"the earlier of two identical items is not", queue.Item{ID: twinA, MovieID: 2, ReleaseTitle: "Movie.B.Twin.1080p", SizeBytes: 77}, "", 0},
		{"nothing says what it is for", queue.Item{ReleaseTitle: "Movie.A.2000.1080p", SizeBytes: 5000}, "hash-a", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := repo.FindDuplicate(tt.item, tt.hash)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == 0 {
				if found {
					t.Fatalf("found duplicate %d (%q), want none", got.ID, got.ReleaseTitle)
				}
				return
			}
			if !found || got.ID != tt.want {
				t.Fatalf("found = %v id = %d, want %d", found, got.ID, tt.want)
			}
		})
	}
}
