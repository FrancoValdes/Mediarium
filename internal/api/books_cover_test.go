package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// tinyJPEG is the start of a real JPEG file, enough for the type check.
var tinyJPEG = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00}, make([]byte, 64)...)

// A book cover is fetched from Open Library once and then served from a copy
// kept on this server, with a long browser cache, so pages of books don't wait
// on Open Library again.
func TestBookCoverIsFetchedOnceAndKept(t *testing.T) {
	var hits atomic.Int32
	ol := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/b/id/123-M.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(tinyJPEG)
		case "/b/id/124-M.jpg":
			http.NotFound(w, r)
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>not a picture</html>"))
		}
	}))
	defer ol.Close()

	server, base, client := loginNewServer(t)
	server.TestSetOpenLibrary(ol.URL)
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/modules", map[string]any{"ebooks": true}, http.StatusOK)

	get := func(path string) (*http.Response, []byte) {
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, body
	}

	resp, body := get("/api/book-covers/123-M.jpg")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" || len(body) != len(tinyJPEG) {
		t.Fatalf("first request: %d %q %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age=") || !strings.Contains(cc, "immutable") {
		t.Errorf("a cover should be kept by the browser, got Cache-Control %q", cc)
	}
	first := hits.Load()
	if resp, _ := get("/api/book-covers/123-M.jpg"); resp.StatusCode != http.StatusOK {
		t.Fatalf("second request: %d", resp.StatusCode)
	}
	if hits.Load() != first {
		t.Errorf("the second request went to Open Library again (%d hits, %d before)", hits.Load(), first)
	}

	// Missing cover: 404, and remembered, so Open Library isn't asked again.
	if resp, _ := get("/api/book-covers/124-M.jpg"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a cover Open Library doesn't have: %d", resp.StatusCode)
	}
	missing := hits.Load()
	if resp, _ := get("/api/book-covers/124-M.jpg"); resp.StatusCode != http.StatusNotFound || hits.Load() != missing {
		t.Errorf("a missing cover must not be asked for twice (%d hits, %d before)", hits.Load(), missing)
	}

	// Something that isn't a picture is never kept or sent.
	if resp, _ := get("/api/book-covers/125-M.jpg"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an answer that isn't a picture: %d", resp.StatusCode)
	}
	// Anything that isn't "number-size.jpg" is refused without a request.
	before := hits.Load()
	for _, bad := range []string{"/api/book-covers/abc-M.jpg", "/api/book-covers/123-X.jpg", "/api/book-covers/..%2F..%2Fetc%2Fpasswd", "/api/book-covers/123-M.png"} {
		if resp, _ := get(bad); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", bad, resp.StatusCode)
		}
	}
	if hits.Load() != before {
		t.Errorf("bad cover names reached Open Library")
	}
}
