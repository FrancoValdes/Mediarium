// Package metafiles writes the small files media servers read next to your
// videos: NFO files (movie.nfo, tvshow.nfo, one per episode) and artwork
// (poster, fanart, clearlogo, season posters). Kodi, Jellyfin and Emby read
// both; the NFO files carry the TMDB, IMDb and TVDB ids so the server matches
// the title correctly.
//
// The NFO layout follows what Kodi itself writes (CVideoInfoTag::Save in
// Kodi's source): the root element is movie, tvshow or episodedetails, ratings
// sit in a ratings block, ids are uniqueid elements with a type attribute, and a
// collection is a set element. Jellyfin and Emby read the same elements and
// also the older tmdbid, imdbid and tvdbid ones, which are written too.
package metafiles

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Actor is one person of the cast.
type Actor struct {
	Name  string
	Role  string
	Order int
}

// Set is the collection a film belongs to.
type Set struct {
	Name     string
	Overview string
}

// Movie is what movie.nfo says about a film. Empty fields are left out.
type Movie struct {
	Title         string
	OriginalTitle string
	Year          int
	Premiered     string // YYYY-MM-DD
	Plot          string
	Tagline       string
	Runtime       int // minutes
	MPAA          string
	Rating        float64 // out of 10, TMDB's
	Votes         int
	TMDBID        int
	IMDBID        string
	Genres        []string
	Countries     []string
	Studios       []string
	Directors     []string
	Writers       []string
	Set           *Set
	Actors        []Actor
	TrailerKey    string // a YouTube video id
}

// Show is what tvshow.nfo says about a show.
type Show struct {
	Title         string
	OriginalTitle string
	Year          int
	Premiered     string // YYYY-MM-DD, the first air date
	Plot          string
	MPAA          string
	Status        string // "Continuing" or "Ended"
	Rating        float64
	Votes         int
	TMDBID        int
	TVDBID        int
	IMDBID        string
	Genres        []string
	Studios       []string // the networks
	Actors        []Actor
}

// Episode is what an episode's NFO says about it.
type Episode struct {
	ShowTitle string
	Title     string
	Season    int
	Episode   int
	Aired     string // YYYY-MM-DD
	Plot      string
	Runtime   int
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes" ?>` + "\n"

// NFO renders movie.nfo.
func (m Movie) NFO() []byte {
	w := &nfoWriter{}
	w.raw(xmlHeader)
	w.open("movie")
	w.text("title", m.Title)
	if m.OriginalTitle != m.Title {
		w.text("originaltitle", m.OriginalTitle)
	}
	w.ratings(m.Rating, m.Votes)
	w.text("plot", m.Plot)
	w.text("tagline", m.Tagline)
	if m.Runtime > 0 {
		w.text("runtime", strconv.Itoa(m.Runtime))
	}
	w.text("mpaa", m.MPAA)
	// <id> is the film's IMDb id where there is one, as it always was in
	// Kodi's movie NFO files (a number that is not an IMDb id would be read as
	// one); the typed ids follow.
	w.text("id", m.IMDBID)
	if m.TMDBID > 0 {
		w.text("tmdbid", strconv.Itoa(m.TMDBID))
	}
	w.text("imdbid", m.IMDBID)
	if m.TMDBID > 0 {
		w.textAttrs("uniqueid", strconv.Itoa(m.TMDBID), `type="tmdb" default="true"`)
	}
	if m.IMDBID != "" {
		w.textAttrs("uniqueid", m.IMDBID, `type="imdb"`)
	}
	w.list("genre", m.Genres)
	w.list("country", m.Countries)
	if m.Set != nil && m.Set.Name != "" {
		w.open("set")
		w.text("name", m.Set.Name)
		w.text("overview", m.Set.Overview)
		w.close("set")
	}
	w.list("credits", m.Writers)
	w.list("director", m.Directors)
	w.text("premiered", m.Premiered)
	if m.Year > 0 {
		w.text("year", strconv.Itoa(m.Year))
	}
	w.list("studio", m.Studios)
	if m.TrailerKey != "" {
		w.text("trailer", "plugin://plugin.video.youtube/?action=play_video&videoid="+m.TrailerKey)
	}
	w.actors(m.Actors)
	w.close("movie")
	return w.bytes()
}

// NFO renders tvshow.nfo.
func (s Show) NFO() []byte {
	w := &nfoWriter{}
	w.raw(xmlHeader)
	w.open("tvshow")
	w.text("title", s.Title)
	if s.OriginalTitle != s.Title {
		w.text("originaltitle", s.OriginalTitle)
	}
	w.text("showtitle", s.Title)
	w.ratings(s.Rating, s.Votes)
	w.text("plot", s.Plot)
	w.text("mpaa", s.MPAA)
	// For a show <id> is the TheTVDB id, as Sonarr writes it.
	if s.TVDBID > 0 {
		w.text("id", strconv.Itoa(s.TVDBID))
	}
	if s.TMDBID > 0 {
		w.text("tmdbid", strconv.Itoa(s.TMDBID))
	}
	if s.TVDBID > 0 {
		w.text("tvdbid", strconv.Itoa(s.TVDBID))
	}
	w.text("imdbid", s.IMDBID)
	if s.TVDBID > 0 {
		w.textAttrs("uniqueid", strconv.Itoa(s.TVDBID), `type="tvdb" default="true"`)
	}
	if s.TMDBID > 0 {
		attrs := `type="tmdb"`
		if s.TVDBID == 0 {
			attrs += ` default="true"`
		}
		w.textAttrs("uniqueid", strconv.Itoa(s.TMDBID), attrs)
	}
	if s.IMDBID != "" {
		w.textAttrs("uniqueid", s.IMDBID, `type="imdb"`)
	}
	w.list("genre", s.Genres)
	w.text("premiered", s.Premiered)
	if s.Year > 0 {
		w.text("year", strconv.Itoa(s.Year))
	}
	w.text("status", s.Status)
	w.list("studio", s.Studios)
	w.actors(s.Actors)
	w.close("tvshow")
	return w.bytes()
}

// EpisodesNFO renders the NFO of one video file: one episodedetails element
// per episode in it, one after the other (a file that holds two episodes
// carries both, which is how Kodi, Jellyfin and Emby read them).
func EpisodesNFO(eps []Episode) []byte {
	w := &nfoWriter{}
	w.raw(xmlHeader)
	for i, e := range eps {
		if i > 0 {
			w.raw("\n")
		}
		w.open("episodedetails")
		w.text("title", e.Title)
		w.text("showtitle", e.ShowTitle)
		w.text("season", strconv.Itoa(e.Season))
		w.text("episode", strconv.Itoa(e.Episode))
		w.text("aired", e.Aired)
		w.text("plot", e.Plot)
		if e.Runtime > 0 {
			w.text("runtime", strconv.Itoa(e.Runtime))
		}
		w.close("episodedetails")
	}
	return w.bytes()
}

// nfoWriter builds the XML by hand: the files are small, the layout is fixed
// and the output has to be the same every time.
type nfoWriter struct {
	buf   bytes.Buffer
	depth int
}

func (w *nfoWriter) bytes() []byte { return w.buf.Bytes() }

func (w *nfoWriter) raw(s string) { w.buf.WriteString(s) }

func (w *nfoWriter) indent() {
	for i := 0; i < w.depth; i++ {
		w.buf.WriteString("  ")
	}
}

func (w *nfoWriter) open(tag string) {
	w.indent()
	fmt.Fprintf(&w.buf, "<%s>\n", tag)
	w.depth++
}

func (w *nfoWriter) close(tag string) {
	w.depth--
	w.indent()
	fmt.Fprintf(&w.buf, "</%s>\n", tag)
}

// text writes <tag>value</tag>, or nothing for an empty value.
func (w *nfoWriter) text(tag, value string) { w.textAttrs(tag, value, "") }

func (w *nfoWriter) textAttrs(tag, value, attrs string) {
	value = cleanText(value)
	if value == "" {
		return
	}
	w.indent()
	if attrs != "" {
		fmt.Fprintf(&w.buf, "<%s %s>", tag, attrs)
	} else {
		fmt.Fprintf(&w.buf, "<%s>", tag)
	}
	w.buf.WriteString(escape(value))
	fmt.Fprintf(&w.buf, "</%s>\n", tag)
}

func (w *nfoWriter) list(tag string, values []string) {
	seen := map[string]bool{}
	for _, v := range values {
		v = cleanText(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		w.text(tag, v)
	}
}

func (w *nfoWriter) ratings(value float64, votes int) {
	if value <= 0 {
		return
	}
	w.open("ratings")
	w.indent()
	w.buf.WriteString(`<rating name="themoviedb" max="10" default="true">` + "\n")
	w.depth++
	w.text("value", strconv.FormatFloat(value, 'f', 1, 64))
	if votes > 0 {
		w.text("votes", strconv.Itoa(votes))
	}
	w.depth--
	w.indent()
	w.buf.WriteString("</rating>\n")
	w.close("ratings")
}

func (w *nfoWriter) actors(actors []Actor) {
	for _, a := range actors {
		if cleanText(a.Name) == "" {
			continue
		}
		w.open("actor")
		w.text("name", a.Name)
		w.text("role", a.Role)
		w.text("order", strconv.Itoa(a.Order))
		w.close("actor")
	}
}

// cleanText trims a value and drops the characters XML 1.0 cannot hold.
func cleanText(s string) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if s == "" {
		return ""
	}
	var b strings.Builder
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		s = s[n:]
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r < 0x20, r == 0x7f, r >= 0xfffe, r >= 0xd800 && r <= 0xdfff:
			// not allowed in XML 1.0
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(b.String(), "\r\n", "\n"), "\r", "\n"))
}

func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
