package api

import "time"

// TestStreamPauseUnreachableFor makes the pause-while-streaming loop believe no
// media server has answered for d already.
func (s *Server) TestStreamPauseUnreachableFor(d time.Duration) {
	s.streamPause.mu.Lock()
	defer s.streamPause.mu.Unlock()
	if s.streamPause.unreachable > 0 {
		s.streamPause.unreachableSince = time.Now().Add(-d)
	}
}
