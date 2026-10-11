package api

import (
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/web"
)

// After an update the browser must load the new interface, not the old one it
// kept: the page that points at the scripts is always re-checked, and the
// scripts have names that change with their content, so they can be kept for
// ever.
func TestInterfaceIsNotServedStaleAfterAnUpdate(t *testing.T) {
	h := frontendHandler()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	for _, path := range []string{"/", "/settings/system", "/library"} {
		w := get(path)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("GET %s: %d, Cache-Control %q; want 200 and no-cache", path, w.Code, w.Header().Get("Cache-Control"))
		}
	}

	sub, err := fs.Sub(web.DistFS, web.DistDir)
	if err != nil {
		t.Fatal(err)
	}
	assets, _ := fs.ReadDir(sub, "assets")
	if len(assets) == 0 {
		t.Skip("the interface has not been built here (web/dist/assets is empty)")
	}
	for _, a := range assets {
		w := get("/assets/" + a.Name())
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Errorf("GET /assets/%s: %d, Cache-Control %q", a.Name(), w.Code, w.Header().Get("Cache-Control"))
		}
		if !hashedName(a.Name()) {
			t.Errorf("asset %s has no content hash in its name, so an old copy could be kept after an update", a.Name())
		}
	}
}

// hashedName reports whether a built file name carries a content hash
// (index-DGfyRCUI.js), which Vite adds to everything it builds.
func hashedName(name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] == '-' {
			rest := name[i+1:]
			n := 0
			for n < len(rest) && rest[n] != '.' {
				n++
			}
			if n >= 6 {
				return true
			}
		}
	}
	return false
}

// A browser that accepts gzip gets the built scripts, styles and the page
// compressed (the main script is about a megabyte); one that does not, or that
// asks for a range, gets the plain file.
func TestBuiltFilesAreSentCompressed(t *testing.T) {
	h := frontendHandler()
	do := func(path string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	gz := map[string]string{"Accept-Encoding": "gzip, deflate"}
	for _, path := range []string{"/", "/library", "/favicon.svg"} {
		w := do(path, gz)
		if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("GET %s: %d, Content-Encoding %q, Vary %q", path, w.Code, w.Header().Get("Content-Encoding"), w.Header().Get("Vary"))
		}
		zr, err := gzip.NewReader(w.Body)
		if err != nil {
			t.Fatalf("GET %s: not gzip: %v", path, err)
		}
		plain, _ := io.ReadAll(zr)
		if len(plain) == 0 {
			t.Errorf("GET %s: empty after decompressing", path)
		}
		if path != "/favicon.svg" && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Errorf("GET %s: Content-Type %q", path, w.Header().Get("Content-Type"))
		}
		// Asking again with the tag gets "not modified".
		again := do(path, map[string]string{"Accept-Encoding": "gzip", "If-None-Match": w.Header().Get("ETag")})
		if again.Code != http.StatusNotModified {
			t.Errorf("GET %s with its ETag: %d, want 304", path, again.Code)
		}
	}
	if w := do("/", nil); w.Header().Get("Content-Encoding") != "" {
		t.Errorf("without Accept-Encoding the page must be plain, got %q", w.Header().Get("Content-Encoding"))
	}
	if w := do("/", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-10"}); w.Header().Get("Content-Encoding") != "" {
		t.Errorf("a range request must get the plain file, got %q", w.Header().Get("Content-Encoding"))
	}
}
