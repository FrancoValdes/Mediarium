package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/settings"
)

// The weekly summary reaches the targets that listen for it, only when asked
// for, and the day and hour are saved with the other settings.
func TestWeeklySummary(t *testing.T) {
	server, base, client := loginNewServer(t)
	hook, rec := newHookRecorder(t, http.StatusOK)

	// Nothing listens yet: sending says so.
	postJSON[map[string]any](t, client, base+"/api/notifications/digest", nil, http.StatusBadRequest)

	postJSON[map[string]any](t, client, base+"/api/notifications", map[string]any{
		"name": "Hook", "type": "webhook", "events": []string{"digest"}, "config": map[string]string{"url": hook.URL},
	}, http.StatusCreated)
	postJSON[map[string]any](t, client, base+"/api/notifications/digest", nil, http.StatusOK)
	server.TestWaitBackground()
	if rec.count() != 1 || !strings.Contains(rec.bodies[0], "Mediarium this week") {
		t.Fatalf("the summary should arrive once: %v", rec.bodies)
	}
	// Sending one to try it out doesn't count as this week's: the scheduled
	// one still goes out.
	if v, _ := server.Settings.Get(settings.KeyDigestSent); v != "" {
		t.Fatalf("a summary sent by hand must not hold back the scheduled one (sent at %q)", v)
	}

	// The day and hour are saved; a wrong one is refused; the default is off.
	got := getJSON[map[string]any](t, client, base+"/api/settings")
	if got["digestDay"] != float64(-1) || got["digestHour"] != float64(9) {
		t.Fatalf("the summary is off by default: %v %v", got["digestDay"], got["digestHour"])
	}
	putJSON(t, client, base+"/api/settings", map[string]any{"digestDay": 3, "digestHour": 18}, http.StatusOK)
	got = getJSON[map[string]any](t, client, base+"/api/settings")
	if got["digestDay"] != float64(3) || got["digestHour"] != float64(18) {
		t.Fatalf("the day and hour are saved: %v %v", got["digestDay"], got["digestHour"])
	}
	putJSON(t, client, base+"/api/settings", map[string]any{"digestDay": 9}, http.StatusBadRequest)
}
