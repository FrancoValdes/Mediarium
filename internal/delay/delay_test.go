package delay_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/delay"
	"github.com/rdborg/mediarium/internal/store"
)

var noon = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func TestDecide(t *testing.T) {
	cfg := delay.Config{PreferredProtocol: delay.Usenet, UsenetMinutes: 0, TorrentMinutes: 120}
	tests := []struct {
		name      string
		cfg       delay.Config
		rel       delay.Release
		wantHold  bool
		wantUntil time.Time
	}{
		{"no wait for usenet", cfg, delay.Release{Protocol: delay.Usenet, Posted: noon}, false, time.Time{}},
		{"fresh torrent waits", cfg, delay.Release{Protocol: delay.Torrent, Posted: noon.Add(-30 * time.Minute)}, true, noon.Add(90 * time.Minute)},
		{"torrent past the wait goes through", cfg, delay.Release{Protocol: delay.Torrent, Posted: noon.Add(-121 * time.Minute)}, false, time.Time{}},
		{"torrent exactly at the wait goes through", cfg, delay.Release{Protocol: delay.Torrent, Posted: noon.Add(-120 * time.Minute)}, false, time.Time{}},
		{"old torrent from a back catalogue search", cfg, delay.Release{Protocol: delay.Torrent, Posted: noon.AddDate(-3, 0, 0)}, false, time.Time{}},
		{"no posting date: first seen counts", cfg, delay.Release{Protocol: delay.Torrent, FirstSeen: noon.Add(-60 * time.Minute)}, true, noon.Add(60 * time.Minute)},
		{"no date at all: seen just now", cfg, delay.Release{Protocol: delay.Torrent}, true, noon.Add(120 * time.Minute)},
		{"posted in the future (clock skew): seen just now", cfg, delay.Release{Protocol: delay.Torrent, Posted: noon.Add(3 * time.Hour)}, true, noon.Add(120 * time.Minute)},
		{
			"best quality skips the wait on the preferred protocol",
			delay.Config{PreferredProtocol: delay.Torrent, TorrentMinutes: 60, UsenetMinutes: 60, BypassIfHighestQuality: true},
			delay.Release{Protocol: delay.Torrent, Posted: noon, Highest: true}, false, time.Time{},
		},
		{
			"best quality does not skip the wait on the other protocol",
			delay.Config{PreferredProtocol: delay.Torrent, TorrentMinutes: 60, UsenetMinutes: 60, BypassIfHighestQuality: true},
			delay.Release{Protocol: delay.Usenet, Posted: noon, Highest: true}, true, noon.Add(60 * time.Minute),
		},
		{
			"bypass off: best quality still waits",
			delay.Config{PreferredProtocol: delay.Usenet, UsenetMinutes: 30},
			delay.Release{Protocol: delay.Usenet, Posted: noon, Highest: true}, true, noon.Add(30 * time.Minute),
		},
		{
			"unset preferred protocol means usenet",
			delay.Config{UsenetMinutes: 30, BypassIfHighestQuality: true},
			delay.Release{Protocol: delay.Usenet, Posted: noon, Highest: true}, false, time.Time{},
		},
		{"everything at zero does nothing", delay.Config{}, delay.Release{Protocol: delay.Torrent, Posted: noon}, false, time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.Decide(tc.rel, noon)
			if got.Hold != tc.wantHold || !got.Until.Equal(tc.wantUntil) {
				t.Fatalf("Decide = %+v, want hold %v until %v", got, tc.wantHold, tc.wantUntil)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     delay.Config
		wantErr bool
	}{
		{"default", delay.Default(), false},
		{"both waits", delay.Config{PreferredProtocol: "torrent", UsenetMinutes: 5, TorrentMinutes: 600}, false},
		{"the longest wait", delay.Config{UsenetMinutes: delay.MaxMinutes}, false},
		{"too long", delay.Config{UsenetMinutes: delay.MaxMinutes + 1}, true},
		{"negative", delay.Config{TorrentMinutes: -1}, true},
		{"unknown protocol", delay.Config{PreferredProtocol: "carrier pigeon"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestActive(t *testing.T) {
	if delay.Default().Active() {
		t.Fatal("the default must not delay anything")
	}
	if !(delay.Config{TorrentMinutes: 1}).Active() {
		t.Fatal("a torrent wait is active")
	}
}

func TestNote(t *testing.T) {
	utc := time.UTC
	tests := []struct {
		name  string
		until time.Time
		want  string
	}{
		{"today", time.Date(2026, 10, 9, 14, 5, 0, 0, utc), "Waiting for a better protocol until 14:05"},
		{"another day", time.Date(2026, 10, 10, 1, 30, 0, 0, utc), "Waiting for a better protocol until 10 Oct 01:30"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := delay.Note(tc.until, noon, utc); got != tc.want {
				t.Fatalf("Note = %q, want %q", got, tc.want)
			}
		})
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newStore(t *testing.T) (*delay.Store, *clock) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	c := &clock{t: noon}
	return delay.NewStore(db, c.now), c
}

func TestStoreHoldWaitingAndDue(t *testing.T) {
	st, clk := newStore(t)
	movie := delay.Item{Kind: "movie", MovieID: 7}
	ep := delay.Item{Kind: "episode", SeriesID: 3, Season: 2, Episode: 5}

	first, err := st.Hold(movie, "Film.2026.2160p.WEB-DL-GRP", delay.Torrent, noon.Add(2*time.Hour))
	if err != nil || !first {
		t.Fatalf("first hold: first=%v err=%v", first, err)
	}
	first, err = st.Hold(movie, "Film.2026.2160p.WEB-DL-GRP", delay.Torrent, noon.Add(2*time.Hour))
	if err != nil || first {
		t.Fatalf("the same release again must not be first: first=%v err=%v", first, err)
	}
	if _, err := st.Hold(ep, "Show.S02E05.1080p-GRP", delay.Torrent, noon.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}

	waiting, err := st.Waiting()
	if err != nil || len(waiting) != 2 {
		t.Fatalf("waiting = %+v, %v", waiting, err)
	}
	if waiting[0].Item != ep || !waiting[0].Due.Equal(noon.Add(30*time.Minute)) {
		t.Fatalf("soonest first expected, got %+v", waiting[0])
	}

	if due, err := st.DueSearches(); err != nil || len(due) != 0 {
		t.Fatalf("nothing is due yet: %+v %v", due, err)
	}
	if got := st.FirstSeen(movie, "Film.2026.2160p.WEB-DL-GRP"); !got.Equal(noon) {
		t.Fatalf("FirstSeen = %v, want %v", got, noon)
	}

	clk.t = noon.Add(45 * time.Minute)
	due, err := st.DueSearches()
	if err != nil || len(due) != 1 || due[0] != ep {
		t.Fatalf("due = %+v, %v; want the episode only", due, err)
	}
	// a search that did not run stays due; each follow-up search runs once
	if still, _ := st.DueSearches(); len(still) != 1 {
		t.Fatalf("an unmarked search must stay due: %+v", still)
	}
	if err := st.MarkSearched(ep); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.DueSearches(); len(again) != 0 {
		t.Fatalf("a due search came back: %+v", again)
	}
	waiting, _ = st.Waiting()
	if len(waiting) != 1 || waiting[0].Item != movie {
		t.Fatalf("only the movie is still waiting, got %+v", waiting)
	}
	// the memory of the first sighting outlives the wait
	if got := st.FirstSeen(ep, "Show.S02E05.1080p-GRP"); !got.Equal(noon) {
		t.Fatalf("FirstSeen after the wait = %v", got)
	}

	if err := st.Clear(7, 0); err != nil {
		t.Fatal(err)
	}
	if waiting, _ = st.Waiting(); len(waiting) != 0 {
		t.Fatalf("a cleared movie is still waiting: %+v", waiting)
	}
}

func TestStoreLongerWaitSearchesAgain(t *testing.T) {
	st, clk := newStore(t)
	it := delay.Item{Kind: "movie", MovieID: 1}
	if _, err := st.Hold(it, "R", delay.Torrent, noon.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	clk.t = noon.Add(2 * time.Hour)
	if due, _ := st.DueSearches(); len(due) != 1 {
		t.Fatalf("expected the search to be due, got %+v", due)
	}
	if err := st.MarkSearched(it); err != nil {
		t.Fatal(err)
	}
	// the owner raises the wait: the same release is held again, later
	clk.t = noon.Add(3 * time.Hour)
	if _, err := st.Hold(it, "R", delay.Torrent, noon.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	clk.t = noon.Add(6 * time.Hour)
	if due, _ := st.DueSearches(); len(due) != 1 {
		t.Fatalf("expected a second follow-up search, got %+v", due)
	}
}

func TestStorePrune(t *testing.T) {
	st, clk := newStore(t)
	it := delay.Item{Kind: "movie", MovieID: 1}
	if _, err := st.Hold(it, "R", delay.Torrent, noon.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	clk.t = noon.AddDate(0, 0, 3)
	_ = st.Prune()
	if st.FirstSeen(it, "R").IsZero() {
		t.Fatal("pruned too early")
	}
	clk.t = noon.AddDate(0, 0, 9)
	_ = st.Prune()
	if !st.FirstSeen(it, "R").IsZero() {
		t.Fatal("an old row was kept")
	}
}
