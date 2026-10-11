package api_test

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/netproxy"
)

// connectProxy is a small HTTP proxy (CONNECT and forwarded requests) that
// wants user "px" with password "secret", and counts what it carries.
func connectProxy(t *testing.T, carried *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("px:secret")) {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		carried.Add(1)
		if r.Method == http.MethodConnect {
			target, err := net.Dial("tcp", r.Host)
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			c, buf, _ := w.(http.Hijacker).Hijack()
			c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
			go func() { io.Copy(target, buf); target.Close() }()
			io.Copy(c, target)
			c.Close()
			return
		}
		out, _ := http.NewRequest(r.Method, r.URL.String(), r.Body)
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProxySettingsAndUsenetThroughIt(t *testing.T) {
	t.Cleanup(func() { netproxy.Set(netproxy.Config{}) })
	e1 := []byte("episode one " + strings.Repeat("a", 400))
	env := newTVEnv(t, map[string]nntpArticle{"e1@example": {fileName: "Fixture.Show.S01E01.1080p.WEB-DL.x264-GRP.mkv", content: e1}})
	base := env.httpSrv.URL

	var carried atomic.Int32
	px := connectProxy(t, &carried)
	host, portText, _ := net.SplitHostPort(strings.TrimPrefix(px.URL, "http://"))
	port, _ := strconv.Atoi(portText)

	// Nothing set at first.
	if got := getJSON[map[string]any](t, env.client, base+"/api/proxy"); got["type"] != "" {
		t.Fatalf("no proxy at first: %v", got)
	}

	// Mistakes are refused.
	putJSONStatus(t, env.client, base+"/api/proxy", map[string]any{"type": "socks4", "host": host, "port": port}, http.StatusBadRequest)
	putJSONStatus(t, env.client, base+"/api/proxy", map[string]any{"type": "http", "host": "http://" + host, "port": port}, http.StatusBadRequest)
	putJSONStatus(t, env.client, base+"/api/proxy", map[string]any{"type": "http", "host": host, "port": 0}, http.StatusBadRequest)

	// Test with a wrong password, then the right one, before saving.
	env.server.SetProxyTestTarget(strings.TrimPrefix(base, "http://"))
	bad := postJSON[map[string]any](t, env.client, base+"/api/proxy/test", map[string]any{"type": "http", "host": host, "port": port, "username": "px", "password": "nope"}, http.StatusOK)
	if bad["ok"] != false || !strings.Contains(bad["message"].(string), "user name or password") {
		t.Fatalf("wrong password: %v", bad)
	}
	good := postJSON[map[string]any](t, env.client, base+"/api/proxy/test", map[string]any{"type": "http", "host": host, "port": port, "username": "px", "password": "secret"}, http.StatusOK)
	if good["ok"] != true {
		t.Fatalf("right password: %v", good)
	}

	// Save it for Usenet only. The password never comes back.
	saved := putJSONStatus(t, env.client, base+"/api/proxy", map[string]any{"type": "http", "host": host, "port": port, "username": "px", "password": "secret", "usenet": true}, http.StatusOK)
	if saved["hasPassword"] != true || saved["usenet"] != true || saved["torrents"] != false {
		t.Fatalf("saved: %v", saved)
	}
	raw, _ := io.ReadAll(mustGet(t, env.client, base+"/api/proxy").Body)
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("the password is sent back: %s", raw)
	}
	// Saving again without a password keeps it.
	putJSONStatus(t, env.client, base+"/api/proxy", map[string]any{"type": "http", "host": host, "port": port, "username": "px", "usenet": true}, http.StatusOK)
	// The saved password goes only with the address it was saved for: another address without a password is tried
	// without one (this stops a stolen key from sending the password to a proxy of someone else's).
	if host != "localhost" {
		other := postJSON[map[string]any](t, env.client, base+"/api/proxy/test", map[string]any{"type": "http", "host": "localhost", "port": port, "username": "px"}, http.StatusOK)
		if other["ok"] == true {
			t.Fatalf("the saved password was sent to another address: %v", other)
		}
	}

	before := carried.Load()
	env.grab(t, "Fixture.Show.S01E01.1080p.WEB-DL.x264-GRP")
	if status, errMsg := env.waitForTerminal(t); status != "completed" {
		t.Fatalf("the download through the proxy didn't complete: %s %s", status, errMsg)
	}
	if carried.Load() == before {
		t.Fatal("the Usenet connections didn't go through the proxy")
	}

	// Off again.
	off := putJSONStatus(t, env.client, base+"/api/proxy", map[string]any{"type": ""}, http.StatusOK)
	if off["type"] != "" || netproxy.For(netproxy.Usenet) != nil {
		t.Fatalf("switched off: %v", off)
	}
}

func mustGet(t *testing.T, c *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestTorrentProxyCountsForTheKillSwitch: with "require VPN for torrents"
// on, a proxy for torrents lets a torrent grab go ahead (through the proxy)
// instead of failing for want of a VPN.
func TestTorrentProxyCountsForTheKillSwitch(t *testing.T) {
	t.Cleanup(func() { netproxy.Set(netproxy.Config{}) })
	server, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	postJSON[map[string]any](t, client, base+"/api/onboarding/admin", map[string]string{
		"username": "ryan", "password": "correct-horse-battery-staple", "firstName": "Ryan", "lastName": "Tester",
	}, http.StatusCreated)
	var carried atomic.Int32
	px := connectProxy(t, &carried)
	host, portText, _ := net.SplitHostPort(strings.TrimPrefix(px.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"requireVpnForTorrents": true}, http.StatusOK)
	putJSONStatus(t, client, base+"/api/proxy", map[string]any{"type": "http", "host": host, "port": port, "username": "px", "password": "secret", "torrents": true}, http.StatusOK)

	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 605, Title: "The Proxied Movie", Year: 2001, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	grab := postJSON[map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d/grab", base, movie.ID), map[string]any{
		"releaseTitle": "The Proxied Movie 2001 1080p",
		"downloadUrl":  "magnet:?xt=urn:btih:0000000000000000000000000000000000000001&dn=fixture",
		"sizeBytes":    int64(123456),
		"protocol":     "torrent",
	}, http.StatusAccepted)
	time.Sleep(1500 * time.Millisecond)
	list := getJSON[[]map[string]any](t, client, base+"/api/queue")
	if len(list) != 1 {
		t.Fatalf("queue: %v", list)
	}
	if status, _ := list[0]["status"].(string); status == "failed" {
		t.Fatalf("the grab failed although a proxy is set for torrents: %v", list[0]["error"])
	}
	if code, _ := doJSONStatus(t, client, http.MethodPost, fmt.Sprintf("%s/api/queue/%v/stop", base, grab["queueId"]), nil); code/100 != 2 {
		t.Fatalf("stop: %d", code)
	}
	waitBackground(t, server)
}
