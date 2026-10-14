package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/usagestats"
)

type statsReceiver struct {
	mu     sync.Mutex
	bodies [][]byte
	srv    *httptest.Server
}

func newStatsReceiver(t *testing.T) *statsReceiver {
	r := &statsReceiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, b)
		r.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *statsReceiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *statsReceiver) waitFor(n int) bool {
	for i := 0; i < 100; i++ {
		if r.count() >= n {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

type statsState struct {
	Choice  string             `json:"choice"`
	HasID   bool               `json:"hasId"`
	Preview usagestats.Payload `json:"preview"`
}

// Nothing is sent before the administrator says yes, and the message that goes
// out is exactly the one the settings page previews.
func TestUsageStatsAreSentOnlyAfterYes(t *testing.T) {
	server, base, client := loginNewServer(t)
	recv := newStatsReceiver(t)
	server.TestSetStatsEndpoint(recv.srv.URL)

	before := getJSON[statsState](t, client, base+"/api/usage-stats")
	if before.Choice != "" || before.HasID {
		t.Fatalf("a new install must not have answered: %+v", before)
	}
	server.TestSendStats()
	if recv.count() != 0 {
		t.Fatal("something was sent before the question was answered")
	}

	putJSON(t, client, base+"/api/usage-stats", map[string]string{"choice": "no"}, http.StatusOK)
	server.TestSendStats()
	if recv.count() != 0 {
		t.Fatal("something was sent after No")
	}

	putJSON(t, client, base+"/api/usage-stats", map[string]string{"choice": "yes"}, http.StatusOK)
	if !recv.waitFor(1) {
		t.Fatal("nothing was sent after Yes")
	}
	var sent usagestats.Payload
	if err := json.Unmarshal(recv.bodies[0], &sent); err != nil {
		t.Fatal(err)
	}
	if !usagestats.Valid(sent) {
		t.Fatalf("the message is not valid: %s", recv.bodies[0])
	}
	after := getJSON[statsState](t, client, base+"/api/usage-stats")
	if after.Choice != "yes" || !after.HasID || after.Preview.ID != sent.ID {
		t.Fatalf("state after yes: %+v (sent id %s)", after, sent.ID)
	}
	if after.Preview.Version != sent.Version || after.Preview.Arch != sent.Arch || after.Preview.Library != sent.Library {
		t.Fatalf("the preview is not what was sent:\n%+v\n%+v", after.Preview, sent)
	}

	// Once a day only.
	server.TestSendStats()
	if recv.count() != 1 {
		t.Fatalf("sent %d times in one day", recv.count())
	}

	// A new ID, then No forgets it.
	postJSON[map[string]any](t, client, base+"/api/usage-stats/reset-id", nil, http.StatusOK)
	reset := getJSON[statsState](t, client, base+"/api/usage-stats")
	if reset.Preview.ID == sent.ID {
		t.Fatal("Reset my ID kept the same ID")
	}
	// A new ID still waits for the next day.
	server.TestSendStats()
	if recv.count() != 1 {
		t.Fatalf("Reset my ID sent a second message the same day (%d)", recv.count())
	}
	putJSON(t, client, base+"/api/usage-stats", map[string]string{"choice": "no"}, http.StatusOK)
	if got := getJSON[statsState](t, client, base+"/api/usage-stats"); got.HasID || got.Choice != "no" {
		t.Fatalf("No must forget the ID: %+v", got)
	}
	// Nor does No and then Yes again.
	putJSON(t, client, base+"/api/usage-stats", map[string]string{"choice": "yes"}, http.StatusOK)
	server.TestSendStats()
	time.Sleep(200 * time.Millisecond)
	if recv.count() != 1 {
		t.Fatalf("No and Yes again sent a second message the same day (%d)", recv.count())
	}
}

func TestUsageStatsRefuseAnythingButYesOrNo(t *testing.T) {
	_, base, client := loginNewServer(t)
	putJSON(t, client, base+"/api/usage-stats", map[string]string{"choice": "maybe"}, http.StatusBadRequest)
}
