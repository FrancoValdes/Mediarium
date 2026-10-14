package queue_test

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/queue"
)

// With the "newest first" rule on, the automatic downloads of newer releases
// start before older ones; what a person asked for still goes first.
func TestNewestFirstRule(t *testing.T) {
	db := openDB(t)
	repo := queue.NewRepo(db)
	lib := library.NewRepo(db)
	t.Cleanup(func() { queue.SetNewestFirst(false) })

	var ids []int64
	for i, date := range []string{"1999-05-01", "2024-03-10", "2011-07-04"} {
		m, err := lib.Add(library.Movie{TMDBID: 100 + i, Title: "Film " + date})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE movies SET release_date = ? WHERE id = ?`, date, m.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, seedItem(t, repo, m.ID, queue.StatusQueued))
	}
	next := func() int64 {
		it, ok, err := repo.NextWaiting(false)
		if err != nil || !ok {
			t.Fatalf("next waiting: %v %v", ok, err)
		}
		return it.ID
	}

	queue.SetNewestFirst(false)
	if got := next(); got != ids[0] {
		t.Fatalf("first come, first served: want %d, got %d", ids[0], got)
	}
	queue.SetNewestFirst(true)
	if got := next(); got != ids[1] {
		t.Fatalf("the newest release (2024) should start first: want %d, got %d", ids[1], got)
	}
	// What is moved to the front still beats a newer release.
	if ok, err := repo.MoveToFront(ids[0]); err != nil || !ok {
		t.Fatalf("move to front: %v %v", ok, err)
	}
	if got := next(); got != ids[0] {
		t.Fatalf("a download moved to the front goes first: want %d, got %d", ids[0], got)
	}
	// The order shown on the page follows the same rule.
	items, _ := repo.List()
	byID := map[int64]queue.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if !queue.LineOrder(byID[ids[1]], byID[ids[2]]) || queue.LineOrder(byID[ids[2]], byID[ids[1]]) {
		t.Fatalf("2024 is ahead of 2011 in the line: %q vs %q", byID[ids[1]].Released, byID[ids[2]].Released)
	}
}

// Under "newest first", something with no release date (a season pack, an
// album, a book) ranks as if it came out the day it was added, so it is not
// held back behind every dated release; and a resumed download goes first in
// its priority whatever the dates say.
func TestNewestFirstUndatedAndResumed(t *testing.T) {
	db := openDB(t)
	repo := queue.NewRepo(db)
	lib := library.NewRepo(db)
	t.Cleanup(func() { queue.SetNewestFirst(false) })
	queue.SetNewestFirst(true)

	dated := func(tmdb int, date string) int64 {
		m, err := lib.Add(library.Movie{TMDBID: tmdb, Title: "Film " + date})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE movies SET release_date = ? WHERE id = ?`, date, m.ID); err != nil {
			t.Fatal(err)
		}
		return seedItem(t, repo, m.ID, queue.StatusQueued)
	}
	old := dated(201, "2001-01-01")
	undated, err := repo.Enqueue(queue.Item{ReleaseTitle: "Some.Album.FLAC"}) // no movie or episode: no date
	if err != nil {
		t.Fatal(err)
	}
	// The album was added a year ago; a film from last month is newer, one
	// from five years ago is not.
	yearAgo := time.Now().AddDate(-1, 0, 0).UTC()
	if _, err := db.Exec(`UPDATE download_queue SET added_at = ? WHERE id = ?`, yearAgo.Format("2006-01-02T15:04:05.000Z"), undated); err != nil {
		t.Fatal(err)
	}
	recent := dated(202, time.Now().AddDate(0, -1, 0).Format("2006-01-02"))
	older := dated(203, time.Now().AddDate(-5, 0, 0).Format("2006-01-02"))

	order := func() []int64 {
		var out []int64
		for {
			it, ok, err := repo.NextWaiting(false)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				return out
			}
			out = append(out, it.ID)
			if ok, err := repo.Claim(it.ID); err != nil || !ok {
				t.Fatalf("claim %d: %v %v", it.ID, ok, err)
			}
		}
	}
	reset := func(ids ...int64) {
		for _, id := range ids {
			if _, err := db.Exec(`UPDATE download_queue SET status = 'queued' WHERE id = ?`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := []int64{recent, undated, older, old}
	got := order()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("start order = %v, want %v (recent film, the album added a year ago, then the older films)", got, want)
	}
	// The list on the page agrees.
	reset(want...)
	assertListOrder(t, repo, want)

	// The oldest film was paused and is resumed: it goes first, ahead of the
	// newer releases.
	if err := repo.Pause(old, false); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.Requeue(old); err != nil || !ok {
		t.Fatalf("requeue: %v %v", ok, err)
	}
	want = []int64{old, recent, undated, older}
	assertListOrder(t, repo, want)
	if got := order(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("after resuming, start order = %v, want %v", got, want)
	}

	// A new item added afterwards never counts as resumed.
	reset(want...)
	late := dated(204, "1990-01-01")
	if it, _ := repo.Get(late); it.LineSeq <= 0 {
		t.Fatalf("a new item got place %d, which marks resumed items", it.LineSeq)
	}
}

// assertListOrder sorts the waiting items with LineOrder and compares.
func assertListOrder(t *testing.T, repo *queue.Repo, want []int64) {
	t.Helper()
	items, err := repo.ListActive()
	if err != nil {
		t.Fatal(err)
	}
	var waiting []queue.Item
	for _, it := range items {
		if it.Status == queue.StatusQueued {
			waiting = append(waiting, it)
		}
	}
	sort.SliceStable(waiting, func(i, j int) bool { return queue.LineOrder(waiting[i], waiting[j]) })
	var got []int64
	for _, it := range waiting {
		got = append(got, it.ID)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("list order = %v, want %v", got, want)
	}
}
