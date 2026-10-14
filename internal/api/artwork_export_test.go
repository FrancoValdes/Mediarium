package api

import "context"

// TestRunArtworkJob runs the calendar artwork fetch once.
func (s *Server) TestRunArtworkJob() { s.artworkJob(context.Background()) }
