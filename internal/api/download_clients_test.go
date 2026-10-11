package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/api"
)

// The Activity page's Download clients tab lists the torrents in the client,
// in plain words, and its buttons control them.
func TestDownloadClientTorrentsCanBeSeenAndControlled(t *testing.T) {
	e := newClientEnv(t)
	now := time.Now().Unix()
	e.qb.extra = []map[string]any{
		{"hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "Mine.Movie.2020.1080p", "progress": 0.4, "state": "downloading", "size": 4000, "dlspeed": 2_000_000, "upspeed": 1000, "num_seeds": 12, "num_leechs": 3, "eta": 600, "category": "mediarium", "tags": "mediarium-q7", "added_on": now - 3600, "last_activity": now},
		{"hash": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "name": "Somebody.Elses.Linux.ISO", "progress": 1.0, "state": "stalledUP", "size": 9000, "ratio": 1.5, "category": "linux", "added_on": now - 7200},
		{"hash": "cccccccccccccccccccccccccccccccccccccccc", "name": "Stuck.Show.S01", "progress": 0.1, "state": "stalledDL", "size": 5000, "eta": 8640000, "category": "mediarium", "added_on": now - 90000, "last_activity": now - 7200},
	}
	// Nothing set up yet: the tab has nothing to show.
	none := getJSON[map[string]any](t, e.client, e.url+"/api/download-client/torrents")
	if none["configured"] != false {
		t.Fatalf("no client: %v", none)
	}
	// Torrents go to the built-in client until one is connected.
	if via := getJSON[map[string]any](t, e.client, e.url+"/api/downloads/status")["torrent"].(map[string]any)["via"]; via != "builtin" {
		t.Fatalf("no client connected: torrents go to the built-in one, got %v", via)
	}
	e.save(t, map[string]any{})
	if tor := getJSON[map[string]any](t, e.client, e.url+"/api/downloads/status")["torrent"].(map[string]any); tor["via"] != "client" || tor["viaLabel"] != "qBittorrent" {
		t.Fatalf("a connected client takes the torrents: %v", tor)
	}

	mine := getJSON[map[string]any](t, e.client, e.url+"/api/download-client/torrents")
	list := mine["torrents"].([]any)
	if mine["configured"] != true || mine["label"] != "qBittorrent" || len(list) != 2 {
		t.Fatalf("only Mediarium's category by default: %v", mine)
	}
	first := list[0].(map[string]any)
	if first["name"] != "Mine.Movie.2020.1080p" || first["stage"] != "downloading" || first["stageText"] != "Downloading" || first["progress"] != float64(40) ||
		first["downSpeed"] != float64(2_000_000) || first["seeds"] != float64(12) || first["peers"] != float64(3) || first["etaSeconds"] != float64(600) || first["queueId"] != float64(7) || first["mine"] != true {
		t.Fatalf("a download in progress: %v", first)
	}
	stuck := list[1].(map[string]any)
	if stuck["stage"] != "waiting" || stuck["stageText"] != "Waiting for peers" || stuck["etaSeconds"] != float64(0) || stuck["idleSeconds"].(float64) < 7000 {
		t.Fatalf("a stalled one says how long it has been idle and has no time left: %v", stuck)
	}

	all := getJSON[map[string]any](t, e.client, e.url+"/api/download-client/torrents?scope=all")["torrents"].([]any)
	if len(all) != 3 {
		t.Fatalf("everything in the client: %v", all)
	}

	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, action := range []string{"pause", "resume", "recheck", "remove", "remove-files"} {
		postJSON[map[string]any](t, e.client, fmt.Sprintf("%s/api/download-client/torrents/%s/%s", e.url, hash, action), nil, http.StatusOK)
	}
	postJSON[map[string]any](t, e.client, fmt.Sprintf("%s/api/download-client/torrents/%s/explode", e.url, hash), nil, http.StatusBadRequest)
	postJSON[map[string]any](t, e.client, e.url+"/api/download-client/torrents/not-a-hash/pause", nil, http.StatusBadRequest)
	e.qb.mu.Lock()
	defer e.qb.mu.Unlock()
	if got := strings.Join(e.qb.actions, ";"); got != "stop "+hash+";start "+hash+";recheck "+hash {
		t.Fatalf("controls pressed: %q", got)
	}
	if got := strings.Join(e.qb.deleted, ";"); got != hash+" false;"+hash+" true" {
		t.Fatalf("removals: %q", got)
	}
}

// A client that can't be reached is said so on the tab, not answered with an error.
func TestDownloadClientTorrentsSaysWhenTheClientIsDown(t *testing.T) {
	e := newClientEnv(t)
	e.save(t, map[string]any{})
	postJSONMethod[map[string]any](t, e.client, http.MethodPut, e.url+"/api/download-client", map[string]any{"kind": "qbittorrent", "url": "http://127.0.0.1:1", "username": "admin", "password": "adminpass"}, http.StatusOK)
	got := getJSON[map[string]any](t, e.client, e.url+"/api/download-client/torrents")
	if got["configured"] != true || got["error"] == nil || got["error"] == "" {
		t.Fatalf("should say what is wrong: %v", got)
	}
}

// The same tab lists what is on a debrid account and can remove from it.
func TestDebridTorrentsCanBeSeenAndRemoved(t *testing.T) {
	var removed []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /torrents", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "RD1", "filename": "Show.S01", "bytes": 5000, "progress": 40, "status": "downloading", "speed": 1500, "seeders": 7, "added": "2026-10-09T08:00:00Z"},
			{"id": "RD2", "filename": "Dead.One", "bytes": 5, "progress": 0, "status": "dead", "added": "2026-10-08T08:00:00Z"},
		})
	})
	mux.HandleFunc("DELETE /torrents/delete/{id}", func(w http.ResponseWriter, r *http.Request) {
		removed = append(removed, r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer api.SetDebridURLForTest(srv.URL)()

	e := newClientEnv(t)
	none := getJSON[map[string]any](t, e.client, e.url+"/api/debrid/torrents")
	if none["configured"] != false {
		t.Fatalf("nothing set up: %v", none)
	}
	postJSONMethod[map[string]any](t, e.client, http.MethodPut, e.url+"/api/debrid", map[string]any{"service": "realdebrid", "apiKey": "rd-key"}, http.StatusOK)
	got := getJSON[map[string]any](t, e.client, e.url+"/api/debrid/torrents")
	list, _ := got["torrents"].([]any)
	if got["configured"] != true || got["label"] != "Real-Debrid" || len(list) != 2 {
		t.Fatalf("debrid list: %v", got)
	}
	first := list[0].(map[string]any)
	if first["name"] != "Show.S01" || first["stage"] != "downloading" || first["progress"] != float64(40) || first["downSpeed"] != float64(1500) || first["seeders"] != float64(7) {
		t.Fatalf("first: %v", first)
	}
	if list[1].(map[string]any)["stage"] != "problem" {
		t.Fatalf("a dead one is a problem: %v", list[1])
	}
	postJSON[map[string]any](t, e.client, e.url+"/api/debrid/torrents/RD2/remove", nil, http.StatusOK)
	postJSON[map[string]any](t, e.client, e.url+"/api/debrid/torrents/../remove", nil, http.StatusNotFound)
	if len(removed) != 1 || removed[0] != "RD2" {
		t.Fatalf("removed: %v", removed)
	}
}
