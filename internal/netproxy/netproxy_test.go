package netproxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// socksServer is a minimal SOCKS5 server (CONNECT only) that counts the
// connections it carries. With user set, it wants that user name and
// password.
type socksServer struct {
	ln         net.Listener
	user, pass string
	carried    atomic.Int32
}

func newSocksServer(t *testing.T, user, pass string) *socksServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &socksServer{ln: ln, user: user, pass: pass}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *socksServer) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil || head[0] != 5 {
		return
	}
	if _, err := io.ReadFull(r, make([]byte, head[1])); err != nil {
		return
	}
	if s.user == "" {
		c.Write([]byte{5, 0})
	} else {
		c.Write([]byte{5, 2})
		b := make([]byte, 2)
		io.ReadFull(r, b)
		u := make([]byte, b[1])
		io.ReadFull(r, u)
		pl, _ := r.ReadByte()
		p := make([]byte, pl)
		io.ReadFull(r, p)
		if string(u) != s.user || string(p) != s.pass {
			c.Write([]byte{1, 1})
			return
		}
		c.Write([]byte{1, 0})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(r, req); err != nil || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		io.ReadFull(r, ip)
		host = net.IP(ip).String()
	case 3:
		n, _ := r.ReadByte()
		name := make([]byte, n)
		io.ReadFull(r, name)
		host = string(name)
	case 4:
		ip := make([]byte, 16)
		io.ReadFull(r, ip)
		host = net.IP(ip).String()
	}
	pb := make([]byte, 2)
	io.ReadFull(r, pb)
	target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb)))))
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()
	s.carried.Add(1)
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go io.Copy(target, r)
	io.Copy(c, target)
}

// httpProxy is a minimal HTTP proxy: CONNECT tunnels and plain forwarded
// requests, behind Basic auth when user is set.
func httpProxy(t *testing.T, user, pass string, carried *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user != "" && r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)) {
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
			hj, _ := w.(http.Hijacker)
			c, buf, _ := hj.Hijack()
			c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
			go io.Copy(target, buf)
			io.Copy(c, target)
			c.Close()
			target.Close()
			return
		}
		out, _ := http.NewRequest(r.Method, r.URL.String(), r.Body)
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func target(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, c *http.Client, u string) (string, error) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in, err string
	}{
		{"socks5://10.0.0.2:1080", ""},
		{"socks5h://user:pa%40ss@proxy.lan:1080", ""},
		{"http://proxy.lan:3128", ""},
		{"https://proxy.lan:3128", "SOCKS5 or HTTP"},
		{"socks4://proxy.lan:1080", "SOCKS5 or HTTP"},
		{"socks5://:1080", "host name"},
		{"socks5://proxy.lan", "port"},
		{"socks5://proxy.lan:70000", "port"},
	} {
		_, err := Parse(tc.in)
		if (err == nil) != (tc.err == "") || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("Parse(%q) = %v, want %q", tc.in, err, tc.err)
		}
	}
	if u := mustParse(t, "socks5://u:p@h:1/path?x=1"); u.String() != "socks5://u:p@h:1" {
		t.Errorf("path and query should be dropped: %s", u)
	}
}

func TestSocksForIndexersOnly(t *testing.T) {
	t.Cleanup(func() { Set(Config{}) })
	tgt := target(t)
	socks := newSocksServer(t, "", "")
	Set(Config{URL: mustParse(t, "socks5://"+socks.ln.Addr().String()), For: map[Use]bool{Indexers: true}})

	if body, err := get(t, Client(5*time.Second, Indexers), tgt.URL); err != nil || body != "hello" {
		t.Fatalf("through the proxy: %q %v", body, err)
	}
	if socks.carried.Load() != 1 {
		t.Fatalf("the proxy carried %d connections, want 1", socks.carried.Load())
	}
	// Usenet isn't ticked: straight to the server.
	conn, err := DialContext(context.Background(), Usenet, 5*time.Second, "tcp", strings.TrimPrefix(tgt.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if socks.carried.Load() != 1 {
		t.Fatal("Usenet went through the proxy although it isn't ticked")
	}
	// Switched off: direct again, with the same client.
	c := Client(5*time.Second, Indexers)
	Set(Config{})
	if body, err := get(t, c, tgt.URL); err != nil || body != "hello" || socks.carried.Load() != 1 {
		t.Fatalf("without the proxy: %q %v, carried %d", body, err, socks.carried.Load())
	}
}

func TestSocksWithPassword(t *testing.T) {
	t.Cleanup(func() { Set(Config{}) })
	tgt := target(t)
	socks := newSocksServer(t, "alice", "s3cret")
	addr := socks.ln.Addr().String()

	Set(Config{URL: mustParse(t, "socks5://alice:wrong@"+addr), For: map[Use]bool{Usenet: true}})
	_, err := DialContext(context.Background(), Usenet, 5*time.Second, "tcp", strings.TrimPrefix(tgt.URL, "http://"))
	if err == nil || !strings.Contains(err.Error(), "refused the user name or password") {
		t.Fatalf("a wrong password: %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("the error shows the password: %v", err)
	}
	Set(Config{URL: mustParse(t, "socks5://alice:s3cret@"+addr), For: map[Use]bool{Usenet: true}})
	conn, err := DialContext(context.Background(), Usenet, 5*time.Second, "tcp", strings.TrimPrefix(tgt.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestHTTPProxy(t *testing.T) {
	t.Cleanup(func() { Set(Config{}) })
	tgt := target(t)
	var carried atomic.Int32
	px := httpProxy(t, "bob", "pw", &carried)
	host := strings.TrimPrefix(px.URL, "http://")

	Set(Config{URL: mustParse(t, "http://bob:pw@"+host), For: map[Use]bool{Indexers: true, Torrents: true}})
	if body, err := get(t, Client(5*time.Second, Indexers), tgt.URL); err != nil || body != "hello" {
		t.Fatalf("forwarded request: %q %v", body, err)
	}
	// Torrent peers use CONNECT through the same proxy.
	d := For(Torrents)
	if d == nil || d != For(Indexers) {
		t.Fatal("one dialer per setting")
	}
	conn, err := d.DialContext(context.Background(), "tcp", strings.TrimPrefix(tgt.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(conn, "GET / HTTP/1.0\r\n\r\n")
	b, _ := io.ReadAll(conn)
	conn.Close()
	if !strings.Contains(string(b), "hello") {
		t.Fatalf("CONNECT tunnel: %q", b)
	}
	if carried.Load() != 2 {
		t.Fatalf("the proxy carried %d, want 2", carried.Load())
	}

	Set(Config{URL: mustParse(t, "http://bob:nope@"+host), For: map[Use]bool{Torrents: true}})
	if _, err := For(Torrents).DialContext(context.Background(), "tcp", "127.0.0.1:1"); err == nil || !strings.Contains(err.Error(), "refused the user name or password") {
		t.Fatalf("wrong password: %v", err)
	}
}
