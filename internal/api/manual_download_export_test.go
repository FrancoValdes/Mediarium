package api

import "time"

// What manual_download_test.go (package api_test) looks at directly.

// TestPruneUploads removes the uploaded files nothing refers to any more.
func (s *Server) TestPruneUploads(now time.Time) int { return s.pruneUploads(now) }
