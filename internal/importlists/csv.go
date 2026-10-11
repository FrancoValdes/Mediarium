package importlists

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ErrNotAList is returned for a CSV that has none of the columns a list
// export has.
var ErrNotAList = errors.New("That file doesn't look like an IMDb or Letterboxd list. The first line should name columns like Const, Title and Year")

// MaxCSVBytes is the biggest file read. A list of thousands of titles is a
// few hundred kilobytes.
const MaxCSVBytes = 4 << 20

// ParseCSV reads a list exported from IMDb ("Export this list": Position,
// Const, Created, Modified, Description, Title, URL, Title Type, ...), from
// Letterboxd (the watchlist.csv in a data export, or a list exported as CSV:
// Name or Title, Year, a Letterboxd URL) or any file with columns for an id
// (tmdbID, imdbID or Const), a title and a year. Columns are found by name,
// in any order; the rest are ignored.
func ParseCSV(text string) ([]Item, error) {
	text = strings.TrimPrefix(text, string(rune(0xFEFF)))
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("That file is empty")
	}
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1 // exports vary; short rows are fine
	r.LazyQuotes = true
	if first := strings.SplitN(text, "\n", 2)[0]; strings.Count(first, ";") > strings.Count(first, ",") {
		r.Comma = ';'
	}
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read csv header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		key := strings.ToLower(strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.TrimSpace(h)))
		if _, seen := col[key]; !seen {
			col[key] = i
		}
	}
	find := func(names ...string) int {
		for _, n := range names {
			if i, ok := col[n]; ok {
				return i
			}
		}
		return -1
	}
	tmdbCol := find("tmdbid", "tmdb")
	imdbCol := find("const", "imdbid", "imdb")
	titleCol := find("title", "name", "primarytitle")
	yearCol := find("year", "releaseyear")
	typeCol := find("titletype", "type")
	if tmdbCol < 0 && imdbCol < 0 && titleCol < 0 {
		return nil, ErrNotAList
	}
	get := func(rec []string, i int) string {
		if i < 0 || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	var out []Item
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read csv: %w", err)
		}
		it := Item{Title: get(rec, titleCol), Year: yearOf(get(rec, yearCol))}
		it.TMDBID, _ = strconv.Atoi(get(rec, tmdbCol))
		if id := strings.ToLower(get(rec, imdbCol)); strings.HasPrefix(id, "tt") {
			it.IMDBID = id
		}
		if typeCol >= 0 {
			kind, ok := kindOfImdbType(get(rec, typeCol))
			if !ok {
				continue // an episode, a game, a podcast
			}
			it.Kind = kind
		}
		if it.TMDBID <= 0 && it.IMDBID == "" && it.Title == "" {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// yearOf reads the first four digits of "1999", "2008-2013" or "(2019)".
func yearOf(s string) int {
	for i := 0; i+4 <= len(s); i++ {
		if y, err := strconv.Atoi(s[i : i+4]); err == nil && y >= 1870 && y <= 2200 {
			return y
		}
	}
	return 0
}

// kindOfImdbType maps IMDb's "Title Type" to movie or tv. ok is false for the
// ones that are neither (an episode, a video game, a podcast).
func kindOfImdbType(t string) (kind string, ok bool) {
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case t == "":
		return "", true
	case strings.Contains(t, "episode"), strings.Contains(t, "game"), strings.Contains(t, "podcast"), strings.Contains(t, "music video"):
		return "", false
	case strings.Contains(t, "series"):
		return "tv", true // TV Series, TV Mini Series
	}
	return "movie", true // Movie, TV Movie, TV Special, Video, Short
}
