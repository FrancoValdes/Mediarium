package importlists

import (
	"strings"
	"unicode"
)

// Filter holds the "only add titles that are at least this good or new" rules
// of a list. The rating is TMDB's vote average (0 to 10) whichever service the
// list came from, so the same number means the same thing on every list.
type Filter struct {
	MinRating float64
	MinYear   int
}

// Active reports whether the filter holds back anything.
func (f Filter) Active() bool { return f.MinRating > 0 || f.MinYear > 0 }

// Verdict is the answer to Filter.Check.
type Verdict int

const (
	Pass        Verdict = iota // the title is wanted
	Fail                       // the title is held back
	NeedDetails                // the year or rating is not known yet: look the title up and ask again
)

// Check judges a title by its TMDB rating and year, where 0 means "not known".
// A title that is clearly too old or too low is held back without looking
// anything up. When something is unknown, the answer is NeedDetails the first
// time (haveDetails false) and Fail after the title was looked up and TMDB
// still has no rating or year: a title nobody has rated yet doesn't pass a
// minimum rating.
func (f Filter) Check(rating float64, year int, haveDetails bool) Verdict {
	unknown := false
	if f.MinYear > 0 {
		switch {
		case year == 0:
			unknown = true
		case year < f.MinYear:
			return Fail
		}
	}
	if f.MinRating > 0 {
		switch {
		case rating <= 0:
			unknown = true
		case rating < f.MinRating:
			return Fail
		}
	}
	switch {
	case !unknown:
		return Pass
	case haveDetails:
		return Fail
	}
	return NeedDetails
}

// NormalizeTitle reduces a title to lower-case letters and digits with single
// spaces, "&" read as "and" and a leading "the" dropped, so "The Lord of the
// Rings: The Two Towers" and "lord of the rings - the two towers" compare equal.
func NormalizeTitle(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "&", " and "))
	var b strings.Builder
	space := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	out := strings.TrimSpace(b.String())
	return strings.TrimPrefix(out, "the ")
}

// Candidate is a title TMDB returned for a search.
type Candidate struct {
	ID    int
	Title string
	Year  int
}

// BestMatch picks the search result that is the title a list names: the same
// title (see NormalizeTitle) and, when the list gives a year, within a year of
// it. Of several, the closest year wins, then the first (TMDB lists the most
// relevant first). It returns false rather than guess.
func BestMatch(title string, year int, candidates []Candidate) (int, bool) {
	want := NormalizeTitle(title)
	if want == "" {
		return 0, false
	}
	best, bestGap := 0, 1<<30
	for _, c := range candidates {
		if NormalizeTitle(c.Title) != want {
			continue
		}
		gap := 0
		if year > 0 {
			if c.Year == 0 {
				continue
			}
			gap = c.Year - year
			if gap < 0 {
				gap = -gap
			}
			if gap > 1 {
				continue
			}
		}
		if gap < bestGap {
			best, bestGap = c.ID, gap
		}
	}
	return best, best != 0
}
