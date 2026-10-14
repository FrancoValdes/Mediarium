package api_test

import (
	"net/http"
	"testing"
)

func TestQuietForAWhileHoldsHeavyJobs(t *testing.T) {
	server, base, client := loginNewServer(t)
	runs := 0
	server.TestHeavy(func() { runs++ })
	if runs != 1 {
		t.Fatalf("a heavy job runs normally: %d", runs)
	}
	if got := getJSON[map[string]any](t, client, base+"/api/automation/quiet"); got["active"] != false {
		t.Fatalf("not quiet at the start: %v", got)
	}
	got := putJSON(t, client, base+"/api/automation/quiet", map[string]any{"minutes": 60}, http.StatusOK)
	if got["active"] != true || got["until"] == nil {
		t.Fatalf("quiet for an hour: %v", got)
	}
	server.TestHeavy(func() { runs++ })
	if runs != 1 {
		t.Fatalf("a heavy job does nothing during the quiet time: %d", runs)
	}
	putJSON(t, client, base+"/api/automation/quiet", map[string]any{"minutes": 100000}, http.StatusBadRequest)
	putJSON(t, client, base+"/api/automation/quiet", map[string]any{}, http.StatusBadRequest)
	got = putJSON(t, client, base+"/api/automation/quiet", map[string]any{"minutes": 0}, http.StatusOK)
	if got["active"] == true {
		t.Fatalf("0 ends it: %v", got)
	}
	server.TestHeavy(func() { runs++ })
	if runs != 2 {
		t.Fatalf("back to normal: %d", runs)
	}
}
