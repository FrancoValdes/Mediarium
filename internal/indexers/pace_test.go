package indexers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Searches on one indexer are spaced apart, each indexer on its own, and a
// longer gap is kept for one that said "too many requests".
func TestPaceSpacesSearchesOnOneIndexer(t *testing.T) {
	tr := NewTracker(nil, nil)
	gap := 80 * time.Millisecond
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := tr.Pace(context.Background(), 1, gap); err != nil {
			t.Fatalf("pace gave up: %v", err)
		}
	}
	if took := time.Since(start); took < 2*gap-10*time.Millisecond {
		t.Fatalf("three searches should take at least two gaps, took %s", took)
	}
	other := time.Now()
	tr.Pace(context.Background(), 2, gap)
	if time.Since(other) > gap/2 {
		t.Fatal("another indexer isn't held up by the first")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr.Pace(context.Background(), 3, time.Minute)
	if err := tr.Pace(ctx, 3, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled search stops waiting, got %v", err)
	}
	var none *Tracker
	if err := none.Pace(context.Background(), 1, gap); err != nil {
		t.Fatalf("no tracker, no waiting: %v", err)
	}
}

// A search that would wait longer than maxPacedWaiting is skipped at once
// instead of queueing for hours.
func TestPaceSkipsAWaitThatIsTooLong(t *testing.T) {
	tr := NewTracker(nil, nil)
	gap := maxPacedWaiting/2 + time.Second
	if err := tr.Pace(context.Background(), 1, gap); err != nil {
		t.Fatal(err)
	}
	// the next one would wait about half the limit: it queues
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tr.Pace(ctx, 1, gap) }()
	waitFor(t, func() bool { return lane(tr, 1).waiting == 1 })
	// one more would wait the whole limit and more
	start := time.Now()
	if err := tr.Pace(context.Background(), 1, gap); !errors.Is(err, ErrSearchQueueFull) {
		t.Fatalf("got %v, want ErrSearchQueueFull", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("the skipped search waited")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("the waiting search: %v", err)
	}
}

// Searches that give up while waiting don't push back the ones after them.
func TestPaceCancelledWaitsDoNotDelayOthers(t *testing.T) {
	tr := NewTracker(nil, nil)
	gap := 300 * time.Millisecond
	if err := tr.Pace(context.Background(), 1, gap); err != nil {
		t.Fatal(err)
	}
	// five searches line up and are all cancelled
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() { errs <- tr.Pace(ctx, 1, gap) }()
	}
	waitFor(t, func() bool { return lane(tr, 1).waiting == 5 })
	cancel()
	for i := 0; i < 5; i++ {
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled wait: %v", err)
		}
	}
	// the next one waits one gap after the last search that started, not six
	start := time.Now()
	if err := tr.Pace(context.Background(), 1, gap); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2*gap {
		t.Fatalf("waited %s after cancelled searches; should be at most one gap", took)
	}
}

func lane(tr *Tracker, id int64) paceLane {
	tr.paceMu.Lock()
	defer tr.paceMu.Unlock()
	if l := tr.lanes[id]; l != nil {
		return *l
	}
	return paceLane{}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSearchGapIsLongerAfterTooManyRequests(t *testing.T) {
	tr := NewTracker(nil, nil)
	if got := tr.SearchGap(7); got != gapNormal {
		t.Fatalf("a quiet indexer: %s", got)
	}
	tr.mu.Lock()
	tr.loaded = true
	tr.health[7] = Health{Failures: 1, Reason: "it said you made too many requests"}
	tr.mu.Unlock()
	if got := tr.SearchGap(7); got != gapLimited {
		t.Fatalf("after too many requests: %s", got)
	}
}

// Noting where releases came from stays cheap once the notes are full: old
// ones are dropped oldest first, never by scanning everything on every add.
func TestRememberedSourcesStayBoundedAndCheap(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr := NewTracker(nil, func() time.Time { return now })
	batch := func(prefix string, n int) []Result {
		out := make([]Result, n)
		for i := range out {
			out[i] = Result{DownloadURL: fmt.Sprintf("%s/%d", prefix, i)}
		}
		return out
	}
	start := time.Now()
	for i := 0; i < 2000; i++ {
		tr.remember(1, batch(fmt.Sprintf("https://a.example/%d", i), 100))
		now = now.Add(time.Second)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("200,000 notes took %s", took)
	}
	if n := len(tr.sources); n > maxSource {
		t.Fatalf("%d notes kept, want at most %d", n, maxSource)
	}
	if len(tr.order)-tr.orderHead > 2*maxSource {
		t.Fatalf("the order list grew to %d", len(tr.order)-tr.orderHead)
	}
	// the newest are kept, the oldest gone
	if _, ok := tr.sources["https://a.example/1999/5"]; !ok {
		t.Fatal("the newest note was dropped")
	}
	if _, ok := tr.sources["https://a.example/0/5"]; ok {
		t.Fatal("the oldest note was kept")
	}
	// a release seen again keeps its newer note when its old one is dropped
	tr2 := NewTracker(nil, func() time.Time { return now })
	tr2.remember(1, []Result{{DownloadURL: "x"}})
	now = now.Add(sourceTTL / 2)
	tr2.remember(2, []Result{{DownloadURL: "x"}})
	now = now.Add(sourceTTL/2 + time.Minute)
	tr2.remember(3, []Result{{DownloadURL: "y"}})
	if n, ok := tr2.sources["x"]; !ok || n.id != 2 {
		t.Fatalf("the newer note was dropped with the old one: %+v %v", n, ok)
	}
}
