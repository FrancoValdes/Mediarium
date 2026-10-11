package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// The built web app is served under a prefix (URL_BASE) by rewriting the few
// files that hold addresses: the page itself, the Books page and the "Add to
// home screen" manifests. They are rewritten once, when the handler is made,
// and kept. Everything else in the build uses addresses relative to itself.

// preparedFile is one rewritten file, kept plain and gzipped.
type preparedFile struct {
	plain, gz   []byte
	etag        string
	contentType string
}

func newPreparedFile(raw []byte, contentType string) *preparedFile {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	sum := sha256.Sum256(raw)
	return &preparedFile{plain: raw, gz: buf.Bytes(), etag: `"` + hex.EncodeToString(sum[:16]), contentType: contentType}
}

// serve answers a GET or HEAD with the file, compressed when the browser can
// read it, and "not modified" when it already has this copy.
func (f *preparedFile) serve(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Vary", "Accept-Encoding")
	h.Set("Content-Type", f.contentType)
	body, etag := f.plain, f.etag+`"`
	if r.Header.Get("Range") == "" && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		body, etag = f.gz, f.etag+`-gz"`
		h.Set("Content-Encoding", "gzip")
	}
	h.Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		h.Del("Content-Encoding")
		h.Del("Content-Type")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// pageSet is the rewritten files of one build for one prefix.
type pageSet struct {
	index     *preparedFile            // the app's page, served for "/" and every page address
	books     *preparedFile            // the same page for Mediarium Books
	manifests map[string]*preparedFile // "manifest.webmanifest" and friends, only under a prefix
}

// forPath is the rewritten file for a request path, or nil when the path is an
// ordinary file.
func (p pageSet) forPath(path string) *preparedFile {
	if path == "/" {
		return p.index
	}
	return p.manifests[strings.TrimPrefix(path, "/")]
}

func preparePages(sub fs.FS, base string) pageSet {
	var set pageSet
	if page, err := fs.ReadFile(sub, "index.html"); err == nil {
		set.index = newPreparedFile(rewritePage(page, base), "text/html; charset=utf-8")
		set.books = newPreparedFile(rewritePage(bookshelfPage(page), base), "text/html; charset=utf-8")
	}
	if base != "" {
		set.manifests = map[string]*preparedFile{}
		entries, _ := fs.ReadDir(sub, ".")
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".webmanifest") {
				continue
			}
			raw, err := fs.ReadFile(sub, e.Name())
			if err != nil {
				continue
			}
			set.manifests[e.Name()] = newPreparedFile(rewriteManifest(raw, base), "application/manifest+json")
		}
	}
	return set
}

// pageAddress is a src or href that points at a file of the app, written
// absolute (/assets/x) or relative to the page (./assets/x). Addresses with a
// scheme or two slashes are somebody else's and are left alone.
var pageAddress = regexp.MustCompile(`\b(src|href)="\.?/([^/"])`)

// rewritePage puts the prefix in front of every address of the app in the
// page and, under a prefix, adds the tag the scripts read it from (a page may
// not carry inline scripts, so it is a meta tag). With no prefix the
// addresses simply become absolute, which is what the page always had.
func rewritePage(page []byte, base string) []byte {
	out := pageAddress.ReplaceAll(page, []byte(`${1}="`+base+`/${2}`))
	if base != "" {
		tag := []byte(`<meta name="mediarium-base" content="` + html.EscapeString(base) + `" />`)
		if loc := headOpen.FindIndex(out); loc != nil {
			out = append(out[:loc[1]:loc[1]], append(tag, out[loc[1]:]...)...)
		}
	}
	return out
}

var headOpen = regexp.MustCompile(`(?i)<head[^>]*>`)

// rewriteManifest puts the prefix in front of the addresses in a web app
// manifest (where the app starts, its scope and its icons). A file that is not
// the JSON it should be is served as it is.
func rewriteManifest(raw []byte, base string) []byte {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	prefix := func(v any) any {
		if s, ok := v.(string); ok && strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") {
			return base + s
		}
		return v
	}
	for _, key := range []string{"start_url", "scope", "id"} {
		if v, ok := m[key]; ok {
			m[key] = prefix(v)
		}
	}
	for _, list := range []string{"icons", "shortcuts"} {
		items, _ := m[list].([]any)
		for _, it := range items {
			obj, _ := it.(map[string]any)
			if v, ok := obj["src"]; ok {
				obj["src"] = prefix(v)
			}
			if v, ok := obj["url"]; ok {
				obj["url"] = prefix(v)
			}
			icons, _ := obj["icons"].([]any)
			for _, ic := range icons {
				if iobj, ok := ic.(map[string]any); ok {
					iobj["src"] = prefix(iobj["src"])
				}
			}
		}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return raw
	}
	return out
}
