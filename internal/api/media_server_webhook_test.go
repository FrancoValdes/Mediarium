package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/library"
)

type webhookCall struct {
	Auth    string
	Event   string   `json:"event"`
	Kind    string   `json:"kind"`
	Folders []string `json:"folders"`
	Source  string   `json:"source"`
}

// fakeWebhook records every message it gets; it refuses calls without the
// right token.
func fakeWebhook(t *testing.T, token string) (*httptest.Server, func() []webhookCall) {
	t.Helper()
	var (
		mu    sync.Mutex
		calls []webhookCall
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/hooks/media" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var c webhookCall
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.Auth = r.Header.Get("Authorization")
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []webhookCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]webhookCall(nil), calls...)
	}
}

func TestWebhookMediaServerAfterImport(t *testing.T) {
	movieContent := []byte("Fixture movie bytes. " + strings.Repeat("padding-", 200))
	nntpSrv := newFakeNNTPServer(t, movieContent)
	indexerSrv := newFakeIndexerServer(t)
	hook, calls := fakeWebhook(t, "hook-token")

	server, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	postJSON[map[string]any](t, client, base+"/api/onboarding/admin", map[string]string{
		"username": "ryan", "password": "correct-horse-battery-staple", "firstName": "Ryan", "lastName": "Tester",
	}, http.StatusCreated)
	moviesDir, _ := getJSON[map[string]any](t, client, base+"/api/settings")["moviesPath"].(string)
	postJSON[map[string]any](t, client, base+"/api/indexers", map[string]any{
		"name": "Fixture Indexer", "definitionId": "fixture", "baseUrl": indexerSrv.URL, "apiKey": "fixture-key",
	}, http.StatusCreated)
	postJSON[map[string]any](t, client, base+"/api/usenet-servers", map[string]any{
		"name": "Fixture Usenet", "host": nntpSrv.addr, "port": nntpSrv.port, "useSsl": false, "connections": 2,
	}, http.StatusCreated)

	// Test with a wrong token, then the right one.
	code, bad := doJSONStatus(t, client, http.MethodPost, base+"/api/media-servers/test", map[string]any{"kind": "webhook", "baseUrl": hook.URL + "/hooks/media", "token": "nope"})
	if code/100 == 2 && bad["ok"] != false {
		t.Fatalf("a wrong token should fail: %d %v", code, bad)
	}
	if msg := fmt.Sprint(bad); !strings.Contains(msg, "Check the token") {
		t.Fatalf("wrong token message: %v", bad)
	}
	postJSON[map[string]any](t, client, base+"/api/media-servers/test", map[string]any{"kind": "webhook", "baseUrl": hook.URL + "/hooks/media", "token": "hook-token"}, http.StatusOK)
	if got := calls(); len(got) != 1 || got[0].Event != "test" || got[0].Source != "mediarium" {
		t.Fatalf("test call: %+v", got)
	}

	created := postJSON[map[string]any](t, client, base+"/api/media-servers", map[string]any{
		"kind": "webhook", "name": "Silo", "baseUrl": hook.URL + "/hooks/media", "token": "hook-token",
		"pathMap": []map[string]string{{"from": moviesDir, "to": "/data/movies"}},
	}, http.StatusCreated)
	if created["kind"] != "webhook" || created["name"] != "Silo" {
		t.Fatalf("created: %v", created)
	}
	// A webhook has no titles: no "Watch in" link and no home link.
	links := getJSON[any](t, client, base+"/api/media-servers/links?kind=movie&tmdbId=603")
	if raw, _ := json.Marshal(links); strings.Contains(string(raw), "Silo") {
		t.Fatalf("a webhook shouldn't get links: %s", raw)
	}

	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 603, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	results := getJSON[[]map[string]any](t, client, base+"/api/search?q=fixture")
	postJSON[map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d/grab", base, movie.ID), map[string]any{
		"releaseTitle": results[0]["title"], "downloadUrl": results[0]["downloadUrl"], "sizeBytes": int64(len(movieContent)),
	}, http.StatusAccepted)
	deadline := time.Now().Add(20 * time.Second)
	for {
		q := getJSON[[]map[string]any](t, client, base+"/api/queue")
		if len(q) == 1 && (q[0]["status"] == "completed" || q[0]["status"] == "failed") {
			if q[0]["status"] == "failed" {
				t.Fatalf("pipeline failed: %+v", q[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pipeline did not finish: %+v", q)
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitBackground(t, server)
	server.TestFlushMediaServerRefresh()

	final := getJSON[map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d", base, movie.ID))
	rel, err := filepath.Rel(moviesDir, filepath.Dir(fmt.Sprint(final["filePath"])))
	if err != nil {
		t.Fatal(err)
	}
	want := "/data/movies/" + filepath.ToSlash(rel)
	got := calls()
	if len(got) != 2 {
		t.Fatalf("want the test call and one refresh, got %+v", got)
	}
	if r := got[1]; r.Event != "refresh" || r.Kind != "movie" || len(r.Folders) != 1 || r.Folders[0] != want || r.Auth != "Bearer hook-token" {
		t.Fatalf("refresh call = %+v, want folder %q", r, want)
	}
}
