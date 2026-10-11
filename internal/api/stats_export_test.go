package api

import "time"

// TestStatsSnapshot takes the daily statistics snapshot as of now.
func (s *Server) TestStatsSnapshot(now time.Time) error { return s.takeStatsSnapshot(now) }

// TestExec runs one statement on the server's database (to set up a test).
func (s *Server) TestExec(query string, args ...any) error {
	_, err := s.db.Exec(query, args...)
	return err
}
