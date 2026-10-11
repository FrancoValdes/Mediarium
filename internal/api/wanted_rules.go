package api

import (
	"fmt"
	"time"

	"github.com/rdborg/mediarium/internal/library"
)

// The rules for "wanted, and missing" live here so the Wanted page and the
// Dashboard number can never disagree: a title only counts when it is
// monitored, has no file, and (for an episode) has already aired.

// movieWantedMissing reports whether a movie belongs in Wanted > Missing.
func movieWantedMissing(m library.Movie) bool {
	return m.Monitored && m.Status == library.StatusMissing && !fileExists(m.FilePath)
}

// An episode belongs in Wanted > Missing when its show and it are monitored,
// it is marked missing, it has aired by today and no file of it is on disk.
// The library package's ListWantedEpisodes and MissingEpisodeFiles apply the
// first three in SQL; the file check stays in Go.

// wantedMissingCounts counts what Wanted > Missing would list.
//
// The database narrows it down to the titles marked missing (a handful of
// columns, not whole rows), and the only thing left to check is the odd one
// that still names a file: if that file is on disk, it is not wanted.
func (s *Server) wantedMissingCounts() (movies, episodes int, err error) {
	movieFiles, err := s.MovieRepo.MissingMovieFiles()
	if err != nil {
		return 0, 0, fmt.Errorf("list missing movies: %w", err)
	}
	for _, path := range movieFiles {
		if !fileExists(path) {
			movies++
		}
	}
	today := time.Now().UTC().Format("2006-01-02")
	episodeFiles, err := s.MovieRepo.MissingEpisodeFiles(today)
	if err != nil {
		return 0, 0, fmt.Errorf("list missing episodes: %w", err)
	}
	for _, path := range episodeFiles {
		if !fileExists(path) {
			episodes++
		}
	}
	return movies, episodes, nil
}
