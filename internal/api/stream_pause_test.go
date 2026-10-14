package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/mediaservers"
)

// A media server that goes away while someone was watching must not keep the
// downloads paused for good: they come back once no server has answered for
// a while, and at once when every server is removed.
func TestStreamPauseGivesBackWhenNoServerAnswers(t *testing.T) {
	// start holds a download paused for a stream on a Jellyfin server that
	// says someone is watching.
	start := func(t *testing.T) (*controlEnv, int64, *httptest.Server, mediaservers.Server) {
		e := newControlEnv(t, 2)
		running := e.downloadHeld(t)
		jf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `[{"Id":"a","NowPlayingItem":{"Name":"A film"}}]`)
		}))
		t.Cleanup(jf.Close)
		created, err := e.server.MediaServers.Create(mediaservers.Server{Name: "Jellyfin", Kind: mediaservers.KindJellyfin, BaseURL: jf.URL, Token: "k", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		putJSONStatus(t, e.client, e.base+"/api/downloads/stream-pause", map[string]any{"enabled": true}, http.StatusOK)
		e.server.TestStreamPauseRound(context.Background())
		waitFor(t, "the running download to pause", func() bool { return e.status(t, running) == "paused" })
		return e, running, jf, created
	}

	t.Run("a server that stays down", func(t *testing.T) {
		e, running, jf, _ := start(t)
		ctx := context.Background()
		jf.Close()
		// A few checks in a short time change nothing.
		for range 4 {
			e.server.TestStreamPauseRound(ctx)
		}
		if e.status(t, running) != "paused" {
			t.Fatalf("a server that was only out of reach briefly gave the downloads back: %s", e.status(t, running))
		}
		// Ten minutes on, still nobody to ask: they go back.
		e.server.TestStreamPauseUnreachableFor(11 * time.Minute)
		e.server.TestStreamPauseRound(ctx)
		waitFor(t, "the download to carry on", func() bool { return e.status(t, running) != "paused" })
		e.nntp.open()
		e.waitAllDone(t, []int64{running})
	})

	t.Run("every server removed", func(t *testing.T) {
		e, running, _, created := start(t)
		if err := e.server.MediaServers.Delete(created.ID); err != nil {
			t.Fatal(err)
		}
		e.server.TestStreamPauseRound(context.Background())
		waitFor(t, "removing the server to give the download back", func() bool { return e.status(t, running) != "paused" })
		e.nntp.open()
		e.waitAllDone(t, []int64{running})
	})
}

// With the switch on, downloads pause while someone watches and carry on a
// minute after the last person stops. Only what was paused for a stream is
// put back.
func TestDownloadsPauseWhileSomeoneIsWatching(t *testing.T) {
	e := newControlEnv(t, 2)
	m2, m3 := lineMovie(t, e, 2), lineMovie(t, e, 3)
	running := e.downloadHeld(t)
	waiting, _ := e.server.TestGrabMovie(m2, releaseName, e.nzbURL)
	byHand, _ := e.server.TestGrabMovie(m3, releaseName, e.nzbURL)
	// A person paused this one: it is not Mediarium's to resume.
	postJSON[map[string]any](t, e.client, e.url(byHand, "pause"), nil, http.StatusOK)

	var watching atomic.Int32
	jf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Sessions" {
			http.NotFound(w, r)
			return
		}
		if watching.Load() > 0 {
			fmt.Fprint(w, `[{"Id":"a","NowPlayingItem":{"Name":"A film"}}]`)
			return
		}
		fmt.Fprint(w, `[{"Id":"a"}]`)
	}))
	defer jf.Close()
	if _, err := e.server.MediaServers.Create(mediaservers.Server{Name: "Jellyfin", Kind: mediaservers.KindJellyfin, BaseURL: jf.URL, Token: "k", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Off by default: someone watching changes nothing.
	watching.Store(1)
	e.server.TestStreamPauseRound(ctx)
	if e.status(t, running) != "downloading" {
		t.Fatalf("the switch is off, so nothing pauses: %s", e.status(t, running))
	}

	putJSONStatus(t, e.client, e.base+"/api/downloads/stream-pause", map[string]any{"enabled": true}, http.StatusOK)
	e.server.TestStreamPauseRound(ctx)
	waitFor(t, "the running download to pause", func() bool { return e.status(t, running) == "paused" })
	if e.status(t, waiting) != "paused" {
		t.Fatalf("the waiting download pauses too: %s", e.status(t, waiting))
	}
	st := getJSON[map[string]any](t, e.client, e.base+"/api/downloads/stream-pause")
	if st["watching"] != float64(1) || st["held"] != float64(2) || st["servers"] != float64(1) {
		t.Fatalf("the status says what it found: %v", st)
	}

	// Nobody watching: the first quiet check waits, the second puts them back.
	watching.Store(0)
	e.server.TestStreamPauseRound(ctx)
	if e.status(t, running) != "paused" {
		t.Fatal("one quiet check is not enough")
	}
	e.server.TestStreamPauseRound(ctx)
	waitFor(t, "the downloads to carry on", func() bool { return e.status(t, running) != "paused" && e.status(t, waiting) != "paused" })
	if e.status(t, byHand) != "paused" {
		t.Fatalf("what a person paused stays paused: %s", e.status(t, byHand))
	}
	if st := getJSON[map[string]any](t, e.client, e.base+"/api/downloads/stream-pause"); st["held"] != float64(0) {
		t.Fatalf("nothing is held now: %v", st)
	}

	// Switching it off while holding gives the downloads back.
	watching.Store(1)
	e.server.TestStreamPauseRound(ctx)
	waitFor(t, "the downloads to pause again", func() bool { return e.status(t, running) == "paused" })
	putJSONStatus(t, e.client, e.base+"/api/downloads/stream-pause", map[string]any{"enabled": false}, http.StatusOK)
	waitFor(t, "switching off to give them back", func() bool { return e.status(t, running) != "paused" })
	e.nntp.open()
	postJSON[map[string]any](t, e.client, e.url(byHand, "resume"), nil, http.StatusOK)
	e.waitAllDone(t, []int64{running, waiting, byHand})
}
