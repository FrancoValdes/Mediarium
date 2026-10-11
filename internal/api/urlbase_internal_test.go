package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const builtPage = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <title>Mediarium</title>
    <link rel="icon" href="./favicon.svg" type="image/svg+xml" />
    <link rel="apple-touch-icon" href="./apple-touch-icon-180.png" />
    <link rel="manifest" href="./manifest.webmanifest" />
    <script type="module" crossorigin src="./assets/index-tmgxaC7e.js"></script>
    <link rel="modulepreload" crossorigin href="./assets/rolldown-runtime-Dd_uD5pT.js">
    <link rel="stylesheet" crossorigin href="./assets/start-Fhi8k7Xa.css">
  </head>
  <body><div id="root"></div><a href="https://example.com/x">x</a><a href="//cdn.example.com/y">y</a></body>
</html>`

func TestRewritePage(t *testing.T) {
	t.Run("no prefix: addresses become absolute, nothing is added", func(t *testing.T) {
		got := string(rewritePage([]byte(builtPage), ""))
		for _, want := range []string{`href="/favicon.svg"`, `src="/assets/index-tmgxaC7e.js"`, `href="/assets/start-Fhi8k7Xa.css"`, `href="/manifest.webmanifest"`} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %s in\n%s", want, got)
			}
		}
		if strings.Contains(got, "mediarium-base") || strings.Contains(got, `"./`) {
			t.Errorf("unexpected content:\n%s", got)
		}
	})
	t.Run("prefix", func(t *testing.T) {
		got := string(rewritePage([]byte(builtPage), "/mediarium"))
		for _, want := range []string{
			`<meta name="mediarium-base" content="/mediarium" />`,
			`href="/mediarium/favicon.svg"`, `src="/mediarium/assets/index-tmgxaC7e.js"`,
			`href="/mediarium/assets/rolldown-runtime-Dd_uD5pT.js"`, `href="/mediarium/manifest.webmanifest"`,
			`href="https://example.com/x"`, `href="//cdn.example.com/y"`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %s in\n%s", want, got)
			}
		}
		if strings.Index(got, "mediarium-base") > strings.Index(got, "<script") {
			t.Error("the base tag has to come before the scripts")
		}
	})
	t.Run("absolute addresses (an older build) get the prefix too", func(t *testing.T) {
		got := string(rewritePage([]byte(`<head><script src="/assets/a.js"></script></head>`), "/m"))
		if !strings.Contains(got, `src="/m/assets/a.js"`) {
			t.Errorf("got %s", got)
		}
	})
}

func TestBookshelfPageUsesItsOwnManifestForBothAddressStyles(t *testing.T) {
	for _, in := range []string{`<link rel="manifest" href="/manifest.webmanifest" />`, `<link rel="manifest" href="./manifest.webmanifest" />`} {
		page := `<head><title>Mediarium</title>` + in + `</head>`
		got := string(rewritePage(bookshelfPage([]byte(page)), "/mediarium"))
		if !strings.Contains(got, `href="/mediarium/bookshelf.webmanifest"`) || strings.Contains(got, "/manifest.webmanifest") || !strings.Contains(got, "<title>Mediarium Books</title>") {
			t.Errorf("for %s got %s", in, got)
		}
	}
}

func TestRewriteManifest(t *testing.T) {
	raw := []byte(`{"id":"/bookshelf/","name":"Books","start_url":"/bookshelf/","scope":"/bookshelf/","icons":[{"src":"/icon-192.png","sizes":"192x192"},{"src":"https://x.example/i.png"}],"shortcuts":[{"name":"s","url":"/library","icons":[{"src":"/s.png"}]}]}`)
	var m map[string]any
	if err := json.Unmarshal(rewriteManifest(raw, "/mediarium"), &m); err != nil {
		t.Fatal(err)
	}
	if m["id"] != "/mediarium/bookshelf/" || m["start_url"] != "/mediarium/bookshelf/" || m["scope"] != "/mediarium/bookshelf/" {
		t.Errorf("start/scope/id: %v", m)
	}
	icons := m["icons"].([]any)
	if icons[0].(map[string]any)["src"] != "/mediarium/icon-192.png" || icons[1].(map[string]any)["src"] != "https://x.example/i.png" {
		t.Errorf("icons: %v", icons)
	}
	sc := m["shortcuts"].([]any)[0].(map[string]any)
	if sc["url"] != "/mediarium/library" || sc["icons"].([]any)[0].(map[string]any)["src"] != "/mediarium/s.png" {
		t.Errorf("shortcuts: %v", sc)
	}
	if got := rewriteManifest([]byte("not json"), "/mediarium"); string(got) != "not json" {
		t.Errorf("a file that is not JSON must come back as it was, got %q", got)
	}
}

func TestWithURLBaseStripsThePrefix(t *testing.T) {
	var seenPath, seenRaw, seenQuery string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath, seenRaw, seenQuery = r.URL.Path, r.URL.RawPath, r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	})
	h := withURLBase("/mediarium", inner)
	tests := []struct {
		target, wantPath, wantRaw, wantLocation string
		wantCode                                int
	}{
		{"/mediarium/", "/", "", "", 200},
		{"/mediarium/api/version", "/api/version", "", "", 200},
		{"/mediarium/radarr/api/v3/movie?x=1", "/radarr/api/v3/movie", "", "", 200},
		{"/mediarium/title/a%2Fb", "/title/a/b", "/title/a%2Fb", "", 200},
		{"/mediarium", "", "", "/mediarium/", 302},
		{"/mediarium?a=b", "", "", "/mediarium/?a=b", 302},
		// Not the prefix: handled as they are (a proxy that removed it, or another path).
		{"/api/version", "/api/version", "", "", 200},
		{"/mediarium2/x", "/mediarium2/x", "", "", 200},
		{"/", "/", "", "", 200},
	}
	for _, tc := range tests {
		seenPath, seenRaw, seenQuery = "", "", ""
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.wantCode {
			t.Errorf("%s: status %d, want %d", tc.target, rec.Code, tc.wantCode)
		}
		if tc.wantLocation != "" && rec.Header().Get("Location") != tc.wantLocation {
			t.Errorf("%s: Location %q, want %q", tc.target, rec.Header().Get("Location"), tc.wantLocation)
		}
		if tc.wantCode == 200 {
			if seenPath != tc.wantPath || seenRaw != tc.wantRaw {
				t.Errorf("%s: handler saw path %q raw %q, want %q %q", tc.target, seenPath, seenRaw, tc.wantPath, tc.wantRaw)
			}
			if strings.Contains(tc.target, "?x=1") && seenQuery != "x=1" {
				t.Errorf("%s: query lost: %q", tc.target, seenQuery)
			}
		}
	}
	// No prefix: the very same handler comes back.
	if got := withURLBase("", inner); http.Handler(got) == nil {
		t.Fatal("expected a handler")
	}
}

func TestFrontendHandlerServesPreparedPages(t *testing.T) {
	h := frontendHandlerBase("/mediarium")
	get := func(target string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, path := range []string{"/", "/library", "/settings/system", "/title/603"} {
		rec := get(path, nil)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(rec.Body.String(), `<meta name="mediarium-base" content="/mediarium" />`) {
			t.Errorf("GET %s: %d %q %.120q", path, rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
		}
	}
	if rec := get("/bookshelf/read/4", nil); !strings.Contains(rec.Body.String(), "Mediarium Books") {
		t.Errorf("the Books page is missing: %.200q", rec.Body.String())
	}
	// The same page, with no prefix, has no base tag.
	rec := httptest.NewRecorder()
	frontendHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/library", nil))
	if strings.Contains(rec.Body.String(), "mediarium-base") {
		t.Error("no prefix, no base tag")
	}
	// A conditional request gets 304 with the right tag, per encoding.
	first := get("/", map[string]string{"Accept-Encoding": "gzip"})
	if first.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("expected gzip")
	}
	if again := get("/", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": first.Header().Get("ETag")}); again.Code != http.StatusNotModified {
		t.Errorf("conditional get: %d", again.Code)
	}
	plain := get("/", nil)
	if plain.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Error("plain and gzip copies need different tags")
	}
}
