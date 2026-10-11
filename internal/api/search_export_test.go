package api

import (
	"context"
	"time"

	"github.com/rdborg/mediarium/internal/delay"
)

// TestDelayedGrabsJob runs the once-a-minute job that searches again when a
// held release has waited long enough.
func (s *Server) TestDelayedGrabsJob(ctx context.Context) { s.delayedGrabsJob(ctx) }

// TestSetDelayClock gives the held-release store a clock the test moves.
func (s *Server) TestSetDelayClock(now func() time.Time) { s.delays = delay.NewStore(s.db, now) }
