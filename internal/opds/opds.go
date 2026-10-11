// Package opds writes OPDS catalogues: the feeds e-reader apps such as
// KOReader, Moon+ Reader, Librera, Marvin and Foliate read to browse and
// download books. It writes OPDS 1.2 (an Atom feed, https://specs.opds.io/opds-1.2)
// and OPDS 2.0 (JSON, https://specs.opds.io/opds-2.0) from one description of
// a feed, and the OpenSearch document that tells an app how to search.
//
// It knows nothing about Mediarium's library: the caller builds a Feed.
package opds

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"time"
)

// Media types.
const (
	TypeNavigation  = "application/atom+xml;profile=opds-catalog;kind=navigation"
	TypeAcquisition = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	TypeEntry       = "application/atom+xml;type=entry;profile=opds-catalog"
	TypeSearch      = "application/opensearchdescription+xml"
	TypeFeedJSON    = "application/opds+json"
)

// Link relations.
const (
	RelSelf        = "self"
	RelStart       = "start"
	RelUp          = "up"
	RelNext        = "next"
	RelPrevious    = "previous"
	RelFirst       = "first"
	RelLast        = "last"
	RelSearch      = "search"
	RelSubsection  = "subsection"
	RelNew         = "http://opds-spec.org/sort/new"
	RelAcquisition = "http://opds-spec.org/acquisition"
	RelOpenAccess  = "http://opds-spec.org/acquisition/open-access"
	RelImage       = "http://opds-spec.org/image"
	RelThumbnail   = "http://opds-spec.org/image/thumbnail"
	RelCrawlable   = "http://opds-spec.org/crawlable"
)

// Kind is what a feed lists.
type Kind int

const (
	// Navigation feeds list other feeds.
	Navigation Kind = iota
	// Acquisition feeds list books.
	Acquisition
)

// Link is one link of a feed or an entry.
type Link struct {
	Rel    string
	Href   string
	Type   string
	Title  string
	Length int64 // size in bytes, 0 = not known
	// Templated marks an OPDS 2.0 link whose Href is a URI template.
	Templated bool
}

// Entry is one line of a feed: a book in an acquisition feed, a link to
// another feed in a navigation feed.
type Entry struct {
	ID       string
	Title    string
	Updated  time.Time
	Authors  []string
	Summary  string // plain text
	Content  string // navigation entries: what the feed holds
	Issued   string // the year or date the book first came out
	Language string
	// Identifier is a stable name for the book, such as "urn:isbn:...".
	Identifier string
	Series     string
	SeriesPos  string
	Links      []Link
}

// Feed is a whole page of the catalogue.
type Feed struct {
	ID      string
	Title   string
	Updated time.Time
	Kind    Kind
	Links   []Link
	Entries []Entry
	// The paging numbers of the whole list (0 = not paged).
	TotalResults int
	ItemsPerPage int
	StartIndex   int // 1-based index of the first entry
	Page         int // 1-based page number
}

const (
	nsAtom       = "http://www.w3.org/2005/Atom"
	nsDC         = "http://purl.org/dc/terms/"
	nsOPDS       = "http://opds-spec.org/2010/catalog"
	nsOpenSearch = "http://a9.com/-/spec/opensearch/1.1/"
)

// xmlWriter writes indented XML by hand: the OPDS prefixes (dc:, opds:,
// opensearch:) are simpler to get right that way than through encoding/xml.
type xmlWriter struct {
	buf   bytes.Buffer
	depth int
}

func (w *xmlWriter) indent() {
	for i := 0; i < w.depth; i++ {
		w.buf.WriteString("  ")
	}
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(stripControl(s)))
	return b.String()
}

// stripControl removes the characters XML 1.0 cannot carry at all.
func stripControl(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			out = append(out, r)
		case r < 0x20 || r == 0xFFFE || r == 0xFFFF || (r >= 0xD800 && r <= 0xDFFF):
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

func (w *xmlWriter) line(format string, args ...any) {
	w.indent()
	fmt.Fprintf(&w.buf, format, args...)
	w.buf.WriteByte('\n')
}

func (w *xmlWriter) text(tag, value string) {
	w.line("<%s>%s</%s>", tag, esc(value), tag)
}

func (w *xmlWriter) textType(tag, typ, value string) {
	w.line(`<%s type="%s">%s</%s>`, tag, typ, esc(value), tag)
}

func (w *xmlWriter) link(l Link, withLength bool) {
	attrs := ""
	if l.Rel != "" {
		attrs += ` rel="` + esc(l.Rel) + `"`
	}
	attrs += ` href="` + esc(l.Href) + `"`
	if l.Type != "" {
		attrs += ` type="` + esc(l.Type) + `"`
	}
	if l.Title != "" {
		attrs += ` title="` + esc(l.Title) + `"`
	}
	if withLength && l.Length > 0 {
		attrs += ` length="` + strconv.FormatInt(l.Length, 10) + `"`
	}
	w.line("<link%s/>", attrs)
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// Atom writes the feed as an OPDS 1.2 Atom document.
func (f Feed) Atom() []byte {
	w := &xmlWriter{}
	w.buf.WriteString(xml.Header)
	w.line(`<feed xmlns="%s" xmlns:dc="%s" xmlns:opds="%s" xmlns:opensearch="%s">`, nsAtom, nsDC, nsOPDS, nsOpenSearch)
	w.depth++
	w.text("id", f.ID)
	w.text("title", f.Title)
	w.text("updated", stamp(f.Updated))
	w.line("<author>")
	w.depth++
	w.text("name", "Mediarium")
	w.depth--
	w.line("</author>")
	for _, l := range f.Links {
		w.link(l, false)
	}
	if f.TotalResults > 0 || f.ItemsPerPage > 0 {
		w.text("opensearch:totalResults", strconv.Itoa(f.TotalResults))
		w.text("opensearch:itemsPerPage", strconv.Itoa(f.ItemsPerPage))
		start := f.StartIndex
		if start < 1 {
			start = 1
		}
		w.text("opensearch:startIndex", strconv.Itoa(start))
	}
	for _, e := range f.Entries {
		e.writeAtom(w, f.Kind)
	}
	w.depth--
	w.line("</feed>")
	return w.buf.Bytes()
}

func (e Entry) writeAtom(w *xmlWriter, kind Kind) {
	w.line("<entry>")
	w.depth++
	w.text("title", e.Title)
	w.text("id", e.ID)
	w.text("updated", stamp(e.Updated))
	for _, a := range e.Authors {
		w.line("<author>")
		w.depth++
		w.text("name", a)
		w.depth--
		w.line("</author>")
	}
	if e.Identifier != "" {
		w.text("dc:identifier", e.Identifier)
	}
	if e.Language != "" {
		w.text("dc:language", e.Language)
	}
	if e.Issued != "" {
		w.text("dc:issued", e.Issued)
	}
	if e.Series != "" {
		// Calibre's name for it, which several apps read.
		w.line(`<category scheme="http://calibre-ebook.com/series" term="%s" label="%s"/>`, esc(e.Series), esc(e.Series))
	}
	if e.Summary != "" {
		w.textType("summary", "text", e.Summary)
	}
	if e.Content != "" {
		w.textType("content", "text", e.Content)
	}
	for _, l := range e.Links {
		w.link(l, true)
	}
	w.depth--
	w.line("</entry>")
}

// SearchDescription is the OpenSearch description document an app reads to
// learn how to search the catalogue. template holds {searchTerms} where the
// words go, e.g. "https://host/opds/search?q={searchTerms}".
func SearchDescription(name, description, template string) []byte {
	w := &xmlWriter{}
	w.buf.WriteString(xml.Header)
	w.line(`<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">`)
	w.depth++
	w.text("ShortName", name)
	w.text("Description", description)
	w.text("InputEncoding", "UTF-8")
	w.text("OutputEncoding", "UTF-8")
	w.line(`<Url type="%s" template="%s"/>`, esc(TypeAcquisition), esc(template))
	w.depth--
	w.line("</OpenSearchDescription>")
	return w.buf.Bytes()
}
