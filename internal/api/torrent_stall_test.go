package api_test

import (
	"net/http"
	"testing"
)

// How long a torrent may get no data before it is given up on is a setting;
// unset it is -1 (each client keeps its own default).
func TestStallLimitIsASetting(t *testing.T) {
	_, base, client := loginNewServer(t)
	get := func() any { return getJSON[map[string]any](t, client, base+"/api/settings")["torrentStallHours"] }
	if get() != float64(-1) {
		t.Fatalf("unset should read -1, got %v", get())
	}
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"torrentStallHours": 12}, http.StatusOK)
	if get() != float64(12) {
		t.Fatalf("12 hours: %v", get())
	}
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"torrentStallHours": 0}, http.StatusOK)
	if get() != float64(0) {
		t.Fatalf("never: %v", get())
	}
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"torrentStallHours": 100}, http.StatusBadRequest)
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"torrentStallHours": -1}, http.StatusOK)
	if get() != float64(-1) {
		t.Fatalf("back to the default: %v", get())
	}
}
