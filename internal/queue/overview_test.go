package queue_test

import (
	"testing"

	"github.com/rdborg/mediarium/internal/queue"
)

func TestCountAndListUnfinished(t *testing.T) {
	db := openDB(t)
	if _, err := db.Exec(`INSERT INTO movies (id, tmdb_id, title, year, status) VALUES (1, 101, 'A', 2000, 'missing')`); err != nil {
		t.Fatal(err)
	}
	repo := queue.NewRepo(db)
	for _, st := range []queue.Status{queue.StatusQueued, queue.StatusDownloading, queue.StatusDownloading, queue.StatusCompleted, queue.StatusFailed, queue.StatusStopped, queue.StatusPaused} {
		id, err := repo.Enqueue(queue.Item{MovieID: 1, ReleaseTitle: "R." + string(st)})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.SetStatus(id, st, ""); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := repo.CountByStatus()
	if err != nil {
		t.Fatal(err)
	}
	if counts[queue.StatusDownloading] != 2 || counts[queue.StatusQueued] != 1 || counts[queue.StatusFailed] != 1 || counts[queue.StatusCompleted] != 1 {
		t.Fatalf("counts = %v", counts)
	}
	open, err := repo.ListUnfinished()
	if err != nil {
		t.Fatal(err)
	}
	all, _ := repo.List()
	if len(all) != 7 || len(open) != 4 {
		t.Fatalf("all %d, unfinished %d (queued, 2 downloading and paused are still open)", len(all), len(open))
	}
	for _, it := range open {
		if it.Status == queue.StatusCompleted || it.Status == queue.StatusFailed || it.Status == queue.StatusStopped {
			t.Fatalf("a finished item came back: %+v", it)
		}
	}
}

func TestLastSearchesPickTheNewestPerTitle(t *testing.T) {
	db := openDB(t)
	for _, q := range []string{
		`INSERT INTO movies (id, tmdb_id, title, year, status) VALUES (1, 101, 'A', 2000, 'missing'), (2, 102, 'B', 2001, 'missing'), (3, 103, 'C', 2002, 'missing')`,
		`INSERT INTO series (id, tmdb_id, title) VALUES (7, 107, 'Show')`,
		// movie 1: two searches and a newer grab; movie 2: none; show 7: one search.
		`INSERT INTO activity (movie_id, event_type, message, created_at, item_only) VALUES (1, 'searched', 'first', '2026-10-01T10:00:00Z', 1)`,
		`INSERT INTO activity (movie_id, event_type, message, created_at, item_only) VALUES (1, 'searched', 'second', '2026-10-02T10:00:00Z', 1)`,
		`INSERT INTO activity (movie_id, event_type, message, created_at, item_only) VALUES (1, 'grabbed', 'newest but not a search', '2026-10-03T10:00:00Z', 0)`,
		`INSERT INTO activity (movie_id, event_type, message, created_at, item_only) VALUES (2, 'grabbed', 'no search here', '2026-10-03T10:00:00Z', 0)`,
		`INSERT INTO activity (series_id, event_type, message, created_at, item_only) VALUES (7, 'searched', 'show search', '2026-10-04T10:00:00Z', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	repo := queue.NewRepo(db)
	got, err := repo.LastMovieSearches([]int64{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[1].Message != "second" || got[1].At != "2026-10-02T10:00:00Z" || got[1].Kind != "searched" {
		t.Fatalf("movie searches = %+v", got)
	}
	shows, err := repo.LastSeriesSearches([]int64{7})
	if err != nil || shows[7].Message != "show search" {
		t.Fatalf("series searches = %+v, %v", shows, err)
	}
	if none, err := repo.LastMovieSearches(nil); err != nil || len(none) != 0 {
		t.Fatalf("no ids = %+v, %v", none, err)
	}
	// More ids than one statement takes.
	ids := make([]int64, 1000)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if many, err := repo.LastMovieSearches(ids); err != nil || len(many) != 1 {
		t.Fatalf("many ids = %d, %v", len(many), err)
	}
}
