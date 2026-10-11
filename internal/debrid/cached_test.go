package debrid

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The fake replays TorBox's documented answer to GET /torrents/checkcached:
// {success, detail, data} where data lists the cached torrents as
// {name, size, hash} (format=list) or an object keyed by hash (format=object),
// and leaves the others out.
func TestTorBoxCached(t *testing.T) {
	const (
		a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		b = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		c = "cccccccccccccccccccccccccccccccccccccccc"
	)
	cachedByTorBox := map[string]bool{a: true, c: true}
	tests := []struct {
		name   string
		answer func(asked []string) string // the body for the hashes asked
		status int
		hashes []string
		want   map[string]bool
		err    string
	}{
		{
			name: "list format",
			answer: func(asked []string) string {
				var items []string
				for _, h := range asked {
					if cachedByTorBox[h] {
						items = append(items, fmt.Sprintf(`{"name":"Some.Movie.2020.1080p","size":1234,"hash":%q}`, h))
					}
				}
				return `{"success":true,"detail":"Found cached torrents.","data":[` + strings.Join(items, ",") + `]}`
			},
			hashes: []string{strings.ToUpper(a), b, c},
			want:   map[string]bool{a: true, b: false, c: true},
		},
		{
			name: "object format",
			answer: func(asked []string) string {
				var items []string
				for _, h := range asked {
					if cachedByTorBox[h] {
						items = append(items, fmt.Sprintf(`%q:{"name":"x","size":1,"hash":%q}`, h, h))
					}
				}
				return `{"success":true,"detail":"ok","data":{` + strings.Join(items, ",") + `}}`
			},
			hashes: []string{a, b},
			want:   map[string]bool{a: true, b: false},
		},
		{
			name:   "nothing cached comes as null",
			answer: func([]string) string { return `{"success":true,"detail":"No cached torrents found.","data":null}` },
			hashes: []string{a, b},
			want:   map[string]bool{a: false, b: false},
		},
		{
			name:   "nothing cached comes as an empty list",
			answer: func([]string) string { return `{"success":true,"detail":"","data":[]}` },
			hashes: []string{a},
			want:   map[string]bool{a: false},
		},
		{
			name:   "duplicates and blanks are asked once",
			answer: func(asked []string) string { return `{"success":true,"data":[{"hash":"` + a + `"}]}` },
			hashes: []string{a, a, "", " " + a + " "},
			want:   map[string]bool{a: true},
		},
		{
			name:   "refused key",
			answer: func([]string) string { return `{"success":false,"detail":"bad key"}` },
			status: http.StatusForbidden,
			hashes: []string{a},
			err:    ErrAuth.Error(),
		},
		{
			name:   "the service says no",
			answer: func([]string) string { return `{"success":false,"detail":"Slow down."}` },
			status: http.StatusTooManyRequests,
			hashes: []string{a},
			err:    "TorBox: Slow down.",
		},
		{
			name:   "an answer that isn't JSON data",
			answer: func([]string) string { return `{"success":true,"data":"surprise"}` },
			hashes: []string{a},
			err:    "couldn't read",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/torrents/checkcached" || r.Method != http.MethodGet {
					http.NotFound(w, r)
					return
				}
				if r.Header.Get("Authorization") != "Bearer tb-key" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				asked := strings.Split(r.URL.Query().Get("hash"), ",")
				if r.URL.Query().Get("format") != "list" {
					t.Errorf("format = %q, want list", r.URL.Query().Get("format"))
				}
				mu.Lock()
				seen = append(seen, asked...)
				mu.Unlock()
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.answer(asked))
			}))
			defer srv.Close()

			var svc Service = &torBox{key: "tb-key", http: srv.Client(), base: srv.URL}
			cc, ok := svc.(CacheChecker)
			if !ok || !CanCheckCache(svc) {
				t.Fatal("TorBox should be able to check the cache")
			}
			got, err := cc.Cached(context.Background(), tc.hashes)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want one with %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for h, w := range tc.want {
				if got[h] != w {
					t.Errorf("%s cached = %v, want %v", h, got[h], w)
				}
			}
			for _, h := range seen {
				if h != strings.ToLower(h) || h == "" {
					t.Errorf("asked for %q: hashes should go out lower case and non-empty", h)
				}
			}
		})
	}
}

// A long list goes out in several questions, never one that is too long.
func TestTorBoxCachedBatches(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked := strings.Split(r.URL.Query().Get("hash"), ",")
		mu.Lock()
		sizes = append(sizes, len(asked))
		mu.Unlock()
		// Every hash that ends in 0 is cached.
		var items []string
		for _, h := range asked {
			if strings.HasSuffix(h, "0") {
				items = append(items, fmt.Sprintf(`{"hash":%q}`, h))
			}
		}
		fmt.Fprint(w, `{"success":true,"data":[`+strings.Join(items, ",")+`]}`)
	}))
	defer srv.Close()
	var hashes []string
	for i := 0; i < 120; i++ {
		hashes = append(hashes, fmt.Sprintf("%039x%d", i, i%10))
	}
	got, err := (&torBox{key: "k", http: srv.Client(), base: srv.URL}).Cached(context.Background(), hashes)
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 3 || sizes[0] != checkCachedBatch || sizes[2] != 20 {
		t.Fatalf("batches = %v", sizes)
	}
	cached := 0
	for _, v := range got {
		if v {
			cached++
		}
	}
	if len(got) != 120 || cached != 12 {
		t.Fatalf("answered %d hashes, %d cached", len(got), cached)
	}
}

// Real-Debrid has no availability check any more, so the service doesn't offer one.
func TestRealDebridCannotCheck(t *testing.T) {
	svc, err := New(RealDebrid, "key", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if CanCheckCache(svc) {
		t.Fatal("Real-Debrid must not claim a cache check")
	}
}
