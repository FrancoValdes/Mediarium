package api

import (
	"sort"
	"strings"
	"unicode"
)

// searchWords lowercases a title or a search term and keeps only its letters
// and digits, one space between words, so "Dune: Part Two" and "dune part
// two" compare equal.
func searchWords(s string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// withoutArticle drops a leading "the", "a" or "an", so a search for "matrix"
// counts "The Matrix" as a title that starts with it.
func withoutArticle(s string) string {
	for _, a := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(s, a); ok {
			return rest
		}
	}
	return s
}

// newestFirst puts search results in the order people expect: the titles that
// start with what was typed come first, the others after them, and inside each
// of the two groups the newest year comes first, so a 2026 remake is listed
// above the 2022 film with the same name. A result without a year goes last in
// its group. The sort is stable, so equal years keep the search engine's own
// relevance order.
func newestFirst[T any](items []T, query string, title func(T) string, year func(T) int) {
	q := withoutArticle(searchWords(query))
	group := func(t T) int {
		if q != "" && strings.HasPrefix(withoutArticle(searchWords(title(t))), q) {
			return 0
		}
		return 1
	}
	sort.SliceStable(items, func(i, j int) bool {
		if gi, gj := group(items[i]), group(items[j]); gi != gj {
			return gi < gj
		}
		yi, yj := year(items[i]), year(items[j])
		if (yi == 0) != (yj == 0) {
			return yj == 0
		}
		return yi > yj
	})
}
