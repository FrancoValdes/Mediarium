package torrentclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
)

// FileState is one file of a torrent, for the list a person can tick.
type FileState struct {
	Index  int    `json:"index"`
	Path   string `json:"path"` // inside the torrent, with forward slashes
	Size   int64  `json:"size"`
	Done   int64  `json:"done"`
	Wanted bool   `json:"wanted"`
}

// controlTick is how often a running Control looks at its torrent.
const controlTick = 250 * time.Millisecond

// limitedTick is how often it looks while a speed limit is set.
const limitedTick = 40 * time.Millisecond

// ErrNoFileWanted is returned when a file selection would leave nothing to
// download.
var ErrNoFileWanted = errors.New("at least one file has to stay selected")

// Control steers one running torrent: which of its files are downloaded, in
// what order the pieces come, and how fast it may go. The engine's own
// defaults apply until something is set, so a torrent nobody touched behaves as
// it always did. Its methods are safe to call from several goroutines.
type Control struct {
	t *torrent.Torrent

	mu         sync.Mutex
	skip       map[string]bool
	sequential bool
	downLimit  int64 // bytes per second; 0 = none
	upLimit    int64
	seqSet     map[int]torrent.PiecePriority // piece priorities sequential mode has set
	down, up   bucket
	downHeld   bool // the torrent is held back to keep to its download limit
	upHeld     bool
	complete   []bool

	// writeFailed is set when the disk refused data. The engine then stops
	// downloading and the limits must not start it again.
	writeFailed atomic.Bool
	kick        chan struct{}
}

// NewControl returns the control of t, whose metadata must be known (the
// Add... calls wait for it). Every file is wanted until SetSkipped says
// otherwise.
func NewControl(t *torrent.Torrent) *Control {
	c := &Control{t: t, skip: map[string]bool{}, kick: make(chan struct{}, 1)}
	t.SetOnWriteChunkError(func(err error) {
		slog.Error("torrent: the disk refused data, downloading is stopped", "torrent", t.Name(), "err", err)
		c.writeFailed.Store(true)
		t.DisallowDataDownload()
	})
	return c
}

// Files lists the torrent's files with how much of each is downloaded.
func (c *Control) Files() []FileState {
	files := c.t.Files()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]FileState, len(files))
	for i, f := range files {
		out[i] = FileState{Index: i, Path: f.Path(), Size: f.Length(), Done: f.BytesCompleted(), Wanted: !c.skip[f.Path()]}
	}
	return out
}

// Skipped lists the files that are not downloaded.
func (c *Control) Skipped() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, f := range c.t.Files() {
		if c.skip[f.Path()] {
			out = append(out, f.Path())
		}
	}
	return out
}

// SetSkipped sets which files are left out; every other file is downloaded.
// Paths that are not in the torrent are ignored. Leaving out every file is
// refused.
func (c *Control) SetSkipped(paths []string) error {
	known := map[string]bool{}
	for _, f := range c.t.Files() {
		known[f.Path()] = true
	}
	next := map[string]bool{}
	for _, p := range paths {
		if known[p] {
			next[p] = true
		}
	}
	if len(next) >= len(known) && len(known) > 0 {
		return ErrNoFileWanted
	}
	c.mu.Lock()
	c.skip = next
	c.applyFilesLocked()
	c.mu.Unlock()
	c.wake()
	return nil
}

// applyFilesLocked gives wanted files normal priority and the others none,
// which is how the engine decides what to request.
func (c *Control) applyFilesLocked() {
	for _, f := range c.t.Files() {
		want := torrent.PiecePriorityNormal
		if c.skip[f.Path()] {
			want = torrent.PiecePriorityNone
		}
		if f.Priority() != want {
			f.SetPriority(want)
		}
	}
}

// Apply makes the engine follow the file selection. The download calls it
// before it starts.
func (c *Control) Apply() {
	c.mu.Lock()
	c.applyFilesLocked()
	c.mu.Unlock()
}

// SetSequential turns downloading in order on or off.
func (c *Control) SetSequential(on bool) {
	c.mu.Lock()
	c.sequential = on
	c.mu.Unlock()
	c.wake()
}

// Sequential reports whether the torrent downloads in order.
func (c *Control) Sequential() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sequential
}

// SetLimits sets this torrent's own speed limits in bytes per second, 0 for
// none. The engine's global limits still apply on top.
func (c *Control) SetLimits(down, up int64) {
	c.mu.Lock()
	down, up = max(down, 0), max(up, 0)
	if (down > 0 && c.downLimit == 0) || (up > 0 && c.upLimit == 0) {
		// A limit starts counting from now.
		st := c.t.Stats()
		if down > 0 && c.downLimit == 0 {
			c.down = bucket{prev: st.BytesReadData.Int64(), seen: true}
		}
		if up > 0 && c.upLimit == 0 {
			c.up = bucket{prev: st.BytesWrittenData.Int64(), seen: true}
		}
	}
	c.downLimit, c.upLimit = down, up
	c.mu.Unlock()
	c.wake()
}

// Limits returns the limits set with SetLimits.
func (c *Control) Limits() (down, up int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.downLimit, c.upLimit
}

func (c *Control) wake() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Progress is how much of the wanted files is downloaded.
func (c *Control) Progress() (done, total int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.t.Files() {
		if c.skip[f.Path()] {
			continue
		}
		total += f.Length()
		done += f.BytesCompleted()
	}
	return done, total
}

// WantedBytes is the size of the files that are downloaded.
func (c *Control) WantedBytes() int64 {
	_, total := c.Progress()
	return total
}

// Done reports whether every piece of every wanted file is here.
func (c *Control) Done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshCompleteLocked()
	for _, f := range c.t.Files() {
		if c.skip[f.Path()] || f.Length() == 0 {
			continue
		}
		for i := f.BeginPieceIndex(); i < f.EndPieceIndex(); i++ {
			if !c.complete[i] {
				return false
			}
		}
	}
	return true
}

// refreshCompleteLocked reads which pieces are verified into c.complete.
func (c *Control) refreshCompleteLocked() {
	n := c.t.NumPieces()
	if len(c.complete) != n {
		c.complete = make([]bool, n)
	}
	pos := 0
	for _, run := range c.t.PieceStateRuns() {
		ok := run.Ok && run.Complete
		for i := 0; i < run.Length && pos+i < n; i++ {
			c.complete[pos+i] = ok
		}
		pos += run.Length
	}
}

// Download blocks until every wanted file is downloaded and reports progress
// as it goes. It gives up with ErrStalled after stallAfter without a new byte
// (zero: never). It does not stop seeding afterward.
func (c *Control) Download(ctx context.Context, stallAfter time.Duration, onProgress ProgressFunc) error {
	c.Apply()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	done, total := c.Progress()
	last, lastMoved := done, time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			done, total = c.Progress()
			if c.Done() {
				if onProgress != nil {
					onProgress(total, total)
				}
				return nil
			}
			if onProgress != nil {
				onProgress(done, total)
			}
			if done > last {
				last, lastMoved = done, time.Now()
			} else if stallAfter > 0 && time.Since(lastMoved) >= stallAfter {
				return ErrStalled
			}
		}
	}
}

// Run keeps the torrent to what the control was told (reading order, speed
// limits) until ctx ends or the torrent is closed.
func (c *Control) Run(ctx context.Context) {
	interval := controlTick
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer c.release()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.t.Closed():
			return
		case <-ticker.C:
		case <-c.kick:
		}
		now := time.Now()
		want := c.step(now.Sub(last))
		last = now
		if want != interval {
			interval = want
			ticker.Reset(interval)
		}
	}
}

// release lets a torrent that was held back for its limits go again.
func (c *Control) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.holdDownloadLocked(false)
	c.holdUploadLocked(false)
}

// step does one round of the work and says how long to wait before the next:
// a torrent with a speed limit is looked at often, so it does not run far past
// its limit between two looks.
func (c *Control) step(dt time.Duration) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stepOrderLocked()
	if c.downLimit == 0 && c.upLimit == 0 && !c.downHeld && !c.upHeld {
		return controlTick
	}
	c.stepLimitsLocked(dt)
	return limitedTick
}

func (c *Control) stepLimitsLocked(dt time.Duration) {
	st := c.t.Stats()
	c.holdDownloadLocked(c.down.update(c.downLimit, st.BytesReadData.Int64(), dt))
	c.holdUploadLocked(c.up.update(c.upLimit, st.BytesWrittenData.Int64(), dt))
}

func (c *Control) holdDownloadLocked(hold bool) {
	if hold == c.downHeld || c.writeFailed.Load() {
		return
	}
	c.downHeld = hold
	if hold {
		c.t.DisallowDataDownload()
	} else {
		c.t.AllowDataDownload()
	}
}

func (c *Control) holdUploadLocked(hold bool) {
	if hold == c.upHeld {
		return
	}
	c.upHeld = hold
	if hold {
		c.t.DisallowDataUpload()
	} else {
		c.t.AllowDataUpload()
	}
}

// bucket keeps a torrent to a speed limit on average. The engine only has
// limits for the whole client, so a torrent with its own limit is held back
// (no new requests, no uploads) whenever it has taken more than its share, and
// let go again when the share has caught up.
type bucket struct {
	tokens float64 // bytes it may still move; negative = it moved too much
	prev   int64
	seen   bool
}

// burstSeconds is how much of the limit can be saved up while idle.
const burstSeconds = 0.5

// update takes the byte counter of the last dt and reports whether the
// torrent must be held back now.
func (b *bucket) update(limit, counter int64, dt time.Duration) bool {
	used := counter - b.prev
	if !b.seen {
		used, b.seen = 0, true
	}
	b.prev = counter
	if limit <= 0 {
		b.tokens = 0
		return false
	}
	b.tokens += float64(limit)*dt.Seconds() - float64(used)
	if top := float64(limit) * burstSeconds; b.tokens > top {
		b.tokens = top
	}
	return b.tokens < 0
}

// sequentialWindowBytes is how far ahead of the first missing piece a
// sequential download asks for pieces with raised priority.
const sequentialWindowBytes = 32 << 20

type pieceSpan struct{ begin, end int } // pieces [begin, end) of one wanted file

// sequentialPlan says which pieces a download in order asks for first. The
// first missing pieces get the most urgent priorities, the rest of a window
// ahead a lower one, and the first and last piece of every file one above the
// normal (a video often keeps its index at the end). Pieces not in the result
// keep the priority their file gives them.
func sequentialPlan(spans []pieceSpan, complete []bool, window int) map[int]torrent.PiecePriority {
	plan := map[int]torrent.PiecePriority{}
	raise := func(piece int, p torrent.PiecePriority) {
		if piece >= 0 && piece < len(complete) && !complete[piece] && plan[piece] < p {
			plan[piece] = p
		}
	}
	k := 0
	last := -1
	for _, s := range spans {
		for i := s.begin; i < s.end && k < window; i++ {
			if complete[i] || i <= last {
				continue
			}
			last = i
			switch {
			case k < 2:
				raise(i, torrent.PiecePriorityNow)
			case k < 6:
				raise(i, torrent.PiecePriorityNext)
			default:
				raise(i, torrent.PiecePriorityReadahead)
			}
			k++
		}
	}
	current := true
	for _, s := range spans {
		if s.end <= s.begin {
			continue
		}
		missing := false
		for i := s.begin; i < s.end; i++ {
			if !complete[i] {
				missing = true
				break
			}
		}
		if !missing {
			continue
		}
		if current {
			// The file being read: its end is needed early too.
			raise(s.end-1, torrent.PiecePriorityNext)
			current = false
			continue
		}
		raise(s.begin, torrent.PiecePriorityHigh)
		raise(s.end-1, torrent.PiecePriorityHigh)
	}
	return plan
}

// stepOrderLocked applies sequential mode: it sets the piece priorities of the
// plan and clears the ones an earlier plan set that are not in this one.
func (c *Control) stepOrderLocked() {
	if !c.sequential {
		if len(c.seqSet) > 0 {
			for i := range c.seqSet {
				c.t.Piece(i).SetPriority(torrent.PiecePriorityNone)
			}
			c.seqSet = nil
		}
		return
	}
	c.refreshCompleteLocked()
	var spans []pieceSpan
	for _, f := range c.t.Files() {
		if c.skip[f.Path()] || f.Length() == 0 {
			continue
		}
		spans = append(spans, pieceSpan{f.BeginPieceIndex(), f.EndPieceIndex()})
	}
	window := 8
	if pl := c.t.Info().PieceLength; pl > 0 {
		window = max(int(sequentialWindowBytes/pl), 8)
	}
	plan := sequentialPlan(spans, c.complete, window)
	for i := range c.seqSet {
		if _, keep := plan[i]; !keep {
			c.t.Piece(i).SetPriority(torrent.PiecePriorityNone)
		}
	}
	for i, p := range plan {
		if c.seqSet[i] != p {
			c.t.Piece(i).SetPriority(p)
		}
	}
	c.seqSet = plan
}

// String describes the control for logs.
func (c *Control) String() string {
	d, u := c.Limits()
	return fmt.Sprintf("control(sequential=%v down=%d up=%d)", c.Sequential(), d, u)
}
