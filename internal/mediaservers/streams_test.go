package mediaservers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestActiveStreams(t *testing.T) {
	jellyfin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Sessions" {
			http.NotFound(w, r)
			return
		}
		// One device is playing a film, one is only signed in, one is playing too.
		fmt.Fprint(w, `[{"Id":"a","NowPlayingItem":{"Name":"A film"}},{"Id":"b"},{"Id":"c","NowPlayingItem":{"Name":"An episode"},"PlayState":{"IsPaused":true}},{"Id":"d","NowPlayingItem":null}]`)
	}))
	defer jellyfin.Close()
	plex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status/sessions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"MediaContainer":{"size":2,"Metadata":[{"title":"One"},{"title":"Two"}]}}`)
	}))
	defer plex.Close()
	quiet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/status/sessions" {
			fmt.Fprint(w, `{"MediaContainer":{"size":0}}`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer quiet.Close()

	c := &Client{HTTP: jellyfin.Client(), Version: "test"}
	ctx := context.Background()
	for _, kind := range []Kind{KindJellyfin, KindEmby} {
		n, err := c.ActiveStreams(ctx, Server{Name: "J", Kind: kind, BaseURL: jellyfin.URL, Token: "k"})
		if err != nil || n != 2 {
			t.Fatalf("%s: %d streams, %v; want 2 (paused counts, signed in only and null do not)", kind, n, err)
		}
	}
	if n, err := c.ActiveStreams(ctx, Server{Name: "P", Kind: KindPlex, BaseURL: plex.URL, Token: "k"}); err != nil || n != 2 {
		t.Fatalf("plex: %d streams, %v; want 2", n, err)
	}
	if n, err := c.ActiveStreams(ctx, Server{Name: "Q", Kind: KindPlex, BaseURL: quiet.URL, Token: "k"}); err != nil || n != 0 {
		t.Fatalf("quiet plex: %d, %v", n, err)
	}
	if n, err := c.ActiveStreams(ctx, Server{Name: "Q", Kind: KindJellyfin, BaseURL: quiet.URL, Token: "k"}); err != nil || n != 0 {
		t.Fatalf("quiet jellyfin: %d, %v", n, err)
	}
	if n, err := c.ActiveStreams(ctx, Server{Name: "W", Kind: KindWebhook, BaseURL: quiet.URL}); err != nil || n != 0 {
		t.Fatalf("a kind with no sessions answers 0: %d, %v", n, err)
	}
	if _, err := c.ActiveStreams(ctx, Server{Name: "Down", Kind: KindJellyfin, BaseURL: "http://127.0.0.1:1", Token: "k"}); err == nil {
		t.Fatal("a server that can't be reached is an error, not 0")
	}
}
