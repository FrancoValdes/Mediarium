package api

import (
	"context"

	"github.com/rdborg/mediarium/internal/settings"
	"github.com/rdborg/mediarium/internal/subtitles"
)

// Wizdom, a Hebrew subtitle site with a public API (subtitles/wizdom.go). It
// needs no key or account, so the only setting is whether it is switched on,
// and it is off until someone does.

// wizdomOn reports whether Wizdom is switched on.
func (s *Server) wizdomOn() bool { return s.settingOn(settings.KeySubtitleWizdom, false) }

func (s *Server) wizdomClient() *subtitles.Wizdom {
	if s.wizdom == nil {
		return subtitles.NewWizdom()
	}
	return s.wizdom
}

// findWizdomSubtitles asks Wizdom about a title. asked is false when it wasn't
// asked at all (switched off, or the wanted language isn't Hebrew), so the
// caller doesn't count it as a source that came up empty.
func (s *Server) findWizdomSubtitles(ctx context.Context, q subtitles.Query) (results []subtitles.Result, asked bool, err error) {
	if !s.wizdomOn() || !subtitles.WizdomLanguage(q.Language) {
		return nil, false, nil
	}
	imdb := s.addonImdbID(ctx, q)
	if imdb == "" {
		return nil, true, nil // nothing to ask with: asked, found nothing, not a failure
	}
	results, err = s.wizdomClient().Find(ctx, subtitles.WizdomQuery{
		IMDBID: imdb, Series: q.ParentTMDBID > 0, Season: q.Season, Episode: q.Episode, Language: q.Language,
	})
	return results, true, err
}
