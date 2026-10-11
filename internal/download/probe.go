package download

import (
	"context"
	"errors"
	"time"
)

// ProbeResult is what a test of a login's connection limit found.
type ProbeResult struct {
	// Accepted is how many connections the provider let in at once.
	Accepted int
	// Requested is how many were tried (at most).
	Requested int
	// Refused is true when the provider said "too many connections" before
	// Requested was reached, so Accepted is the provider's limit for this login
	// right now (other programs using the login count against it).
	Refused bool
}

// ErrServerBusy is returned by ProbeConnections when a download is using the
// login, which would make the answer meaningless.
var ErrServerBusy = errors.New("a download is using this server right now")

// ActiveConnections is how many connections the downloader has open or is
// opening to a login.
func ActiveConnections(cfg ClientConfig) int {
	gatesMu.Lock()
	g := gates[keyFor(cfg)]
	gatesMu.Unlock()
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inUse
}

// ProbeConnections finds out how many connections a news server lets this
// login open at once, by opening them one after another (each signed in) until
// the provider says there are too many or upTo are open, then closing them
// all. It stops at the first refusal, so it never asks for more than the
// provider's limit by more than one.
func ProbeConnections(ctx context.Context, cfg ClientConfig, upTo int) (ProbeResult, error) {
	upTo = max(min(upTo, 100), 1)
	res := ProbeResult{Requested: upTo}
	if ActiveConnections(cfg) > 0 {
		return res, ErrServerBusy
	}
	var open []*NNTPConn
	defer func() {
		for _, c := range open {
			_ = c.Quit()
			_ = c.Close()
		}
	}()
	for len(open) < upTo {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		c, err := DialNNTP(cfg.Host, cfg.Port, cfg.UseSSL, 10*time.Second)
		if err == nil {
			err = c.Authenticate(cfg.Username, cfg.Password)
			if err != nil {
				_ = c.Close()
			}
		}
		if err != nil {
			if IsTooManyConnections(err) {
				res.Refused = true
				break
			}
			return res, err
		}
		open = append(open, c)
	}
	res.Accepted = len(open)
	return res, nil
}
