package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/rdborg/mediarium/web"
)

// frontendHandler serves the embedded React build, falling back to
// index.html for any path that isn't a real static asset — required for
// client-side routing (search/library/settings/onboarding are all
// client-side routes).
func frontendHandler() http.Handler { return frontendHandlerBase("") }

// frontendHandlerBase is frontendHandler for an app served under a sub-path
// (URL_BASE, "" for the root). The built page, the "Add to home screen" files
// and the Books page are rewritten once, here, so every address in them
// carries the prefix; the built scripts and styles use relative addresses and
// need no change.
func frontendHandlerBase(base string) http.Handler {
	sub, err := fs.Sub(web.DistFS, web.DistDir)
	if err != nil {
		// Should be unreachable: web.DistFS always embeds at least the
		// placeholder dist/index.html checked into the repo.
		panic("web: embedded dist not found: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))
	pages := preparePages(sub, base)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(sub, trimLeadingSlash(r.URL.Path)); err != nil {
			if r.URL.Path == "/bookshelf" || strings.HasPrefix(r.URL.Path, "/bookshelf/") {
				// Mediarium Books installs as an app of its own: the same
				// page, with its own name and web app manifest.
				if pages.books != nil {
					w.Header().Set("Cache-Control", "no-cache")
					pages.books.serve(w, r)
					return
				}
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		if f := pages.forPath(r.URL.Path); f != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			w.Header().Set("Cache-Control", "no-cache")
			f.serve(w, r)
			return
		}
		// Built assets have content-hashed names, so they can be kept
		// forever; the page that points at them must be re-checked every
		// time so an update is picked up straight away instead of the
		// browser running last version's JavaScript.
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if strings.HasSuffix(r.URL.Path, ".webmanifest") {
			// What browsers expect for "Add to home screen".
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		if serveCompressed(w, r, sub) {
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// compressedAsset is a built file, compressed once and kept: the whole build
// is a couple of megabytes, and compressing it again for every visitor would
// be wasted work.
type compressedAsset struct {
	data        []byte
	etag        string
	contentType string
}

var (
	compressedAssets   sync.Map // file name -> *compressedAsset
	compressibleSuffix = map[string]bool{".js": true, ".css": true, ".html": true, ".svg": true, ".json": true, ".webmanifest": true, ".ttf": true, ".otf": true, ".txt": true, ".xml": true}
)

// serveCompressed answers a browser that accepts gzip with the compressed
// copy of a built text, script, style or font file (the main script is about a
// megabyte on its own). It reports false when it did not answer, so the plain
// file server takes over (a browser without gzip, a Range request, an image).
func serveCompressed(w http.ResponseWriter, r *http.Request, sub fs.FS) bool {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || r.Header.Get("Range") != "" || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
		return false
	}
	name := trimLeadingSlash(r.URL.Path)
	if name == "." {
		name = "index.html"
	}
	if !compressibleSuffix[strings.ToLower(path.Ext(name))] {
		return false
	}
	var asset *compressedAsset
	if v, ok := compressedAssets.Load(name); ok {
		asset = v.(*compressedAsset)
	} else {
		raw, err := fs.ReadFile(sub, name)
		if err != nil {
			return false
		}
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return false
		}
		if _, err := zw.Write(raw); err != nil || zw.Close() != nil {
			return false
		}
		sum := sha256.Sum256(raw)
		ct := mime.TypeByExtension(path.Ext(name))
		if strings.HasSuffix(name, ".webmanifest") {
			ct = "application/manifest+json"
		}
		if ct == "" {
			ct = http.DetectContentType(raw)
		}
		asset = &compressedAsset{data: buf.Bytes(), etag: `"` + hex.EncodeToString(sum[:16]) + `-gz"`, contentType: ct}
		compressedAssets.Store(name, asset)
	}
	h := w.Header()
	h.Set("ETag", asset.etag)
	h.Set("Vary", "Accept-Encoding")
	if etagMatches(r.Header.Get("If-None-Match"), asset.etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	h.Set("Content-Type", asset.contentType)
	h.Set("Content-Encoding", "gzip")
	h.Set("Content-Length", strconv.Itoa(len(asset.data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(asset.data)
	}
	return true
}

func trimLeadingSlash(p string) string {
	if p == "" || p == "/" {
		return "."
	}
	if p[0] == '/' {
		return p[1:]
	}
	return p
}

// bookshelfPage is index.html for Mediarium Books: its own web app manifest
// and title, so a browser offers to install it as a separate app.
func bookshelfPage(page []byte) []byte {
	out := manifestLink.ReplaceAll(page, []byte(`href="${1}bookshelf.webmanifest"`))
	return titleTag.ReplaceAll(out, []byte("<title>Mediarium Books</title>"))
}

// manifestLink is the page's link to its web app manifest, written with an
// absolute (/) or relative (./) address.
var manifestLink = regexp.MustCompile(`href="(\.?/)manifest\.webmanifest"`)

var titleTag = regexp.MustCompile(`<title>[^<]*</title>`)
