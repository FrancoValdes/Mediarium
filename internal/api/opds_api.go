package api

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/books"
	"github.com/rdborg/mediarium/internal/opds"
)

// The OPDS catalogue: the library's ebooks as a feed that e-reader apps
// (KOReader, Moon+ Reader, Librera, Marvin, Foliate, ...) read to browse,
// search and download. It is read-only. OPDS 1.2 (Atom) lives at /opds/ and
// OPDS 2.0 (JSON) at /opds/v2/. The caller signs in with HTTP Basic: the
// account's user name and its e-reader password (ereader_auth.go).

const (
	opdsPageSize = 50
	opdsNavPage  = 100
	opdsSummary  = 2000
)

// opdsView is the catalogue as one caller asks for it: Atom or JSON.
type opdsView struct {
	root string // "/opds" or "/opds/v2"
	json bool
}

func (v opdsView) href(p string) string { return v.root + p }

func (v opdsView) navType() string {
	if v.json {
		return opds.TypeFeedJSON
	}
	return opds.TypeNavigation
}

func (v opdsView) acqType() string {
	if v.json {
		return opds.TypeFeedJSON
	}
	return opds.TypeAcquisition
}

func (s *Server) opdsRoutes(root *http.ServeMux) {
	root.Handle("/opds/", noStore(http.HandlerFunc(s.handleOPDS)))
	root.Handle("/opds", http.RedirectHandler(s.cfg.URLBase+"/opds/", http.StatusMovedPermanently))
}

// handleOPDS answers every address under /opds/.
func (s *Server) handleOPDS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "The catalogue is read-only.", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.opdsUser(w, r)
	if !ok {
		return
	}
	_ = user // every account sees the whole library; the sign-in decides who may come in
	v := opdsView{root: s.cfg.URLBase + "/opds"}
	rest := strings.TrimPrefix(r.URL.Path, "/opds/")
	if rest == "v2" || strings.HasPrefix(rest, "v2/") {
		v = opdsView{root: s.cfg.URLBase + "/opds/v2", json: true}
		rest = strings.TrimPrefix(strings.TrimPrefix(rest, "v2"), "/")
	}
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")

	switch {
	case rest == "":
		s.opdsRootFeed(w, r, v)
	case rest == "new":
		s.opdsBooks(w, r, v, "/new", "Recently added", "new", func(a, b books.Book) bool { return a.AddedAt > b.AddedAt || (a.AddedAt == b.AddedAt && a.ID > b.ID) }, nil)
	case rest == "books":
		s.opdsBooks(w, r, v, "/books", "All books", "books", byTitle, nil)
	case rest == "authors":
		s.opdsGroups(w, r, v, "/authors", "Authors", "authors", "/author", func(b books.Book) string { return b.Author })
	case rest == "series":
		s.opdsGroups(w, r, v, "/series", "Series", "series", "/series-books", func(b books.Book) string { return b.SeriesName })
	case rest == "author":
		name := r.URL.Query().Get("name")
		s.opdsBooks(w, r, v, "/author", name, "author:"+name, byTitle, func(b books.Book) bool { return sameGroup(b.Author, name) }, "name", name)
	case rest == "series-books":
		name := r.URL.Query().Get("name")
		s.opdsBooks(w, r, v, "/series-books", name, "series:"+name, bySeriesPos, func(b books.Book) bool { return sameGroup(b.SeriesName, name) }, "name", name)
	case rest == "search":
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		s.opdsBooks(w, r, v, "/search", "Search: "+q, "search:"+q, byTitle, searchMatch(q), "q", q)
	case rest == "opensearch.xml" && !v.json:
		s.opdsSearchDescription(w, v)
	case len(parts) == 3 && parts[0] == "books":
		s.opdsBookFile(w, r, parts[1], parts[2])
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) opdsSearchDescription(w http.ResponseWriter, v opdsView) {
	w.Header().Set("Content-Type", opds.TypeSearch+"; charset=utf-8")
	_, _ = w.Write(opds.SearchDescription("Mediarium books", "Search the ebooks in Mediarium", v.href("/search")+"?q={searchTerms}"))
}

// ---- Feeds ----

func byTitle(a, b books.Book) bool {
	ta, tb := strings.ToLower(a.Title), strings.ToLower(b.Title)
	if ta != tb {
		return ta < tb
	}
	return a.ID < b.ID
}

func bySeriesPos(a, b books.Book) bool {
	pa, _ := strconv.ParseFloat(strings.TrimSpace(a.SeriesPosition), 64)
	pb, _ := strconv.ParseFloat(strings.TrimSpace(b.SeriesPosition), 64)
	if pa != pb {
		return pa < pb
	}
	return byTitle(a, b)
}

func groupKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func sameGroup(a, name string) bool { return name != "" && groupKey(a) == groupKey(name) }

func searchMatch(q string) func(books.Book) bool {
	terms := strings.Fields(strings.ToLower(q))
	return func(b books.Book) bool {
		if len(terms) == 0 {
			return false
		}
		hay := strings.ToLower(b.Title + " " + b.Author + " " + b.SeriesName)
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				return false
			}
		}
		return true
	}
}

func parseAdded(b books.Book) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, b.AddedAt); err == nil {
		return t
	}
	return time.Unix(0, 0).UTC()
}

func (v opdsView) startAndSearch(self opds.Link) []opds.Link {
	links := []opds.Link{self, {Rel: opds.RelStart, Href: v.href("/"), Type: v.navType()}}
	if v.json {
		links = append(links, opds.Link{Rel: opds.RelSearch, Href: v.href("/search") + "{?query}", Type: opds.TypeFeedJSON, Templated: true})
		return links
	}
	links = append(links,
		opds.Link{Rel: opds.RelSearch, Href: v.href("/opensearch.xml"), Type: opds.TypeSearch, Title: "Search"},
		opds.Link{Rel: opds.RelSearch, Href: v.href("/search") + "?q={searchTerms}", Type: opds.TypeAcquisition, Title: "Search"})
	return links
}

func (s *Server) writeFeed(w http.ResponseWriter, r *http.Request, v opdsView, f opds.Feed) {
	if v.json {
		data, err := f.JSON()
		if err != nil {
			slog.Error("opds: write feed", "err", err)
			http.Error(w, "Couldn't make the feed.", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", opds.TypeFeedJSON)
		_, _ = w.Write(data)
		return
	}
	ct := opds.TypeAcquisition
	if f.Kind == opds.Navigation {
		ct = opds.TypeNavigation
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(f.Atom())
}

func (s *Server) opdsRootFeed(w http.ResponseWriter, r *http.Request, v opdsView) {
	list, err := s.ebookBooks()
	if err != nil {
		slog.Error("opds: list books", "err", err)
		http.Error(w, "Couldn't read the library.", http.StatusInternalServerError)
		return
	}
	updated := latestAdded(list)
	nav := func(id, title, content, rel, p string, kind opds.Kind) opds.Entry {
		typ := v.navType()
		if kind == opds.Acquisition {
			typ = v.acqType()
		}
		return opds.Entry{ID: "urn:mediarium:opds:" + id, Title: title, Updated: updated, Content: content,
			Links: []opds.Link{{Rel: rel, Href: v.href(p), Type: typ}}}
	}
	f := opds.Feed{
		ID: "urn:mediarium:opds:root", Title: "Mediarium books", Updated: updated, Kind: opds.Navigation,
		Links: v.startAndSearch(opds.Link{Rel: opds.RelSelf, Href: v.href("/"), Type: v.navType()}),
		Entries: []opds.Entry{
			nav("new", "Recently added", "The newest books in the library.", opds.RelNew, "/new", opds.Acquisition),
			nav("books", "All books", fmt.Sprintf("%d books, by title.", len(list)), opds.RelSubsection, "/books", opds.Acquisition),
			nav("authors", "Authors", "Books by author.", opds.RelSubsection, "/authors", opds.Navigation),
			nav("series", "Series", "Books by series.", opds.RelSubsection, "/series", opds.Navigation),
		},
	}
	s.writeFeed(w, r, v, f)
}

func latestAdded(list []books.Book) time.Time {
	var latest time.Time
	for _, b := range list {
		if t := parseAdded(b); t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		latest = time.Unix(0, 0).UTC()
	}
	return latest
}

// opdsPage is the page a request asks for, kept between the first and the last
// page of a feed with total items. It is clamped before any arithmetic is done
// with it: a huge number in the address must not overflow the offset.
func opdsPage(r *http.Request, total, size int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil && n == 0 {
		return 1 // not a number
	}
	last := max((total+size-1)/size, 1)
	return min(max(n, 1), last)
}

// pageLinks adds first, previous, next and last links for a paged feed.
func (v opdsView) pageLinks(f *opds.Feed, p string, extra url.Values, page, total, size int, typ string) {
	pages := (total + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	at := func(n int) string {
		q := url.Values{}
		for k, vs := range extra {
			q[k] = vs
		}
		if n > 1 {
			q.Set("page", strconv.Itoa(n))
		}
		if len(q) == 0 {
			return v.href(p)
		}
		return v.href(p) + "?" + q.Encode()
	}
	f.Links = append(f.Links, opds.Link{Rel: opds.RelFirst, Href: at(1), Type: typ})
	if page > 1 {
		f.Links = append(f.Links, opds.Link{Rel: opds.RelPrevious, Href: at(page - 1), Type: typ})
	}
	if page < pages {
		f.Links = append(f.Links, opds.Link{Rel: opds.RelNext, Href: at(page + 1), Type: typ})
	}
	f.Links = append(f.Links, opds.Link{Rel: opds.RelLast, Href: at(pages), Type: typ})
}

// opdsBooks writes a paged feed of books: those keep passing filter (all of
// them when it is nil), in the given order. query is key, value pairs the
// feed's own address keeps.
func (s *Server) opdsBooks(w http.ResponseWriter, r *http.Request, v opdsView, p, title, id string,
	less func(a, b books.Book) bool, keep func(books.Book) bool, query ...string) {
	list, err := s.ebookBooks()
	if err != nil {
		slog.Error("opds: list books", "err", err)
		http.Error(w, "Couldn't read the library.", http.StatusInternalServerError)
		return
	}
	var picked []books.Book
	for _, b := range list {
		if keep == nil || keep(b) {
			picked = append(picked, b)
		}
	}
	sort.SliceStable(picked, func(i, j int) bool { return less(picked[i], picked[j]) })
	extra := url.Values{}
	for i := 0; i+1 < len(query); i += 2 {
		extra.Set(query[i], query[i+1])
	}
	total := len(picked)
	page := opdsPage(r, total, opdsPageSize)
	start := (page - 1) * opdsPageSize
	end := min(start+opdsPageSize, total)
	self := v.href(p)
	selfQ := url.Values{}
	for k, vs := range extra {
		selfQ[k] = vs
	}
	if page > 1 {
		selfQ.Set("page", strconv.Itoa(page))
	}
	if len(selfQ) > 0 {
		self += "?" + selfQ.Encode()
	}
	f := opds.Feed{
		ID: "urn:mediarium:opds:" + id, Title: title, Updated: latestAdded(picked), Kind: opds.Acquisition,
		Links:        v.startAndSearch(opds.Link{Rel: opds.RelSelf, Href: self, Type: v.acqType()}),
		TotalResults: total, ItemsPerPage: opdsPageSize, StartIndex: start + 1, Page: page,
	}
	f.Links = append(f.Links, opds.Link{Rel: opds.RelUp, Href: v.href("/"), Type: v.navType()})
	v.pageLinks(&f, p, extra, page, total, opdsPageSize, v.acqType())
	for _, b := range picked[start:end] {
		f.Entries = append(f.Entries, s.opdsBookEntry(v, b))
	}
	s.writeFeed(w, r, v, f)
}

// opdsGroups writes a navigation feed of the authors or the series, each
// linking to the feed of its books.
func (s *Server) opdsGroups(w http.ResponseWriter, r *http.Request, v opdsView, p, title, id, bookPath string, name func(books.Book) string) {
	list, err := s.ebookBooks()
	if err != nil {
		slog.Error("opds: list books", "err", err)
		http.Error(w, "Couldn't read the library.", http.StatusInternalServerError)
		return
	}
	type group struct {
		name    string
		count   int
		updated time.Time
	}
	groups := map[string]*group{}
	for _, b := range list {
		n := strings.TrimSpace(name(b))
		if n == "" {
			continue
		}
		g := groups[groupKey(n)]
		if g == nil {
			g = &group{name: n}
			groups[groupKey(n)] = g
		}
		g.count++
		if t := parseAdded(b); t.After(g.updated) {
			g.updated = t
		}
	}
	sorted := make([]*group, 0, len(groups))
	for _, g := range groups {
		sorted = append(sorted, g)
	}
	sort.Slice(sorted, func(i, j int) bool { return groupKey(sorted[i].name) < groupKey(sorted[j].name) })
	total := len(sorted)
	page := opdsPage(r, total, opdsNavPage)
	start := (page - 1) * opdsNavPage
	end := min(start+opdsNavPage, total)
	self := v.href(p)
	if page > 1 {
		self += "?page=" + strconv.Itoa(page)
	}
	f := opds.Feed{
		ID: "urn:mediarium:opds:" + id, Title: title, Updated: latestAdded(list), Kind: opds.Navigation,
		Links:        v.startAndSearch(opds.Link{Rel: opds.RelSelf, Href: self, Type: v.navType()}),
		TotalResults: total, ItemsPerPage: opdsNavPage, StartIndex: start + 1, Page: page,
	}
	f.Links = append(f.Links, opds.Link{Rel: opds.RelUp, Href: v.href("/"), Type: v.navType()})
	v.pageLinks(&f, p, nil, page, total, opdsNavPage, v.navType())
	for _, g := range sorted[start:end] {
		count := fmt.Sprintf("%d books", g.count)
		if g.count == 1 {
			count = "1 book"
		}
		f.Entries = append(f.Entries, opds.Entry{
			ID: "urn:mediarium:opds:" + id + ":" + groupKey(g.name), Title: g.name, Updated: g.updated, Content: count,
			Links: []opds.Link{{Rel: opds.RelSubsection, Href: v.href(bookPath) + "?name=" + url.QueryEscape(g.name), Type: v.acqType()}},
		})
	}
	s.writeFeed(w, r, v, f)
}

var opdsFileTypes = map[string]string{
	"epub": "application/epub+zip",
	"pdf":  "application/pdf",
	"mobi": "application/x-mobipocket-ebook",
	"azw":  "application/vnd.amazon.ebook",
	"azw3": "application/vnd.amazon.ebook",
	"prc":  "application/x-mobipocket-ebook",
	"cbz":  "application/vnd.comicbook+zip",
	"cbr":  "application/vnd.comicbook-rar",
	"cb7":  "application/x-cb7",
}

func (s *Server) opdsBookEntry(v opdsView, b books.Book) opds.Entry {
	id := strconv.FormatInt(b.ID, 10)
	summary := strings.TrimSpace(b.Description)
	if r := []rune(summary); len(r) > opdsSummary {
		summary = string(r[:opdsSummary]) + "..."
	}
	e := opds.Entry{
		ID: "urn:mediarium:book:" + id, Title: b.Title, Updated: parseAdded(b), Summary: summary, Language: "",
		Series: b.SeriesName, SeriesPos: b.SeriesPosition,
	}
	if b.Author != "" {
		e.Authors = []string{b.Author}
	}
	if b.Year > 0 {
		e.Issued = strconv.Itoa(b.Year)
	}
	ct := opdsFileTypes[ebookExt(b)]
	if ct == "" {
		ct = "application/octet-stream"
	}
	e.Links = []opds.Link{
		{Rel: opds.RelAcquisition, Href: s.cfg.URLBase + "/opds/books/" + id + "/download", Type: ct},
		{Rel: opds.RelImage, Href: s.cfg.URLBase + "/opds/books/" + id + "/cover", Type: "image/jpeg"},
		{Rel: opds.RelThumbnail, Href: s.cfg.URLBase + "/opds/books/" + id + "/thumbnail", Type: "image/jpeg"},
	}
	return e
}

// ---- Files and covers ----

func (s *Server) opdsBookFile(w http.ResponseWriter, r *http.Request, idText, what string) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	b, err := s.BookRepo.Get(id)
	if err != nil || !hasEbook(b) {
		http.NotFound(w, r)
		return
	}
	switch what {
	case "download":
		s.serveEbookDownload(w, r, b)
	case "cover":
		s.serveEReaderCover(w, r, b, 800, 1200)
	case "thumbnail":
		s.serveEReaderCover(w, r, b, 200, 300)
	default:
		http.NotFound(w, r)
	}
}

// serveEbookDownload sends the book's own file.
func (s *Server) serveEbookDownload(w http.ResponseWriter, r *http.Request, b books.Book) {
	file, info, _, err := s.openEbook(b)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "Couldn't find the file. It may have been moved or deleted.", http.StatusNotFound)
			return
		}
		slog.Info("opds: open book", "book", b.ID, "err", err)
		http.Error(w, "Couldn't read the file.", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	ext := ebookExt(b)
	ct := opdsFileTypes[ext]
	if ct == "" {
		ct = "application/octet-stream"
	}
	name := path.Base(b.EbookPath)
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeContent(w, r, name, info.ModTime(), file)
}

// serveEReaderCover sends the book's cover as a JPEG no bigger than w by h.
func (s *Server) serveEReaderCover(w http.ResponseWriter, r *http.Request, b books.Book, wantW, wantH int) {
	data, err := s.ebookCover(r.Context(), b, wantW, wantH)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
