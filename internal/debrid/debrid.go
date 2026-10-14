// Package debrid sends torrents to a debrid service (Real-Debrid, TorBox) and
// brings the finished files home over HTTPS. The service downloads the
// torrent on its own servers; Mediarium then downloads plain files from it
// into the downloads folder, where the usual unpacking and import take over.
//
// Nothing is mounted and nothing is streamed: no FUSE, no rclone, no special
// container permissions. And no torrent traffic leaves this machine, so the
// VPN for torrents isn't involved.
package debrid

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/rdborg/mediarium/internal/netguard"
	"github.com/rdborg/mediarium/internal/speed"
)

// Services this package speaks to.
const (
	RealDebrid = "realdebrid"
	TorBox     = "torbox"
)

// Label is a service's name as people know it.
func Label(service string) string {
	switch service {
	case RealDebrid:
		return "Real-Debrid"
	case TorBox:
		return "TorBox"
	}
	return service
}

// Account is what the Test button shows.
type Account struct {
	Name    string    `json:"name"`
	Premium bool      `json:"premium"`
	Expires time.Time `json:"expires,omitzero"`
}

// State is where a torrent is on the service.
type State string

const (
	Waiting     State = "waiting"     // being checked or queued
	Downloading State = "downloading" // the service is downloading it
	Ready       State = "ready"       // done: the files can be fetched
	Failed      State = "failed"      // the service gave up (dead, virus, error)
)

// Status is a torrent on the service.
type Status struct {
	State    State
	Progress float64 // 0..1, the service's own download
	Message  string  // why it failed, in the service's words
	Seeders  int     // -1 when unknown
}

// File is one finished file and where to download it.
type File struct {
	Name string // relative path inside the torrent, as the service gives it
	Size int64
	URL  string
}

// Item is a torrent on the service as the Activity page lists it.
type Item struct {
	ID       string
	Name     string
	State    State
	Text     string  // the service's own state, in words
	Progress float64 // 0..1
	Size     int64
	Speed    int64 // bytes per second the service is downloading at
	Seeders  int   // -1 when unknown
	Added    time.Time
}

// Service is a debrid service.
type Service interface {
	Name() string
	Account(ctx context.Context) (Account, error)
	// Add hands over a magnet link or the bytes of a .torrent file.
	Add(ctx context.Context, magnet string, torrentFile []byte) (id string, err error)
	Status(ctx context.Context, id string) (Status, error)
	// Files lists the finished files with links good for a few hours.
	Files(ctx context.Context, id string) ([]File, error)
	Remove(ctx context.Context, id string) error
	// List lists the torrents on the account, newest first.
	List(ctx context.Context) ([]Item, error)
}

// CacheChecker is a service that can say which torrents it already has, so a
// release that is ready at once can be told from one that has to be downloaded
// first. Not every service can: Real-Debrid switched its availability check
// off in late 2024 and has no replacement, so only TorBox implements this.
type CacheChecker interface {
	// Cached answers for each info hash (40 hex characters, any case) whether
	// the service has the torrent. The map is keyed by the lower-case hash and
	// holds every hash that was asked about.
	Cached(ctx context.Context, hashes []string) (map[string]bool, error)
}

// CanCheckCache reports whether s can tell which torrents are already cached.
func CanCheckCache(s Service) bool {
	_, ok := s.(CacheChecker)
	return ok
}

// New returns the client for a service.
func New(service, apiKey string, hc *http.Client) (Service, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("add the API key of your debrid service first")
	}
	switch service {
	case RealDebrid:
		return &realDebrid{key: apiKey, http: hc, base: RealDebridURL}, nil
	case TorBox:
		return &torBox{key: apiKey, http: hc, base: TorBoxURL}, nil
	}
	return nil, fmt.Errorf("unknown debrid service %q", service)
}

// WithBaseURL points a service at another address (tests).
func WithBaseURL(s Service, base string) Service {
	switch v := s.(type) {
	case *realDebrid:
		v.base = base
	case *torBox:
		v.base = base
	}
	return s
}

// ErrAuth means the service refused the API key.
var ErrAuth = errors.New("the debrid service refused the API key")

// SafeName turns a path from the service into one that stays inside the
// download folder: no "..", no absolute paths, no control characters.
func SafeName(name string) string {
	var parts []string
	for _, p := range strings.FieldsFunc(strings.ReplaceAll(name, `\`, "/"), func(r rune) bool { return r == '/' }) {
		p = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, p)
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." {
			continue
		}
		if len(p) > 200 {
			p = p[:200]
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "download"
	}
	return filepath.Join(parts...)
}

// Fetch downloads one file into dir under its safe name, carrying on from a
// part already there. progress gets the bytes written so far.
func Fetch(ctx context.Context, hc *http.Client, f File, dir string, progress func(int64)) error {
	dest := filepath.Join(dir, SafeName(f.Name))
	if rel, err := filepath.Rel(dir, dest); err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("refusing a file name that leaves the download folder: %q", f.Name)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create folder: %w", err)
	}
	part := dest + ".part"
	var have int64
	if st, err := os.Stat(dest); err == nil && f.Size > 0 && st.Size() == f.Size {
		progress(f.Size)
		return nil // already complete from an earlier try
	}
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return fmt.Errorf("bad download link: %w", err)
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", filepath.Base(dest), netguard.CleanError(err))
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		have = 0
		flags |= os.O_TRUNC
	case http.StatusRequestedRangeNotSatisfiable:
		if f.Size > 0 && have == f.Size {
			return os.Rename(part, dest)
		}
		have = 0
		_ = os.Remove(part)
		return fmt.Errorf("download %s: the server refused to carry on; it will start again on the next try", filepath.Base(dest))
	default:
		return fmt.Errorf("download %s: the server answered HTTP %d", filepath.Base(dest), resp.StatusCode)
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(dest), err)
	}
	w := &countingWriter{w: out, n: have, report: progress}
	var body io.Reader = resp.Body
	if f.Size > 0 {
		// One byte more than the file's size is enough to tell that the
		// service sends too much, without letting it fill the disk.
		body = io.LimitReader(resp.Body, f.Size-have+1)
	}
	// The download speed limit and its schedule cover these files too.
	_, copyErr := io.Copy(w, &limitedReader{ctx: ctx, r: body})
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("download %s: %w", filepath.Base(dest), copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(dest), closeErr)
	}
	if f.Size > 0 && w.n > f.Size {
		_ = os.Remove(part)
		return fmt.Errorf("download %s: the service sent more than the file's size of %d bytes", filepath.Base(dest), f.Size)
	}
	if f.Size > 0 && w.n != f.Size {
		return fmt.Errorf("download %s: got %d of %d bytes", filepath.Base(dest), w.n, f.Size)
	}
	return os.Rename(part, dest)
}

// limitedReader holds each read back to the shared download speed limit.
type limitedReader struct {
	ctx context.Context
	r   io.Reader
}

func (l *limitedReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	if n > 0 {
		if werr := speed.Wait(l.ctx, n); werr != nil && err == nil {
			err = werr
		}
	}
	return n, err
}

type countingWriter struct {
	w      io.Writer
	n      int64
	report func(int64)
	last   time.Time
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.report != nil && time.Since(c.last) > 500*time.Millisecond {
		c.last = time.Now()
		c.report(c.n)
	}
	return n, err
}
