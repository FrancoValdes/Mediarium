package api_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/api"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/queue"
)

// movableClock is a clock a test moves by hand.
type movableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *movableClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *movableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// heldMovieEnv has a movie whose only release is held back by a two hour wait,
// with the clock of the held-release store in the test's hands.
type heldMovieEnv struct {
	server *api.Server
	client *http.Client
	base   string
	k      *knobIndexer
	clock  *movableClock
	idle   library.Movie
}

func newHeldMovieEnv(t *testing.T) *heldMovieEnv {
	t.Helper()
	server, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	signIn(t, client, base)
	k := newKnobIndexer(t)
	addKnobIndexer(t, client, base, "Fixture Indexer", k)
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/search/delay", map[string]any{"usenetMinutes": 120}, http.StatusOK)
	clock := &movableClock{t: time.Now()}
	server.TestSetDelayClock(clock.now)
	if _, err := server.MovieRepo.Add(library.Movie{TMDBID: 801, Title: "The Fixture Movie", Year: 1999, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	idle, err := server.MovieRepo.Add(library.Movie{TMDBID: 999, Title: "Something Else", Year: 1990, Monitored: false})
	if err != nil {
		t.Fatal(err)
	}
	server.TestHunt(context.Background())
	if got := queueTitles(t, client, base); len(got) != 0 {
		t.Fatalf("the fresh release was grabbed: %v", got)
	}
	// Three hours pass: the wait is over and the release is old enough.
	clock.advance(3 * time.Hour)
	k.ageMinutes.Store(185)
	return &heldMovieEnv{server: server, client: client, base: base, k: k, clock: clock, idle: idle}
}

// dueRows is how many held releases still wait for their follow-up search.
func (e *heldMovieEnv) dueRows(t *testing.T) string {
	t.Helper()
	v, err := e.server.TestScalar(`SELECT COUNT(*) FROM delayed_releases WHERE searched = 0`)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// The follow-up search is an automatic one: while the cap of automatic
// downloads is reached it does not run, and the title stays due.
func TestDelayedFollowUpWaitsForRoomUnderTheAutomaticCap(t *testing.T) {
	e := newHeldMovieEnv(t)
	for i := 0; i < 10; i++ {
		id, err := e.server.QueueRepo.Enqueue(queue.Item{MovieID: e.idle.ID, ReleaseTitle: fmt.Sprintf("Old.Release.%d", i), NZBURL: "http://example.invalid/x.nzb"})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.server.QueueRepo.SetStatus(id, queue.StatusCompleted, ""); err != nil {
			t.Fatal(err)
		}
	}
	hits := e.k.hits.Load()
	e.server.TestDelayedGrabsJob(context.Background())
	if e.k.hits.Load() != hits {
		t.Fatal("the follow-up searched although ten automatic downloads were added this hour")
	}
	if got := e.dueRows(t); got != "1" {
		t.Fatalf("the follow-up was counted as done without running (%s rows still due)", got)
	}

	// The hour passes for the downloads that filled the cap.
	if err := e.server.TestExec(`DELETE FROM download_queue`); err != nil {
		t.Fatal(err)
	}
	e.server.TestDelayedGrabsJob(context.Background())
	if got := queueTitles(t, e.client, e.base); len(got) != 1 || got[0] != knobRelease {
		t.Fatalf("with room again the follow-up must grab the release, queue = %v", got)
	}
	if got := e.dueRows(t); got != "0" {
		t.Fatalf("%s rows still due after the search ran", got)
	}
	waitForQueueTerminal(t, e.client, e.base)
}

// A follow-up that finds every indexer paused has not searched anything, so it
// stays due until an indexer answers again.
func TestDelayedFollowUpStaysDueWhileEveryIndexerIsPaused(t *testing.T) {
	e := newHeldMovieEnv(t)
	e.k.status.Store(http.StatusServiceUnavailable)
	for i := 0; i < 3; i++ {
		resp, err := e.client.Get(e.base + "/api/search?q=matrix")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	hits := e.k.hits.Load()
	e.server.TestDelayedGrabsJob(context.Background())
	if e.k.hits.Load() != hits {
		t.Fatal("a paused indexer was asked")
	}
	if got := e.dueRows(t); got != "1" {
		t.Fatalf("the follow-up was lost while every indexer was paused (%s rows still due)", got)
	}

	e.k.status.Store(0)
	e.k.retryAfter.Store(0)
	for _, r := range getJSON[[]map[string]any](t, e.client, e.base+"/api/indexers") {
		postJSON[any](t, e.client, fmt.Sprintf("%s/api/indexers/%d/resume", e.base, int64(r["id"].(float64))), nil, http.StatusOK)
	}
	e.server.TestDelayedGrabsJob(context.Background())
	if got := queueTitles(t, e.client, e.base); len(got) != 1 || got[0] != knobRelease {
		t.Fatalf("after the indexer is back the follow-up must grab the release, queue = %v", got)
	}
	waitForQueueTerminal(t, e.client, e.base)
}
