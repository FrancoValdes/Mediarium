package api

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/quality"
)

// A person can list "Original language" among the audio languages they want.
// What it means is a property of each film or show, so the language is asked
// from TMDB the first time it is needed and remembered for as long as the
// server runs (a title's original language doesn't change).
var originalLanguages sync.Map // "movie:603" -> "English"

func (s *Server) originalLanguageOf(kind string, tmdbID int) string {
	if tmdbID == 0 {
		return ""
	}
	key := kind + ":" + strconv.Itoa(tmdbID)
	if v, ok := originalLanguages.Load(key); ok {
		return v.(string)
	}
	c := s.TMDB()
	if c == nil || !c.HasAPIKey() {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code := ""
	if kind == "movie" {
		d, err := c.GetMovieDetail(ctx, tmdbID)
		if err != nil {
			return "" // not remembered, so the next search asks again
		}
		code = d.OriginalLanguage
	} else {
		d, err := c.GetShowFull(ctx, tmdbID)
		if err != nil {
			return ""
		}
		code = d.OriginalLanguage
	}
	name := quality.LanguageName(code)
	originalLanguages.Store(key, name)
	return name
}

func hasOriginalToken(list string) bool {
	for _, l := range quality.ParseLanguages(list) {
		if strings.EqualFold(l, quality.OriginalLanguage) {
			return true
		}
	}
	return false
}

// withOriginalLanguage returns the profile with "Original language" in its
// wanted audio languages replaced by the title's own language, and the same
// language given to custom formats that have a condition on it.
func (s *Server) withOriginalLanguage(p quality.Profile, kind string, tmdbID int) quality.Profile {
	needs := func(q quality.Profile) bool { return hasOriginalToken(q.Language) || q.FormatsNeedOriginalLanguage() }
	need := needs(p)
	for _, f := range p.FallbackProfiles {
		need = need || needs(f)
	}
	if !need {
		return p
	}
	orig := s.originalLanguageOf(kind, tmdbID)
	p.Language = quality.ResolveOriginal(p.Language, orig)
	p.OriginalLanguage = orig
	fallbacks := make([]quality.Profile, len(p.FallbackProfiles))
	for i, f := range p.FallbackProfiles {
		f.Language = quality.ResolveOriginal(f.Language, orig)
		f.OriginalLanguage = orig
		fallbacks[i] = f
	}
	p.FallbackProfiles = fallbacks
	return p
}

// profileForMovie is the profile that rules a movie, with its audio languages
// settled for that film.
func (s *Server) profileForMovie(ps profileSet, m library.Movie) quality.Profile {
	return s.withOriginalLanguage(ps.resolve(m.ProfileID), "movie", m.TMDBID)
}

// profileForSeries is the same for a show.
func (s *Server) profileForSeries(ps profileSet, sr library.Series) quality.Profile {
	return s.withOriginalLanguage(ps.resolveTV(sr.ProfileID), "tv", sr.TMDBID)
}
