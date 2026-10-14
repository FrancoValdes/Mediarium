package httpsec

import (
	"net/http"
	"strings"
)

// contentSecurityPolicy fits the single-page app: everything comes from
// Mediarium itself except poster and cover images (TMDB, the Cover Art
// Archive and the Internet Archive it redirects to) and the YouTube player
// for the trailer a person chose to play behind a title page. Inline styles are
// allowed because React sets style attributes; scripts are never inline.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	// blob: styles and fonts are an EPUB's own, unpacked by the reader.
	"style-src 'self' 'unsafe-inline' blob:; " +
	"img-src 'self' data: blob: https://image.tmdb.org https://coverartarchive.org https://archive.org https://*.archive.org https://covers.openlibrary.org; " +
	"font-src 'self' blob:; " +
	"media-src 'self' blob:; " +
	"connect-src 'self'; " +
	// The optional trailer behind a title page; nothing else may be framed.
	"frame-src https://www.youtube-nocookie.com; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// hstsValue is six months. No includeSubDomains or preload: those affect
// sites that share the domain, which is the owner's call, not ours.
const hstsValue = "max-age=15552000"

// Headers sets the security response headers on every response.
// Strict-Transport-Security is added only when the caller is on HTTPS
// (directly or as reported by a trusted proxy): sent over plain HTTP it
// would be ignored anyway, and a LAN visit over http:// must keep working.
func (p *Proxies) Headers(next http.Handler) http.Handler {
	return p.HeadersWithFrames(next, nil)
}

// youtubeFrame is the one place Mediarium frames by default.
const youtubeFrame = "frame-src https://www.youtube-nocookie.com;"

// HeadersWithFrames is Headers, with the origins frames() names (the add-ons
// a person set to open inside Mediarium) allowed in a frame next to the
// trailer player. frames is asked on every request, so a change in the
// settings applies at once; nil allows none.
func (p *Proxies) HeadersWithFrames(next http.Handler, frames func() []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		policy := contentSecurityPolicy
		if frames != nil {
			if extra := frames(); len(extra) > 0 {
				policy = strings.Replace(policy, youtubeFrame, "frame-src https://www.youtube-nocookie.com "+strings.Join(extra, " ")+";", 1)
			}
		}
		h.Set("Content-Security-Policy", policy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		// A media manager has no business in a search engine's index.
		h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		if p.IsHTTPS(r) {
			h.Set("Strict-Transport-Security", hstsValue)
		}
		next.ServeHTTP(w, r)
	})
}
