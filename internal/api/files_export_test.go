package api

import (
	"context"
	"database/sql"
)

// TestRunCollectionsJob runs one pass of the collections job.
func (s *Server) TestRunCollectionsJob() { s.collectionsJob(context.Background()) }

// TestMakeCollectionsDue makes followed collections due for a look again, as
// if their last look was a while ago.
func (s *Server) TestMakeCollectionsDue() {
	_, _ = s.db.Exec(`UPDATE collections SET checked_at = ''`)
}

// SetTestImageBase points the picture downloads at a local server and returns
// what puts the real address back.
func SetTestImageBase(base string) (restore func()) {
	old := tmdbImageBase
	tmdbImageBase = base
	return func() { tmdbImageBase = old }
}

// TestMovieImported and TestEpisodesImported run what happens after an import.
func (s *Server) TestMovieImported(file string)        { s.movieImported(file) }
func (s *Server) TestEpisodesImported(files ...string) { s.episodesImported(files...) }

// TestScalar returns the first column of the first row of a query as text.
func (s *Server) TestScalar(query string, params ...any) (string, error) {
	var v sql.NullString
	if err := s.db.QueryRow(query, params...).Scan(&v); err != nil {
		return "", err
	}
	return v.String, nil
}
