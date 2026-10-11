package announce

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Decider is the part that knows the library. It is given each announced
// release and decides whether it is wanted and, unless the call is a dry run,
// grabs it.
type Decider interface {
	Decide(ctx context.Context, c Connection, a Announce, o DecideOptions) Decision
}

// DecideOptions are the conditions of one decision.
type DecideOptions struct {
	// Dry says to decide without grabbing anything (the test box).
	Dry bool
	// Allow is asked right before a grab and may refuse it (a limit has been
	// reached). The reason is shown in the log. Nil allows.
	Allow func() (ok bool, reason string)
	// URL builds the real download address, with the tracker's keys in it.
	// The decider calls it only when it is about to grab, and never logs it.
	URL func() (string, error)
}

// Decision is what became of one release.
type Decision struct {
	Action string // one of the Decision* constants
	Reason string // in words, for the log
	Item   string // the movie or show it was for, when it was wanted
}

// Manager keeps one client running for every enabled connection and feeds
// what they hear to the Decider.
type Manager struct {
	repo    *Repo
	decider Decider
	log     *slog.Logger

	// Tweak changes the client settings of every connection (tests).
	Tweak func(*Config)
	now   func() time.Time

	reloadMu sync.Mutex // one Reload at a time
	mu       sync.Mutex
	runs     map[int64]*run
	failed   map[int64]string // connections that could not start, with why
	ctx      context.Context
	stop     context.CancelFunc
	wg       sync.WaitGroup
}

// NewManager makes a manager. Nothing runs until Start.
func NewManager(repo *Repo, d Decider, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{repo: repo, decider: d, log: logger, now: time.Now, runs: map[int64]*run{}, failed: map[int64]string{}}
}

const (
	ringSize      = 50
	queueSize     = 256
	seenKeep      = 48 * time.Hour
	seenMax       = 5000
	linesPerSec   = 20
	linesBurst    = 60
	workerTimeout = 2 * time.Minute
)

type run struct {
	conn     Connection
	client   *Client
	reader   *Reader
	compiled *Compiled
	queue    chan Message
	cancel   context.CancelFunc
	done     chan struct{}
	limiter  *rate.Limiter

	mu       sync.Mutex
	ring     []LogEntry // newest first
	seen     map[string]time.Time
	heard    int
	grabbed  int
	dropped  int
	lastDrop time.Time
}

// Start loads the saved connections and runs the enabled ones. It returns
// quickly; the connections run until Stop.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	m.ctx, m.stop = context.WithCancel(ctx)
	m.mu.Unlock()
	return m.Reload()
}

// Stop ends every connection and waits for them.
func (m *Manager) Stop() {
	m.mu.Lock()
	stop := m.stop
	m.mu.Unlock()
	if stop != nil {
		stop()
	}
	m.wg.Wait()
}

// Reload reads the saved connections and starts, restarts or stops runs to
// match them. Call it after any change.
func (m *Manager) Reload() error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	conns, err := m.repo.List()
	if err != nil {
		return fmt.Errorf("load irc connections: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil || m.ctx.Err() != nil {
		return nil
	}
	want := map[int64]Connection{}
	for _, c := range conns {
		if c.Enabled {
			want[c.ID] = c
		}
	}
	for id, r := range m.runs {
		c, ok := want[id]
		if !ok || !reflect.DeepEqual(c, r.conn) {
			m.stopRun(id, r)
		}
	}
	for id := range m.failed {
		if _, ok := want[id]; !ok {
			delete(m.failed, id)
		}
	}
	for id, c := range want {
		if _, running := m.runs[id]; running {
			continue
		}
		delete(m.failed, id)
		if err := m.startRun(c); err != nil {
			m.failed[id] = err.Error()
			m.log.Warn("irc announce: connection not started", "connection", c.Name, "error", err)
		}
	}
	return nil
}

// stopRun ends a run and waits for it. Call with m.mu held.
func (m *Manager) stopRun(id int64, r *run) {
	r.cancel()
	delete(m.runs, id)
	m.mu.Unlock()
	<-r.done
	m.mu.Lock()
}

// clientConfig builds the client settings of a connection.
func clientConfig(c Connection) Config {
	return Config{
		Server: c.Server, Port: c.Port, TLS: c.TLS, InsecureTLS: c.InsecureTLS,
		Nick: c.Nick, Auth: c.Auth, Account: c.AuthAccount, Password: c.AuthPass,
		Invite: c.Invite, Channels: c.Channels, Announcers: c.Announcers,
	}
}

func (m *Manager) startRun(c Connection) error {
	compiled, err := Compile(c.Template)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(m.ctx)
	r := &run{
		conn: c, compiled: compiled, reader: NewReader(compiled), queue: make(chan Message, queueSize),
		cancel: cancel, done: make(chan struct{}), seen: map[string]time.Time{},
		limiter: rate.NewLimiter(linesPerSec, linesBurst),
	}
	cfg := clientConfig(c)
	cfg.OnMessage = func(msg Message) { m.enqueue(r, msg) }
	if m.Tweak != nil {
		m.Tweak(&cfg)
	}
	r.client = NewClient(cfg)
	m.seed(r)
	m.runs[c.ID] = r
	m.wg.Add(2)
	go func() {
		defer m.wg.Done()
		r.client.Run(ctx)
	}()
	go func() {
		defer m.wg.Done()
		defer close(r.done)
		m.work(ctx, r)
	}()
	return nil
}

// seed fills the log and the duplicate check from what earlier runs stored.
func (m *Manager) seed(r *run) {
	if rows, err := m.repo.RecentLog(r.conn.ID, ringSize); err == nil {
		r.ring = rows
	}
	if names, err := m.repo.GrabbedReleases(r.conn.ID, m.now().Add(-seenKeep)); err == nil {
		for _, n := range names {
			r.seen[strings.ToLower(n)] = m.now()
		}
	}
}

// enqueue hands a message to the worker without ever blocking the network
// reader. A flood is dropped, and said so once a second at most.
func (m *Manager) enqueue(r *run, msg Message) {
	r.mu.Lock()
	r.heard++
	r.mu.Unlock()
	if !r.limiter.Allow() {
		m.noteDrop(r, msg, "too many announcements at once")
		return
	}
	select {
	case r.queue <- msg:
	default:
		m.noteDrop(r, msg, "the queue is full")
	}
}

func (m *Manager) noteDrop(r *run, msg Message, why string) {
	r.mu.Lock()
	r.dropped++
	say := m.now().Sub(r.lastDrop) > time.Second
	if say {
		r.lastDrop = m.now()
	}
	r.mu.Unlock()
	if say {
		m.record(r, LogEntry{Channel: msg.Channel, Announcer: msg.Nick, Release: truncate(msg.Text, 120), Decision: DecisionIgnored, Reason: "Skipped: " + why + "."}, false)
	}
}

func (m *Manager) work(ctx context.Context, r *run) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-r.queue:
			m.handle(ctx, r, msg)
		}
	}
}

// handle reads one announcement line and acts on it.
func (m *Manager) handle(ctx context.Context, r *run, msg Message) {
	fields, ok := r.reader.Feed(msg.Channel, msg.Nick, msg.Text, m.now())
	if !ok {
		if !r.compiled.T.Multi {
			m.record(r, LogEntry{Channel: msg.Channel, Announcer: msg.Nick, Release: truncate(msg.Text, 120), Decision: DecisionIgnored, Reason: "The line doesn't match the template."}, false)
		}
		return
	}
	a, err := r.compiled.Build(fields)
	e := LogEntry{Channel: msg.Channel, Announcer: msg.Nick, Release: a.Release, Category: a.Category, SizeBytes: a.SizeBytes}
	if err != nil {
		e.Decision, e.Reason = DecisionIgnored, capitalize(err.Error())+"."
		m.record(r, e, false)
		return
	}
	key := strings.ToLower(a.Release)
	if m.alreadySeen(r, key) {
		e.Decision, e.Reason = DecisionDuplicate, "Already handled this release."
		m.record(r, e, false)
		return
	}
	if r.conn.FreeleechOnly && !a.Freeleech {
		e.Decision, e.Reason = DecisionSkipped, "Not freeleech."
		m.record(r, e, false)
		return
	}
	dctx, cancel := context.WithTimeout(ctx, workerTimeout)
	d := m.decider.Decide(dctx, r.conn, a, DecideOptions{
		Allow: func() (bool, string) { return m.withinLimits(r, msg.Nick) },
		URL: func() (string, error) {
			return r.compiled.DownloadURL(a, URLOptions{TrackerURL: r.conn.TrackerURL, Keys: r.conn.Keys})
		},
	})
	cancel()
	e.Decision, e.Reason, e.Item = d.Action, d.Reason, d.Item
	if e.Decision == DecisionGrabbed || e.Decision == DecisionFailed {
		m.markSeen(r, key)
	}
	m.record(r, e, e.Decision == DecisionGrabbed || e.Decision == DecisionFailed)
}

func (m *Manager) alreadySeen(r *run, key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.seen[key]
	return ok && m.now().Sub(t) < seenKeep
}

func (m *Manager) markSeen(r *run, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.seen) >= seenMax {
		for k, t := range r.seen {
			if m.now().Sub(t) >= seenKeep {
				delete(r.seen, k)
			}
		}
		if len(r.seen) >= seenMax {
			r.seen = map[string]time.Time{}
		}
	}
	r.seen[key] = m.now()
}

// withinLimits checks the per-connection hourly cap and the per-announcer
// daily cap, counted from the stored grabs so a restart does not reset them.
func (m *Manager) withinLimits(r *run, announcer string) (bool, string) {
	c := r.conn
	if c.ID == 0 {
		return true, ""
	}
	if c.MaxPerHour > 0 {
		n, err := m.repo.CountGrabs(c.ID, "", m.now().Add(-time.Hour))
		if err != nil {
			return false, "Couldn't check the hourly limit."
		}
		if n >= c.MaxPerHour {
			return false, fmt.Sprintf("Held back: the limit of %d an hour is reached.", c.MaxPerHour)
		}
	}
	if c.MaxPerDay > 0 {
		n, err := m.repo.CountGrabs(c.ID, announcer, m.now().Add(-24*time.Hour))
		if err != nil {
			return false, "Couldn't check the daily limit."
		}
		if n >= c.MaxPerDay {
			return false, fmt.Sprintf("Held back: %s has reached the limit of %d a day.", announcer, c.MaxPerDay)
		}
	}
	return true, ""
}

// record adds an entry to the log in memory and, when persist is set, to the
// database.
func (m *Manager) record(r *run, e LogEntry, persist bool) {
	e.ConnectionID = r.conn.ID
	e.At = m.now().UTC()
	r.mu.Lock()
	r.ring = append([]LogEntry{e}, r.ring...)
	if len(r.ring) > ringSize {
		r.ring = r.ring[:ringSize]
	}
	if e.Decision == DecisionGrabbed {
		r.grabbed++
	}
	r.mu.Unlock()
	if persist {
		if err := m.repo.AddLog(e); err != nil {
			m.log.Warn("irc announce: could not store the log entry", "connection", r.conn.Name, "error", err)
		}
	}
	if e.Decision == DecisionGrabbed || e.Decision == DecisionFailed {
		m.log.Info("irc announce", "connection", r.conn.Name, "decision", e.Decision, "release", e.Release, "item", e.Item, "reason", e.Reason)
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Info is the state of one connection for the settings page.
type Info struct {
	Status
	Heard   int    `json:"heard"`   // announcements heard since it started
	Grabbed int    `json:"grabbed"` // releases grabbed since it started
	Dropped int    `json:"dropped"` // lines dropped because they came too fast
	Problem string `json:"problem"` // why it could not start
}

// InfoFor reports on a connection.
func (m *Manager) InfoFor(id int64) Info {
	m.mu.Lock()
	r := m.runs[id]
	problem := m.failed[id]
	m.mu.Unlock()
	if r == nil {
		return Info{Status: Status{State: "stopped", Channels: []ChannelStatus{}}, Problem: problem}
	}
	r.mu.Lock()
	heard, grabbed, dropped := r.heard, r.grabbed, r.dropped
	r.mu.Unlock()
	return Info{Status: r.client.Status(), Heard: heard, Grabbed: grabbed, Dropped: dropped}
}

// Log returns the last announcements of a connection, newest first. For a
// connection that is not running it is what was stored.
func (m *Manager) Log(id int64) []LogEntry {
	m.mu.Lock()
	r := m.runs[id]
	m.mu.Unlock()
	if r == nil {
		rows, _ := m.repo.RecentLog(id, ringSize)
		return rows
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]LogEntry(nil), r.ring...)
}

// TestResult is what the test box shows.
type TestResult struct {
	Matched     bool              `json:"matched"`
	Message     string            `json:"message"` // why not, when it did not match
	Fields      map[string]string `json:"fields"`
	Release     string            `json:"release"`
	SizeBytes   int64             `json:"sizeBytes"`
	Category    string            `json:"category"`
	Uploader    string            `json:"uploader"`
	TorrentID   string            `json:"torrentId"`
	Freeleech   bool              `json:"freeleech"`
	DownloadURL string            `json:"downloadUrl"` // keys shown as <name>, never real
	URLProblem  string            `json:"urlProblem"`
	Decision    string            `json:"decision"`
	Reason      string            `json:"reason"`
	Item        string            `json:"item"`
}

// Test reads pasted announcement lines with a connection's template and says
// what would happen, without grabbing anything and without touching the log.
// Lines are fed in order, so a multi-line announcement can be pasted whole.
func (m *Manager) Test(ctx context.Context, c Connection, lines []string) TestResult {
	res := TestResult{Fields: map[string]string{}}
	compiled, err := Compile(c.Template)
	if err != nil {
		res.Message = capitalize(strings.TrimPrefix(err.Error(), ErrTemplate.Error()+": ")) + "."
		return res
	}
	reader := NewReader(compiled)
	var fields Fields
	var got bool
	now := m.now()
	used := 0
	for _, l := range lines {
		l = Clean(l)
		if l == "" {
			continue
		}
		used++
		if used > 20 {
			break
		}
		if f, ok := reader.Feed("#test", "announcer", l, now); ok {
			fields, got = f, true
			break
		}
	}
	if !got {
		if compiled.T.Multi {
			res.Message = "These lines don't make up a whole announcement yet. Paste every line of one announcement, in order."
		} else {
			res.Message = "That line doesn't match the template."
		}
		return res
	}
	a, err := compiled.Build(fields)
	if err != nil {
		res.Message = capitalize(err.Error()) + "."
		return res
	}
	res.Matched = true
	res.Fields = map[string]string(fields)
	res.Release, res.SizeBytes, res.Category, res.Uploader, res.TorrentID, res.Freeleech = a.Release, a.SizeBytes, a.Category, a.Uploader, a.TorrentID, a.Freeleech
	if u, err := compiled.DownloadURL(a, URLOptions{TrackerURL: c.TrackerURL, Mask: true}); err != nil {
		res.URLProblem = capitalize(err.Error()) + "."
	} else {
		res.DownloadURL = u
	}
	if c.FreeleechOnly && !a.Freeleech {
		res.Decision, res.Reason = DecisionSkipped, "Not freeleech, and this connection only takes freeleech."
		return res
	}
	if res.URLProblem != "" {
		res.Decision, res.Reason = DecisionSkipped, "The download address can't be built, so nothing would be downloaded."
		return res
	}
	r := &run{conn: c}
	d := m.decider.Decide(ctx, c, a, DecideOptions{Dry: true, Allow: func() (bool, string) { return m.withinLimits(r, "") }})
	res.Decision, res.Reason, res.Item = d.Action, d.Reason, d.Item
	return res
}
