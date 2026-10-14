package api

import (
	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/library"
)

// TestGrabMovieReplacing grabs a release the way "Replace with a smaller
// version" does: it is meant to take the place of the file at replaces. For
// shrink_test.go (package api_test).
func (s *Server) TestGrabMovieReplacing(m library.Movie, releaseTitle, downloadURL, replaces string) (int64, error) {
	return s.grabMovieReplacing(m, releaseTitle, downloadURL, 0, indexers.ProtocolUsenet, grabPicked, replaces)
}
