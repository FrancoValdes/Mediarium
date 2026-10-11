package api

import (
	"net/http"
	"net/url"
	"strings"
)

// withURLBase lets Mediarium be served under a sub-path (URL_BASE, for example
// /mediarium) behind a reverse proxy. A request for /mediarium/... is handled
// as the same request without the prefix, so the pages, the API, the
// Radarr/Sonarr/Lidarr-style APIs, the manifests and the metrics all answer
// under it. /mediarium itself goes to /mediarium/ so relative addresses work.
//
// Requests that arrive without the prefix are handled as they are. That keeps
// a proxy that removes the prefix before passing the request on working too,
// and the page it serves still points everything at the prefixed addresses.
//
// With no prefix (the default) the handler is returned unchanged.
func withURLBase(base string, next http.Handler) http.Handler {
	if base == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == base:
			target := url.URL{Path: base + "/", RawQuery: r.URL.RawQuery}
			http.Redirect(w, r, target.String(), http.StatusFound)
		case strings.HasPrefix(p, base+"/"):
			r2 := r.Clone(r.Context())
			u := *r.URL
			u.Path = strings.TrimPrefix(p, base)
			u.RawPath = ""
			if raw := r.URL.RawPath; raw != "" && strings.HasPrefix(raw, base+"/") {
				u.RawPath = strings.TrimPrefix(raw, base)
			}
			r2.URL = &u
			next.ServeHTTP(w, r2)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// cookiePath is the path the sign-in cookie is limited to: the prefix, so
// other apps on the same address do not receive it, or "/" with no prefix.
func (s *Server) cookiePath() string {
	if s.cfg.URLBase != "" {
		return s.cfg.URLBase
	}
	return "/"
}
