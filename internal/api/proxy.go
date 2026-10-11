package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/netproxy"
	"github.com/rdborg/mediarium/internal/settings"
)

// The proxy setting: a SOCKS5 or HTTP proxy for torrents, indexers and/or
// Usenet (see internal/netproxy). Stored as one encrypted address plus the
// list of what uses it, and applied to every new connection straight away.

// defaultProxyTestAddr is what a proxy test connects to: TMDB, which
// Mediarium talks to anyway. Only a TLS handshake is made; nothing is sent.
const defaultProxyTestAddr = "api.themoviedb.org:443"

// SetProxyTestTarget makes the proxy test connect somewhere else (tests).
func (s *Server) SetProxyTestTarget(addr string) { s.proxyTestAddr = addr }

type proxyPayload struct {
	// Type is "socks5" or "http"; "" means no proxy.
	Type        string `json:"type"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	HasPassword bool   `json:"hasPassword"`
	Torrents    bool   `json:"torrents"`
	Indexers    bool   `json:"indexers"`
	Usenet      bool   `json:"usenet"`
}

type proxyRequest struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	// Password: left out keeps the saved one, "" removes it.
	Password *string `json:"password"`
	Torrents bool    `json:"torrents"`
	Indexers bool    `json:"indexers"`
	Usenet   bool    `json:"usenet"`
}

// savedProxy reads the stored setting.
func (s *Server) savedProxy() (netproxy.Config, error) {
	raw, err := s.Settings.Get(settings.KeyProxy)
	if err != nil {
		return netproxy.Config{}, err
	}
	forRaw, err := s.Settings.Get(settings.KeyProxyFor)
	if err != nil {
		return netproxy.Config{}, err
	}
	cfg := netproxy.Config{For: map[netproxy.Use]bool{}}
	for _, u := range strings.Split(forRaw, ",") {
		if u = strings.TrimSpace(u); u != "" {
			cfg.For[netproxy.Use(u)] = true
		}
	}
	if strings.TrimSpace(raw) != "" {
		u, err := netproxy.Parse(raw)
		if err != nil {
			return netproxy.Config{}, fmt.Errorf("the saved proxy: %w", err)
		}
		cfg.URL = u
	}
	return cfg, nil
}

// applyProxy puts the stored setting to use. A setting that can't be read
// leaves traffic going direct, and says so in the log.
func (s *Server) applyProxy() {
	cfg, err := s.savedProxy()
	if err != nil {
		log.Printf("proxy: %v; not using a proxy", err)
		netproxy.Set(netproxy.Config{})
		return
	}
	netproxy.Set(cfg)
}

func toProxyPayload(cfg netproxy.Config) proxyPayload {
	p := proxyPayload{Torrents: cfg.For[netproxy.Torrents], Indexers: cfg.For[netproxy.Indexers], Usenet: cfg.For[netproxy.Usenet]}
	if cfg.URL == nil {
		return p
	}
	p.Type = "socks5"
	if cfg.URL.Scheme == "http" {
		p.Type = "http"
	}
	p.Host = cfg.URL.Hostname()
	p.Port, _ = strconv.Atoi(cfg.URL.Port())
	if cfg.URL.User != nil {
		p.Username = cfg.URL.User.Username()
		_, p.HasPassword = cfg.URL.User.Password()
	}
	return p
}

func (s *Server) handleGetProxy(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.savedProxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toProxyPayload(cfg))
}

// proxyFromRequest builds the proxy address from the form, taking the saved
// password when the form leaves it out.
func (s *Server) proxyFromRequest(req proxyRequest) (netproxy.Config, error) {
	cfg := netproxy.Config{For: map[netproxy.Use]bool{
		netproxy.Torrents: req.Torrents, netproxy.Indexers: req.Indexers, netproxy.Usenet: req.Usenet,
	}}
	if req.Type == "" {
		return cfg, nil
	}
	if req.Type != "socks5" && req.Type != "http" {
		return cfg, fmt.Errorf("pick SOCKS5 or HTTP")
	}
	host := strings.TrimSpace(req.Host)
	if host == "" || strings.ContainsAny(host, "/@?# ") {
		return cfg, fmt.Errorf("type the proxy's host name or IP address, without http:// or a port")
	}
	if req.Port < 1 || req.Port > 65535 {
		return cfg, fmt.Errorf("the port must be a number from 1 to 65535")
	}
	u := &url.URL{Scheme: req.Type, Host: net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(req.Port))}
	user := strings.TrimSpace(req.Username)
	password := ""
	if req.Password != nil {
		password = *req.Password
	} else if saved, err := s.savedProxy(); err == nil && saved.URL != nil && saved.URL.User != nil && saved.URL.User.Username() == user &&
		saved.URL.Scheme == req.Type && strings.EqualFold(saved.URL.Hostname(), strings.Trim(host, "[]")) && saved.URL.Port() == strconv.Itoa(req.Port) {
		// The saved password only goes with the address it was saved for; a new address needs it typed again.
		password, _ = saved.URL.User.Password()
	}
	if user != "" {
		u.User = url.UserPassword(user, password)
	} else if password != "" {
		return cfg, fmt.Errorf("a password needs a user name too")
	}
	parsed, err := netproxy.Parse(u.String())
	if err != nil {
		return cfg, err
	}
	cfg.URL = parsed
	return cfg, nil
}

func (s *Server) handlePutProxy(w http.ResponseWriter, r *http.Request) {
	var req proxyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	cfg, err := s.proxyFromRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, sentence(err.Error()))
		return
	}
	raw := ""
	if cfg.URL != nil {
		raw = cfg.URL.String()
	}
	var uses []string
	for _, u := range netproxy.Uses {
		if cfg.For[u] {
			uses = append(uses, string(u))
		}
	}
	if err := s.Settings.Set(settings.KeyProxy, raw, true); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Settings.Set(settings.KeyProxyFor, strings.Join(uses, ","), false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	netproxy.Set(cfg)
	if cfg.URL != nil {
		log.Printf("proxy: set to %s for %s", cfg.URL.Scheme+"://"+cfg.URL.Host, strings.Join(uses, ", "))
	} else {
		log.Printf("proxy: switched off")
	}
	writeJSON(w, http.StatusOK, toProxyPayload(cfg))
}

// handleTestProxy connects through the proxy in the form (not saved yet)
// and makes a TLS handshake with TMDB's server, which proves the proxy
// carries traffic out. Nothing else is sent.
func (s *Server) handleTestProxy(w http.ResponseWriter, r *http.Request) {
	var req proxyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "Pick SOCKS5 or HTTP and fill in the proxy first.")
		return
	}
	cfg, err := s.proxyFromRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, sentence(err.Error()))
		return
	}
	target := s.proxyTestAddr
	if target == "" {
		target = defaultProxyTestAddr
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	started := time.Now()
	conn, err := netproxy.New(cfg.URL).DialContext(ctx, "tcp", target)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": sentence(err.Error())})
		return
	}
	defer conn.Close()
	host, port, _ := net.SplitHostPort(target)
	if port == "443" {
		tc := tls.Client(conn, &tls.Config{ServerName: host})
		if err := tc.HandshakeContext(ctx); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "The proxy connected, but the secure connection through it failed: " + err.Error()})
			return
		}
	}
	ms := time.Since(started).Milliseconds()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": fmt.Sprintf("The proxy works: reached %s through it in %d ms.", host, ms)})
}
