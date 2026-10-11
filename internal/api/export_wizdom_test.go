package api

import (
	"context"

	"github.com/rdborg/mediarium/internal/subtitles"
)

// TestSetWizdomBaseURL points the Wizdom client at a local fixture server.
func (s *Server) TestSetWizdomBaseURL(baseURL string) {
	s.wizdom = subtitles.NewWizdomWithBaseURL(baseURL)
}

// TestWizdomRoundTrip searches for a movie's subtitles in a language and
// downloads the first one found, the way the sweep does.
func (s *Server) TestWizdomRoundTrip(ctx context.Context, tmdbID int, lang string) (results []subtitles.Result, data []byte, err error) {
	results, err = s.findSubtitles(ctx, subtitles.Query{TMDBID: tmdbID, Type: "movie", Language: lang}, false)
	if err != nil || len(results) == 0 {
		return results, nil, err
	}
	data, err = s.subtitleData(ctx, subtitleItem{kind: "movie", tmdbID: tmdbID}, subtitlePick{source: results[0].Source, link: results[0].Link, lang: lang})
	return results, data, err
}
