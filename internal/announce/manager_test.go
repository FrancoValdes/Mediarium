package announce

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/crypto"
	"github.com/rdborg/mediarium/internal/store"
)

type fakeDecider struct {
	calls atomic.Int64
	mu    sync.Mutex
	seen  []string
	reply func(a Announce, o DecideOptions) Decision
}

func (f *fakeDecider) Decide(_ context.Context, _ Connection, a Announce, o DecideOptions) Decision {
	f.calls.Add(1)
	f.mu.Lock()
	f.seen = append(f.seen, a.Release)
	f.mu.Unlock()
	if f.reply != nil {
		return f.reply(a, o)
	}
	return Decision{Action: DecisionSkipped, Reason: "Not wanted."}
}

func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := crypto.LoadOrCreateKey(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	return NewRepo(db, box)
}

func testConn(f *fakeIRC) Connection {
	host, port := f.addr()
	p, _ := PresetByID("one-line-details-link")
	return Connection{
		Name: "t", Enabled: true, Server: host, Port: port, Nick: "mybot", Auth: AuthNone,
		Channels: []Channel{{Name: "#announce"}}, Announcers: []string{"AnnounceBot"}, TrackerURL: "https://tracker.example",
		Template: p.Template, Keys: map[string]string{"passkey": "PK"}, MaxPerHour: 5, MaxPerDay: 20,
	}
}

func newTestManager(t *testing.T, repo *Repo, d Decider) *Manager {
	t.Helper()
	m := NewManager(repo, d, nil)
	m.Tweak = func(c *Config) {
		c.AllowPrivate = true
		c.BackoffMin, c.BackoffMax = 20*time.Millisecond, 100*time.Millisecond
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	return m
}

func line(release string, id int) string {
	return fmt.Sprintf("[Movies/HD] %s - https://tracker.example/details.php?id=%d - 1.5 GB", release, id)
}

func TestManagerFloodIsBoundedAndLogged(t *testing.T) {
	f := newFakeIRC(t)
	f.serve(nil)
	repo := newTestRepo(t)
	conn, err := repo.Create(testConn(f))
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDecider{}
	m := newTestManager(t, repo, d)
	waitFor(t, "joined", func() bool { return m.InfoFor(conn.ID).State == "joined" })

	for i := 0; i < 600; i++ {
		f.Say("AnnounceBot", "#announce", line(fmt.Sprintf("Some.Film.%d.1080p.BluRay.x264-GRP", i), i))
	}
	waitFor(t, "the flood to be seen", func() bool { return m.InfoFor(conn.ID).Heard >= 600 })
	info := m.InfoFor(conn.ID)
	if info.Dropped == 0 {
		t.Fatal("a flood of 600 lines in a moment was not slowed down")
	}
	if got := len(m.Log(conn.ID)); got > ringSize {
		t.Fatalf("log holds %d entries, at most %d are kept", got, ringSize)
	}
}

func TestManagerReloadStartsAndStopsConnections(t *testing.T) {
	f := newFakeIRC(t)
	f.serve(nil)
	repo := newTestRepo(t)
	c := testConn(f)
	c.Enabled = false
	conn, err := repo.Create(c)
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, repo, &fakeDecider{})
	time.Sleep(100 * time.Millisecond)
	if m.InfoFor(conn.ID).State != "stopped" || f.welcomeCount() != 0 {
		t.Fatal("a switched-off connection connected")
	}
	if err := repo.SetEnabled(conn.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "joined", func() bool { return m.InfoFor(conn.ID).State == "joined" })
	if err := repo.SetEnabled(conn.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if st := m.InfoFor(conn.ID).State; st != "stopped" {
		t.Fatalf("state after switching off = %q", st)
	}
}

func TestManagerReportsABrokenTemplateInsteadOfRunning(t *testing.T) {
	f := newFakeIRC(t)
	f.serve(nil)
	repo := newTestRepo(t)
	c := testConn(f)
	c.Template.Patterns = []string{"(?P<releaseName>"}
	conn, err := repo.Create(c)
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, repo, &fakeDecider{})
	if p := m.InfoFor(conn.ID).Problem; !strings.Contains(p, "not a valid expression") {
		t.Fatalf("problem = %q", p)
	}
	if f.welcomeCount() != 0 {
		t.Fatal("connected with a template that does not compile")
	}
}

func TestManagerGrabsAreCountedFromTheDatabase(t *testing.T) {
	f := newFakeIRC(t)
	f.serve(nil)
	repo := newTestRepo(t)
	c := testConn(f)
	c.MaxPerHour = 2
	conn, err := repo.Create(c)
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDecider{reply: func(a Announce, o DecideOptions) Decision {
		if ok, why := o.Allow(); !ok {
			return Decision{Action: DecisionSkipped, Reason: why}
		}
		return Decision{Action: DecisionGrabbed, Reason: "ok", Item: "x"}
	}}
	m := newTestManager(t, repo, d)
	waitFor(t, "joined", func() bool { return m.InfoFor(conn.ID).State == "joined" })
	for i := 0; i < 4; i++ {
		f.Say("AnnounceBot", "#announce", line(fmt.Sprintf("Film.%d.1080p.BluRay.x264-GRP", i), i))
		time.Sleep(30 * time.Millisecond)
	}
	waitFor(t, "four entries", func() bool { return len(m.Log(conn.ID)) >= 4 })
	grabbed, held := 0, 0
	for _, e := range m.Log(conn.ID) {
		switch e.Decision {
		case DecisionGrabbed:
			grabbed++
		case DecisionSkipped:
			if strings.Contains(e.Reason, "an hour") {
				held++
			}
		}
	}
	if grabbed != 2 || held != 2 {
		t.Fatalf("grabbed %d and held back %d, want 2 and 2: %+v", grabbed, held, m.Log(conn.ID))
	}
	// A restart remembers: the stored grabs still count and the same releases are duplicates.
	m.Stop()
	m2 := newTestManager(t, repo, d)
	waitFor(t, "joined again", func() bool { return m2.InfoFor(conn.ID).State == "joined" })
	if n, _ := repo.CountGrabs(conn.ID, "", time.Now().Add(-time.Hour)); n != 2 {
		t.Fatalf("stored grabs = %d", n)
	}
	f.Say("AnnounceBot", "#announce", line("Film.0.1080p.BluRay.x264-GRP", 0))
	waitFor(t, "the repeat to be logged", func() bool {
		for _, e := range m2.Log(conn.ID) {
			if e.Decision == DecisionDuplicate {
				return true
			}
		}
		return false
	})
}

func TestManagerTestBox(t *testing.T) {
	repo := newTestRepo(t)
	d := &fakeDecider{reply: func(a Announce, o DecideOptions) Decision {
		if !o.Dry {
			t.Error("the test box asked for a real grab")
		}
		return Decision{Action: DecisionWouldGrab, Reason: "ok"}
	}}
	m := NewManager(repo, d, nil)
	p, _ := PresetByID("multi-line")
	c := Connection{Template: p.Template, TrackerURL: "https://tracker.example", MaxPerHour: 5, MaxPerDay: 20}

	res := m.Test(context.Background(), c, p.Examples[:2])
	if res.Matched || !strings.Contains(res.Message, "every line") {
		t.Fatalf("half an announcement: %+v", res)
	}
	res = m.Test(context.Background(), c, p.Examples)
	if !res.Matched || res.Decision != DecisionWouldGrab || res.TorrentID != "9116" {
		t.Fatalf("whole announcement: %+v", res)
	}
	if strings.Contains(res.DownloadURL, "authkey=") && !strings.Contains(res.DownloadURL, "<authkey>") {
		t.Fatalf("download address shows a key: %s", res.DownloadURL)
	}

	// A broken template is explained, not run.
	c.Template.Patterns = []string{"("}
	res = m.Test(context.Background(), c, []string{"x"})
	if res.Matched || !strings.Contains(res.Message, "not a valid expression") {
		t.Fatalf("broken template: %+v", res)
	}
	// A tracker address that can't build the download address says so, and nothing would be grabbed.
	sp, _ := PresetByID("one-line-details-link")
	c = Connection{Template: sp.Template, TrackerURL: ""}
	res = m.Test(context.Background(), c, []string{sp.Examples[0]})
	if !res.Matched || res.URLProblem == "" || res.Decision != DecisionSkipped {
		t.Fatalf("no tracker address: %+v", res)
	}
}
