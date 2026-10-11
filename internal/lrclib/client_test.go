package lrclib_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rdborg/mediarium/internal/lrclib"
)

// fake replays LRCLIB's documented answers: GET /api/get gives one record or a
// 404 {code, name, message}; GET /api/search gives a list.
func fake(t *testing.T, get, search http.HandlerFunc) (*lrclib.Client, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/get", func(w http.ResponseWriter, r *http.Request) { hits.Add(1); get(w, r) })
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) { hits.Add(1); search(w, r) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != "Mediarium/9.9 ( https://github.com/rdborg/Mediarium )" {
			t.Errorf("User-Agent = %q", ua)
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return lrclib.New("9.9", lrclib.WithBaseURL(srv.URL), lrclib.WithHTTPClient(srv.Client())), &hits
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"code":404,"name":"TrackNotFound","message":"Failed to find specified track"}`))
}

func TestLookupExactMatch(t *testing.T) {
	c, hits := fake(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("track_name") != "Song" || q.Get("artist_name") != "The Band" || q.Get("album_name") != "Album" || q.Get("duration") != "201" {
			t.Errorf("query %v", q)
		}
		w.Write([]byte(`{"id":1,"trackName":"Song","artistName":"The Band","albumName":"Album","duration":200.8,"instrumental":false,
			"plainLyrics":"la la","syncedLyrics":"[00:01.00] la\n[00:02.00] la"}`))
	}, notFound)
	r, err := c.Lookup(context.Background(), "Song", "The Band", "Album", 201)
	if err != nil || !r.Found || r.Plain != "la la" || !strings.HasPrefix(r.Synced, "[00:01.00]") || hits.Load() != 1 {
		t.Fatalf("%+v %v hits %d", r, err, hits.Load())
	}
}

func TestLookupFallsBackToSearchAndPicksTheRightLength(t *testing.T) {
	c, _ := fake(t, notFound, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("track_name") != "Song" || r.URL.Query().Get("artist_name") != "Band" {
			t.Errorf("search query %v", r.URL.Query())
		}
		w.Write([]byte(`[
			{"trackName":"Song","artistName":"Band","duration":400,"plainLyrics":"wrong version","syncedLyrics":"[00:01.00]x\n[00:02.00]y"},
			{"trackName":"Song","artistName":"Band","duration":180.4,"plainLyrics":"short plain","syncedLyrics":null},
			{"trackName":"Song","artistName":"Band","duration":181.9,"plainLyrics":"close","syncedLyrics":"[00:01.00]a\n[00:02.00]b"}]`))
	})
	r, err := c.Lookup(context.Background(), "Song", "Band", "", 181)
	if err != nil || !r.Found || r.Plain != "close" || r.Synced == "" {
		t.Fatalf("%+v %v", r, err)
	}
	// With no length known, the best-lyrics record is taken.
	r, _ = c.Lookup(context.Background(), "Song", "Band", "", 0)
	if !strings.Contains(r.Plain, "wrong version") {
		t.Fatalf("unknown length: %+v", r)
	}
	// Nothing within the slack of the wanted length.
	if r, _ := c.Lookup(context.Background(), "Song", "Band", "", 60); r.Found {
		t.Fatalf("a record of another length must not be used: %+v", r)
	}
}

func TestLookupInstrumentalNotFoundAndErrors(t *testing.T) {
	c, _ := fake(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"instrumental":true,"plainLyrics":null,"syncedLyrics":null,"duration":100}`))
	}, notFound)
	if r, err := c.Lookup(context.Background(), "S", "A", "B", 100); err != nil || !r.Found || !r.Instrumental {
		t.Fatalf("instrumental: %+v %v", r, err)
	}

	c, hits := fake(t, notFound, notFound)
	if r, err := c.Lookup(context.Background(), "S", "A", "B", 100); err != nil || r.Found || hits.Load() != 2 {
		t.Fatalf("not found: %+v %v hits %d", r, err, hits.Load())
	}
	// Nothing to ask without a title and an artist.
	if r, err := c.Lookup(context.Background(), "", "A", "B", 100); err != nil || r.Found || hits.Load() != 2 {
		t.Fatalf("empty title: %+v %v", r, err)
	}

	c, _ = fake(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }, notFound)
	if _, err := c.Lookup(context.Background(), "S", "A", "B", 100); err == nil {
		t.Fatal("a broken server must be an error, so the miss isn't remembered")
	}
	c, _ = fake(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`not json`)) }, notFound)
	if _, err := c.Lookup(context.Background(), "S", "A", "B", 100); err == nil {
		t.Fatal("bad JSON must be an error")
	}
}
