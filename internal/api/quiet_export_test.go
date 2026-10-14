package api

import "context"

// TestHeavy runs fn the way a heavy background job runs: not during the quiet time.
func (s *Server) TestHeavy(fn func()) { s.heavy(func(context.Context) { fn() })(context.Background()) }
