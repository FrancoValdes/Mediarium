package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/crypto"
	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/quality"
	"github.com/rdborg/mediarium/internal/settings"
	"github.com/rdborg/mediarium/internal/store"
)

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hashC = "cccccccccccccccccccccccccccccccccccccccc"
)

func TestReleaseHash(t *testing.T) {
	// 20 bytes of 0xAA in base32 is "VKVKVKVKVKVKVKVKVKVKVKVKVKVKVKVK".
	tests := []struct {
		name string
		res  indexers.Result
		want string
	}{
		{"torznab infohash", indexers.Result{Protocol: indexers.ProtocolTorrent, InfoHash: strings.ToUpper(hashA)}, hashA},
		{"magnet with hex hash", indexers.Result{Protocol: indexers.ProtocolTorrent, DownloadURL: "magnet:?xt=urn:btih:" + strings.ToUpper(hashB) + "&dn=x"}, hashB},
		{"magnet with base32 hash", indexers.Result{Protocol: indexers.ProtocolTorrent, DownloadURL: "magnet:?xt=urn:btih:VKVKVKVKVKVKVKVKVKVKVKVKVKVKVKVK"}, hashA},
		{"torrent file link with no hash", indexers.Result{Protocol: indexers.ProtocolTorrent, DownloadURL: "https://example.org/dl/1.torrent"}, ""},
		{"torrent flag instead of protocol", indexers.Result{Torrent: true, InfoHash: hashC}, hashC},
		{"usenet is never asked about", indexers.Result{Protocol: indexers.ProtocolUsenet, InfoHash: hashA}, ""},
		{"nonsense hash", indexers.Result{Protocol: indexers.ProtocolTorrent, InfoHash: "xyz"}, ""},
		{"magnet without a hash", indexers.Result{Protocol: indexers.ProtocolTorrent, DownloadURL: "magnet:?dn=x"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseHash(tc.res); got != tc.want {
				t.Errorf("releaseHash = %q, want %q", got, tc.want)
			}
		})
	}
}

// withPolicy sets "a cached release may score lower" for one test.
func withPolicy(t *testing.T, lowerScoreOK bool) {
	t.Helper()
	old := cachedOverScore.Load()
	cachedOverScore.Store(lowerScoreOK)
	t.Cleanup(func() { cachedOverScore.Store(old) })
}

func TestPickPrefersCached(t *testing.T) {
	preferred := quality.Presets()[quality.Preset1080p]
	preferred.Preferred = []quality.Preferred{{Term: "hdr", Score: 10}}
	plain := quality.Presets()[quality.Preset1080p]

	tor := func(url, title string, cached bool) indexers.Result {
		return indexers.Result{Title: title, DownloadURL: url, Protocol: indexers.ProtocolTorrent, Cached: cached, SizeBytes: 8 << 30}
	}
	use := func(url, title string) indexers.Result {
		return indexers.Result{Title: title, DownloadURL: url, Protocol: indexers.ProtocolUsenet, SizeBytes: 8 << 30}
	}
	tests := []struct {
		name         string
		profile      quality.Profile
		lowerScoreOK bool
		results      []indexers.Result
		want         string
	}{
		{
			name:    "equal quality: cached comes first, wherever it is in the list",
			profile: plain,
			results: []indexers.Result{tor("slow", "Movie.2024.1080p.WEB-DL.x264-AAA", false), tor("fast", "Movie.2024.1080p.WEB-DL.x264-BBB", true)},
			want:    "fast",
		},
		{
			name:    "equal quality: cached first in the list stays first",
			profile: plain,
			results: []indexers.Result{tor("fast", "Movie.2024.1080p.WEB-DL.x264-AAA", true), tor("slow", "Movie.2024.1080p.WEB-DL.x264-BBB", false)},
			want:    "fast",
		},
		{
			name:    "nothing cached: the order is what it always was",
			profile: plain,
			results: []indexers.Result{tor("one", "Movie.2024.1080p.WEB-DL.x264-AAA", false), tor("two", "Movie.2024.1080p.WEB-DL.x264-BBB", false)},
			want:    "one",
		},
		{
			name:    "a cached release of a worse quality never wins",
			profile: plain,
			results: []indexers.Result{tor("good", "Movie.2024.1080p.BluRay.x264-AAA", false), tor("fast", "Movie.2024.1080p.WEB-DL.x264-BBB", true)},
			want:    "good",
		},
		{
			name:         "a cached release of a worse quality never wins, even when a lower score is fine",
			profile:      plain,
			lowerScoreOK: true,
			results:      []indexers.Result{tor("good", "Movie.2024.1080p.BluRay.x264-AAA", false), tor("fast", "Movie.2024.1080p.WEB-DL.x264-BBB", true)},
			want:         "good",
		},
		{
			name:    "a better score beats cached while a lower score is not accepted",
			profile: preferred,
			results: []indexers.Result{tor("scored", "Movie.2024.1080p.WEB-DL.HDR.x264-AAA", false), tor("fast", "Movie.2024.1080p.WEB-DL.x264-BBB", true)},
			want:    "scored",
		},
		{
			name:         "cached beats a better score once a lower score is accepted",
			profile:      preferred,
			lowerScoreOK: true,
			results:      []indexers.Result{tor("scored", "Movie.2024.1080p.WEB-DL.HDR.x264-AAA", false), tor("fast", "Movie.2024.1080p.WEB-DL.x264-BBB", true)},
			want:         "fast",
		},
		{
			name:    "a better score still wins between two cached ones",
			profile: preferred,
			results: []indexers.Result{tor("plain", "Movie.2024.1080p.WEB-DL.x264-AAA", true), tor("scored", "Movie.2024.1080p.WEB-DL.HDR.x264-BBB", true)},
			want:    "scored",
		},
		{
			name:    "the preferred protocol is decided before cached",
			profile: plain,
			results: []indexers.Result{tor("fast", "Movie.2024.1080p.WEB-DL.x264-AAA", true), use("nzb", "Movie.2024.1080p.WEB-DL.x264-BBB")},
			want:    "nzb",
		},
		{
			name:    "an indexer set to preferred is decided before cached",
			profile: plain,
			results: []indexers.Result{
				{Title: "Movie.2024.1080p.WEB-DL.x264-AAA", DownloadURL: "fast", Protocol: indexers.ProtocolTorrent, Cached: true, Priority: indexers.PriorityNormal, SizeBytes: 8 << 30},
				{Title: "Movie.2024.1080p.WEB-DL.x264-BBB", DownloadURL: "fav", Protocol: indexers.ProtocolTorrent, Priority: indexers.PriorityPreferred, SizeBytes: 8 << 30},
			},
			want: "fav",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withPolicy(t, tc.lowerScoreOK)
			best := pickBestResult(tc.results, tc.profile, 2024)
			if best == nil || best.DownloadURL != tc.want {
				t.Fatalf("picked %+v, want %q", best, tc.want)
			}
		})
	}
}

func TestPickUpgradeAndTVPreferCached(t *testing.T) {
	profile := quality.Presets()[quality.Preset1080p]
	tor := func(url, title string, cached bool) indexers.Result {
		return indexers.Result{Title: title, DownloadURL: url, Protocol: indexers.ProtocolTorrent, Cached: cached, SizeBytes: 1 << 30}
	}
	withPolicy(t, false)

	up := pickUpgradeResult([]indexers.Result{
		tor("slow", "Movie.2024.1080p.WEB-DL.x264-AAA", false),
		tor("fast", "Movie.2024.1080p.WEB-DL.x264-BBB", true),
	}, profile, quality.TierWebDL720p, "Movie.2024.720p.WEB-DL.x264-OLD", 2024)
	if up == nil || up.DownloadURL != "fast" {
		t.Fatalf("upgrade picked %+v, want the cached release", up)
	}

	series := library.Series{Title: "The Show"}
	ep := pickTVResult([]indexers.Result{
		tor("slow", "The.Show.S01E02.1080p.WEB-DL.x264-AAA", false),
		tor("fast", "The.Show.S01E02.1080p.WEB-DL.x264-BBB", true),
		tor("worse", "The.Show.S01E02.1080p.HDTV.x264-CCC", true),
	}, series, 1, 2, profile, nil)
	if ep == nil || ep.DownloadURL != "fast" {
		t.Fatalf("episode picked %+v, want the cached 1080p release", ep)
	}
}

// fakeTorBoxCache answers GET /torrents/checkcached for the hashes in cached.
type fakeTorBoxCache struct {
	srv     *httptest.Server
	calls   atomic.Int32
	asked   [][]string
	mu      sync.Mutex
	failing atomic.Bool
	slow    atomic.Bool
}

func newFakeTorBoxCache(t *testing.T, cached ...string) *fakeTorBoxCache {
	t.Helper()
	f := &fakeTorBoxCache{}
	has := map[string]bool{}
	for _, h := range cached {
		has[h] = true
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/torrents/checkcached" {
			http.NotFound(w, r)
			return
		}
		f.calls.Add(1)
		if f.slow.Load() {
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
			return
		}
		if f.failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"success":false,"detail":"down"}`)
			return
		}
		asked := strings.Split(r.URL.Query().Get("hash"), ",")
		f.mu.Lock()
		f.asked = append(f.asked, asked)
		f.mu.Unlock()
		var items []string
		for _, h := range asked {
			if has[h] {
				items = append(items, fmt.Sprintf(`{"name":"x","size":1,"hash":%q}`, h))
			}
		}
		fmt.Fprint(w, `{"success":true,"detail":"Found.","data":[`+strings.Join(items, ",")+`]}`)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func cacheServer(t *testing.T, fake *fakeTorBoxCache, service string, extra map[string]string) *Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := crypto.LoadOrCreateKey(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	st := settings.New(db, box)
	if err := st.Set(settings.KeyDebridService, service, false); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(settings.KeyDebridAPIKey, "fake-key", true); err != nil {
		t.Fatal(err)
	}
	for k, v := range extra {
		if err := st.Set(k, v, false); err != nil {
			t.Fatal(err)
		}
	}
	old := debridTestURL
	debridTestURL = fake.srv.URL
	t.Cleanup(func() { debridTestURL = old })
	return &Server{Settings: st}
}

func torrents() []indexers.Result {
	return []indexers.Result{
		{Title: "Movie.2024.1080p.WEB-DL.x264-AAA", DownloadURL: "https://example.org/a.torrent", InfoHash: hashA, Protocol: indexers.ProtocolTorrent},
		{Title: "Movie.2024.1080p.WEB-DL.x264-BBB", DownloadURL: "magnet:?xt=urn:btih:" + hashB, Protocol: indexers.ProtocolTorrent},
		{Title: "Movie.2024.1080p.WEB-DL.x264-CCC", DownloadURL: "https://example.org/c.torrent", Protocol: indexers.ProtocolTorrent}, // no hash: unknown
		{Title: "Movie.2024.1080p.WEB-DL.x264-NZB", DownloadURL: "https://example.org/n.nzb", Protocol: indexers.ProtocolUsenet},
	}
}

func TestMarkCached(t *testing.T) {
	withPolicy(t, false)
	fake := newFakeTorBoxCache(t, hashB)
	s := cacheServer(t, fake, "torbox", nil) // prefer_cached unset: on for TorBox
	in := torrents()
	out := s.markCached(context.Background(), in)

	var got []bool
	for _, r := range out {
		got = append(got, r.Cached)
	}
	if fmt.Sprint(got) != "[false true false false]" {
		t.Fatalf("cached flags = %v", got)
	}
	for _, r := range in {
		if r.Cached {
			t.Fatal("the slice that was passed in must not change: it is shared between titles")
		}
	}
	if len(fake.asked) != 1 || fmt.Sprint(fake.asked[0]) != fmt.Sprint([]string{hashA, hashB}) {
		t.Fatalf("one question with the two known hashes expected, got %v", fake.asked)
	}

	// A second search a moment later is answered from memory.
	s.markCached(context.Background(), torrents())
	if fake.calls.Load() != 1 {
		t.Fatalf("asked the service %d times, want 1", fake.calls.Load())
	}

	// Picking uses it.
	best := pickBestResult(out[:3], quality.Presets()[quality.Preset1080p], 2024) // the torrents; the Usenet post wins on protocol
	if best == nil || !strings.Contains(best.Title, "BBB") {
		t.Fatalf("picked %+v, want the cached BBB release", best)
	}
}

func TestMarkCachedLeavesAlone(t *testing.T) {
	tests := []struct {
		name    string
		service string
		extra   map[string]string
		results func() []indexers.Result
	}{
		{"switched off", "torbox", map[string]string{settings.KeyDebridPreferCached: "0"}, torrents},
		{"Real-Debrid has no check", "realdebrid", nil, torrents},
		{"no debrid service", "", nil, torrents},
		{"one torrent has nothing to be ranked against", "torbox", nil, func() []indexers.Result { return torrents()[:1] }},
		{"only usenet", "torbox", nil, func() []indexers.Result { return torrents()[3:] }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeTorBoxCache(t, hashA, hashB)
			s := cacheServer(t, fake, tc.service, tc.extra)
			for _, r := range s.markCached(context.Background(), tc.results()) {
				if r.Cached {
					t.Errorf("%s marked cached", r.Title)
				}
			}
			if n := fake.calls.Load(); n != 0 {
				t.Errorf("asked the service %d times, want 0", n)
			}
		})
	}
}

func TestMarkCachedFailsQuietly(t *testing.T) {
	t.Run("an error means unknown, and the service is left alone for a while", func(t *testing.T) {
		fake := newFakeTorBoxCache(t, hashA)
		fake.failing.Store(true)
		s := cacheServer(t, fake, "torbox", nil)
		for i := 0; i < 3; i++ {
			for _, r := range s.markCached(context.Background(), torrents()) {
				if r.Cached {
					t.Fatalf("round %d: %s marked cached", i, r.Title)
				}
			}
		}
		if n := fake.calls.Load(); n != 1 {
			t.Fatalf("asked %d times, want 1 (then a pause)", n)
		}
		// When the pause is over it tries again.
		s.debridCache.mu.Lock()
		s.debridCache.pausedTil = time.Now().Add(-time.Second)
		s.debridCache.mu.Unlock()
		fake.failing.Store(false)
		out := s.markCached(context.Background(), torrents())
		if !out[0].Cached {
			t.Fatal("after the pause the answer should be used")
		}
	})
	t.Run("a service that doesn't answer in time means unknown", func(t *testing.T) {
		fake := newFakeTorBoxCache(t, hashA)
		fake.slow.Store(true)
		s := cacheServer(t, fake, "torbox", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		out := s.markCached(ctx, torrents())
		if time.Since(start) > 3*time.Second {
			t.Fatalf("the search waited %s", time.Since(start))
		}
		for _, r := range out {
			if r.Cached {
				t.Fatalf("%s marked cached", r.Title)
			}
		}
	})
}

func TestMarkCachedPayloads(t *testing.T) {
	fake := newFakeTorBoxCache(t, hashA)
	s := cacheServer(t, fake, "torbox", nil)
	rows := []searchResultPayload{{hash: hashA}, {hash: hashB}, {hash: ""}}
	s.markCachedPayloads(context.Background(), rows)
	if !rows[0].Cached || rows[1].Cached || rows[2].Cached {
		t.Fatalf("rows = %+v", rows)
	}
	if fake.calls.Load() != 1 {
		t.Fatalf("one question for the whole list, got %d", fake.calls.Load())
	}
	// A single row is still asked about: the list shows the tag.
	one := []searchResultPayload{{hash: hashC}}
	s.markCachedPayloads(context.Background(), one)
	if one[0].Cached || fake.calls.Load() != 2 {
		t.Fatalf("one = %+v, calls = %d", one, fake.calls.Load())
	}
}

func TestCachedMemoExpires(t *testing.T) {
	var m cachedMemo
	now := time.Now()
	m.put(map[string]bool{hashA: true, hashB: false}, now)
	known, missing, _ := m.get([]string{hashA, hashB, hashC}, now.Add(time.Minute))
	if !known[hashA] || known[hashB] || len(known) != 2 || len(missing) != 1 || missing[0] != hashC {
		t.Fatalf("known %v missing %v", known, missing)
	}
	known, missing, _ = m.get([]string{hashA}, now.Add(cachedTTL+time.Second))
	if len(known) != 0 || len(missing) != 1 {
		t.Fatalf("after the time is up: known %v missing %v", known, missing)
	}
}

func TestDebridCachedSettingsRoundTrip(t *testing.T) {
	withPolicy(t, false)
	fake := newFakeTorBoxCache(t)
	s := cacheServer(t, fake, "torbox", nil)
	call := func(handler http.HandlerFunc, method, body string) debridPayload {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/debrid", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", method, rec.Code, rec.Body.String())
		}
		var p debridPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	p := call(s.handleGetDebrid, http.MethodGet, "")
	if !p.CanCheck || !p.PreferCached || p.CachedLowerScore {
		t.Fatalf("defaults for TorBox: %+v", p)
	}
	// Saving without the two switches leaves them as they are.
	p = call(s.handlePutDebrid, http.MethodPut, `{"service":"torbox"}`)
	if !p.PreferCached || p.CachedLowerScore {
		t.Fatalf("after a plain save: %+v", p)
	}
	p = call(s.handlePutDebrid, http.MethodPut, `{"service":"torbox","preferCached":false,"cachedLowerScore":true}`)
	if p.PreferCached || !p.CachedLowerScore || !cachedOverScore.Load() {
		t.Fatalf("after switching: %+v, over score %v", p, cachedOverScore.Load())
	}
	if s.preferCachedOn() {
		t.Fatal("switched off, but the check is still on")
	}
	p = call(s.handlePutDebrid, http.MethodPut, `{"service":"torbox","preferCached":true,"cachedLowerScore":false}`)
	if !p.PreferCached || p.CachedLowerScore || cachedOverScore.Load() || !s.preferCachedOn() {
		t.Fatalf("after switching back: %+v", p)
	}
	p = call(s.handlePutDebrid, http.MethodPut, `{"service":"realdebrid","apiKey":"rd-key"}`)
	if p.CanCheck || s.preferCachedOn() {
		t.Fatalf("Real-Debrid can't check: %+v", p)
	}
}
