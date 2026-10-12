package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The smallest Transmission there is: the session id handshake, basic
// authentication and a version.
func newTinyTransmission(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transmission/rpc" {
			http.NotFound(w, r)
			return
		}
		if u, p, ok := r.BasicAuth(); !ok || u != "admin" || p != "adminpass" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Transmission-Session-Id") != "sid-1" {
			w.Header().Set("X-Transmission-Session-Id", "sid-1")
			http.Error(w, "409: Conflict", http.StatusConflict)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "success", "arguments": map[string]any{"version": "4.0.5 (a6fe2f7be1)"}})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestTransmissionAsTheDownloadClient(t *testing.T) {
	e := newClientEnv(t)
	tm := newTinyTransmission(t)

	bad := postJSON[map[string]any](t, e.client, e.url+"/api/download-client/test", map[string]any{"kind": "transmission", "url": tm, "username": "admin", "password": "wrong"}, http.StatusOK)
	if bad["ok"] != false || !strings.Contains(fmt.Sprint(bad["error"]), "Transmission refused the user name or password") {
		t.Fatalf("a wrong password says so, by name: %v", bad)
	}
	good := postJSON[map[string]any](t, e.client, e.url+"/api/download-client/test", map[string]any{"kind": "transmission", "url": tm, "username": "admin", "password": "adminpass"}, http.StatusOK)
	if good["ok"] != true || !strings.HasPrefix(fmt.Sprint(good["version"]), "4.0.5") {
		t.Fatalf("test = %v", good)
	}

	// An address that isn't Transmission is told so in plain words.
	other := postJSON[map[string]any](t, e.client, e.url+"/api/download-client/test", map[string]any{"kind": "transmission", "url": e.qbURL, "username": "admin", "password": "adminpass"}, http.StatusOK)
	if other["ok"] != false {
		t.Fatalf("qBittorrent's address is not Transmission's: %v", other)
	}

	putJSONStatus(t, e.client, e.url+"/api/download-client", map[string]any{"kind": "transmission", "url": tm, "username": "admin", "password": "adminpass", "category": "mediarium"}, http.StatusOK)
	got := getJSON[map[string]any](t, e.client, e.url+"/api/download-client")
	if got["kind"] != "transmission" || got["url"] != tm {
		t.Fatalf("saved settings: %v", got)
	}
	if _, has := got["password"]; has && got["password"] != "" {
		t.Fatalf("the password never comes back: %v", got["password"])
	}
}
