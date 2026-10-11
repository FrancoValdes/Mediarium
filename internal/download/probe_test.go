package download

import (
	"context"
	"errors"
	"testing"
)

// The test opens connections until the provider says there are too many, and
// reports how many were let in. A server that allows them all says so, and
// one that refuses the very first reports zero.
func TestProbeConnectionsFindsTheLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		max      int
		upTo     int
		accepted int
		refused  bool
	}{
		{"limit below what was set", 4, 10, 4, true},
		{"everything works", 0, 6, 6, false},
		{"the limit is what was set", 5, 5, 5, false},
		{"refuses at once", -1, 8, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &limitedNNTP{max: tc.max}
			if tc.max < 0 {
				srv.refuseAll = true
			}
			srv = newLimitedNNTP(t, srv, nil)
			cfg := ClientConfig{Host: srv.addr, Port: srv.port, Username: "u", Password: "p", Connections: tc.upTo}
			res, err := ProbeConnections(context.Background(), cfg, tc.upTo)
			if err != nil {
				t.Fatal(err)
			}
			if res.Accepted != tc.accepted || res.Refused != tc.refused {
				t.Errorf("accepted %d refused %v, want %d %v", res.Accepted, res.Refused, tc.accepted, tc.refused)
			}
		})
	}
}

func TestProbeConnectionsWaitsForRunningDownloads(t *testing.T) {
	cfg := ClientConfig{Host: "probe-busy.example", Port: 119, Username: "u", Connections: 2}
	rel, err := gateFor(cfg).acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	if _, err := ProbeConnections(context.Background(), cfg, 2); !errors.Is(err, ErrServerBusy) {
		t.Fatalf("err = %v, want ErrServerBusy", err)
	}
}
