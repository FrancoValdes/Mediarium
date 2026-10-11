package api

import "context"

// TestSetStatsEndpoint points the anonymous usage numbers at a fake receiver.
func (s *Server) TestSetStatsEndpoint(url string) { s.statsEndpoint = url }

// TestSendStats runs one send, the way the daily timer does.
func (s *Server) TestSendStats() { s.sendStats(context.Background(), false) }
