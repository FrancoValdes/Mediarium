package api

import (
	"github.com/rdborg/mediarium/internal/metadata"
	"github.com/rdborg/mediarium/internal/quality"
	"github.com/rdborg/mediarium/internal/settings"
)

// preferredLanguage is the audio language wanted (Settings > Library >
// Quality). English until it is changed.
func (s *Server) preferredLanguage() string {
	if v, _ := s.Settings.Get(settings.KeyQualityLanguage); v != "" {
		return v
	}
	return quality.DefaultLanguage
}

// infoLanguage is the language titles, plots, genres and posters are read in
// (Settings > Info, lists and subtitles > Movie and show info). English until
// it is changed.
func (s *Server) infoLanguage() string {
	if v, _ := s.Settings.Get(settings.KeyInfoLanguage); metadata.IsInfoLanguage(v) {
		return v
	}
	return metadata.DefaultLanguage
}

// displayTMDB is the TMDB client for what is read on screen: Discover, search
// results and title pages, in the chosen language. Anything that names a file
// or folder, or searches an indexer for a title, uses TMDB() instead, which
// always asks in English, so choosing another language never renames or
// re-searches anything.
func (s *Server) displayTMDB() *metadata.Client {
	return s.TMDB().Localized(s.infoLanguage())
}
