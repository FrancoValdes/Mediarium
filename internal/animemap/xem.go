package animemap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/netguard"
)

const (
	xemBase = "https://thexem.info"

	maxXEMResponse = 8 << 20
	// haveMapTTL is how long the list of shows that have a mapping is reused.
	// TheXEM serves it from a cache that lasts an hour; it changes a few times a
	// week at most.
	haveMapTTL = 7 * 24 * time.Hour
)

// ErrOffline means a service could not be reached or was busy. Callers keep
// what they had and try again later; it is not a problem to show.
var ErrOffline = errors.New("couldn't reach the service")

// gate spaces the requests to one service, so a batch of shows never hits it
// harder than a person clicking would.
type gate struct {
	mu   sync.Mutex
	last time.Time
	gap  time.Duration
}

func (g *gate) wait(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if d := time.Until(g.last.Add(g.gap)); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	g.last = time.Now()
	return nil
}

// XEM talks to TheXEM. It asks for a show's mapping only when the show is in
// the list of those that have one, spaces its requests a second apart and
// identifies itself.
type XEM struct {
	base      string
	userAgent string
	http      *http.Client
	gate      gate
	cacheDir  string

	mu      sync.Mutex
	have    map[int]bool
	haveAt  time.Time
	haveTTL time.Duration
}

// NewXEM returns a client. cacheDir ("" = none) keeps the list of mapped shows
// between runs.
func NewXEM(userAgent, cacheDir string) *XEM {
	return &XEM{
		base: xemBase, userAgent: userAgent, http: netguard.Client(30 * time.Second),
		gate: gate{gap: time.Second}, cacheDir: cacheDir, haveTTL: haveMapTTL,
	}
}

// SetBase points the client at a test server.
func (x *XEM) SetBase(base string) {
	x.base = strings.TrimRight(base, "/")
	x.gate.gap = 0
}

type xemAnswer struct {
	Result  string          `json:"result"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (x *XEM) get(ctx context.Context, path string, q url.Values) (xemAnswer, error) {
	var out xemAnswer
	if err := x.gate.wait(ctx); err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, x.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return out, netguard.CleanError(err)
	}
	req.Header.Set("User-Agent", x.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := x.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("%w: TheXEM: %v", ErrOffline, netguard.CleanError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 429, 5xx and Cloudflare's pages all come here: try again later.
		return out, fmt.Errorf("%w: TheXEM answered HTTP %d", ErrOffline, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxXEMResponse))
	if err != nil {
		return out, fmt.Errorf("%w: TheXEM: %v", ErrOffline, err)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("%w: TheXEM sent something that isn't its usual answer", ErrOffline)
	}
	return out, nil
}

// HaveMap reports whether TheXEM has a mapping for the show with this TheTVDB
// id. The list of such shows is read once a week.
func (x *XEM) HaveMap(ctx context.Context, tvdbID int) (bool, error) {
	x.mu.Lock()
	have, at := x.have, x.haveAt
	x.mu.Unlock()
	if have == nil || time.Since(at) > x.haveTTL {
		var err error
		if have, err = x.loadHaveMap(ctx); err != nil {
			return false, err
		}
	}
	return have[tvdbID], nil
}

func (x *XEM) loadHaveMap(ctx context.Context) (map[int]bool, error) {
	if m, at, ok := x.readHaveCache(); ok && time.Since(at) <= x.haveTTL {
		x.setHave(m, at)
		return m, nil
	}
	ans, err := x.get(ctx, "/map/havemap", url.Values{"origin": {"tvdb"}})
	if err != nil {
		// A week-old list is better than none.
		if m, at, ok := x.readHaveCache(); ok {
			x.setHave(m, at)
			return m, nil
		}
		return nil, err
	}
	ids, err := parseHaveMap(ans)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	x.setHave(ids, now)
	x.writeHaveCache(ids, now)
	return ids, nil
}

func (x *XEM) setHave(m map[int]bool, at time.Time) {
	x.mu.Lock()
	x.have, x.haveAt = m, at
	x.mu.Unlock()
}

// parseHaveMap reads {"result":"success","data":["70668","70900",...]}.
func parseHaveMap(ans xemAnswer) (map[int]bool, error) {
	if ans.Result != "success" {
		return nil, fmt.Errorf("%w: TheXEM said %q", ErrOffline, ans.Message)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(ans.Data, &raw); err != nil {
		return nil, fmt.Errorf("%w: TheXEM's list of shows isn't a list", ErrOffline)
	}
	out := make(map[int]bool, len(raw))
	for _, r := range raw {
		s := strings.Trim(string(r), `"`)
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			out[n] = true
		}
	}
	return out, nil
}

func (x *XEM) cachePath() string {
	if x.cacheDir == "" {
		return ""
	}
	return filepath.Join(x.cacheDir, "xem-havemap.json")
}

func (x *XEM) readHaveCache() (map[int]bool, time.Time, bool) {
	p := x.cachePath()
	if p == "" {
		return nil, time.Time{}, false
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, time.Time{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, time.Time{}, false
	}
	var ans xemAnswer
	if json.Unmarshal(b, &ans) != nil {
		return nil, time.Time{}, false
	}
	m, err := parseHaveMap(ans)
	if err != nil {
		return nil, time.Time{}, false
	}
	return m, info.ModTime(), true
}

func (x *XEM) writeHaveCache(m map[int]bool, _ time.Time) {
	p := x.cachePath()
	if p == "" {
		return
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, strconv.Itoa(id))
	}
	b, err := json.Marshal(map[string]any{"result": "success", "data": ids})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, p)
	}
}

// All reads a show's mapping. found is false when TheXEM does not have the
// show; that is an answer, not an error.
func (x *XEM) All(ctx context.Context, tvdbID int) (rows []SceneEpisode, found bool, err error) {
	ans, err := x.get(ctx, "/map/all", url.Values{"id": {strconv.Itoa(tvdbID)}, "origin": {"tvdb"}})
	if err != nil {
		return nil, false, err
	}
	return parseAll(ans)
}

type xemNumbers struct {
	Season   int `json:"season"`
	Episode  int `json:"episode"`
	Absolute int `json:"absolute"`
}

type xemRow struct {
	Scene *xemNumbers `json:"scene"`
	TVDB  *xemNumbers `json:"tvdb"`
}

// parseAll reads {"result":"success","data":[{"scene":{...},"tvdb":{...}},...]}.
// A show TheXEM does not know comes back as {"result":"failure","data":[],
// "message":"no show with the tvdb_id 81797 found"}.
func parseAll(ans xemAnswer) ([]SceneEpisode, bool, error) {
	if ans.Result != "success" {
		if strings.Contains(strings.ToLower(ans.Message), "no show") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%w: TheXEM said %q", ErrOffline, ans.Message)
	}
	var data []xemRow
	if err := json.Unmarshal(ans.Data, &data); err != nil {
		return nil, false, fmt.Errorf("%w: TheXEM's mapping isn't a list", ErrOffline)
	}
	out := make([]SceneEpisode, 0, len(data))
	for _, r := range data {
		if r.Scene == nil || r.TVDB == nil || r.Scene.Season < 0 || r.TVDB.Season < 0 {
			continue
		}
		out = append(out, SceneEpisode{
			SceneSeason: r.Scene.Season, SceneEpisode: r.Scene.Episode, SceneAbsolute: r.Scene.Absolute,
			Season: r.TVDB.Season, Episode: r.TVDB.Episode, Absolute: r.TVDB.Absolute,
		})
	}
	return out, len(out) > 0, nil
}
