package api_test

import (
	"net/http"
	"testing"
)

// A download that waits in line can be sent to the front, and the one that
// was first moves back by one.
func TestAWaitingDownloadCanGoToTheFrontOfTheLine(t *testing.T) {
	e := newControlEnv(t, 2)
	m2, m3, m4 := lineMovie(t, e, 2), lineMovie(t, e, 3), lineMovie(t, e, 4)
	running := e.downloadHeld(t)
	b, _ := e.server.TestGrabMovie(m2, releaseName, e.nzbURL)
	c, _ := e.server.TestGrabMovie(m3, releaseName, e.nzbURL)
	d, _ := e.server.TestGrabMovie(m4, releaseName, e.nzbURL)
	if e.position(t, d) != 3 {
		t.Fatalf("the last one starts at 3, it is %d", e.position(t, d))
	}

	postJSON[map[string]any](t, e.client, e.url(d, "front"), nil, http.StatusOK)
	if e.position(t, d) != 1 || e.position(t, b) != 2 || e.position(t, c) != 3 {
		t.Fatalf("after moving the last one to the front the line is d=%d b=%d c=%d, want 1 2 3", e.position(t, d), e.position(t, b), e.position(t, c))
	}

	// Something that is running is not in the line: it cannot be moved.
	postJSON[map[string]any](t, e.client, e.url(running, "front"), nil, http.StatusConflict)
	postJSON[map[string]any](t, e.client, e.base+"/api/queue/99999/front", nil, http.StatusNotFound)
	e.nntp.open()
	e.waitAllDone(t, []int64{running, b, c, d})
}

// Several downloads can be sent to the front at once and keep the order they
// were picked in.
func TestSeveralDownloadsGoToTheFrontInTheOrderPicked(t *testing.T) {
	e := newControlEnv(t, 2)
	m2, m3, m4 := lineMovie(t, e, 2), lineMovie(t, e, 3), lineMovie(t, e, 4)
	running := e.downloadHeld(t)
	b, _ := e.server.TestGrabMovie(m2, releaseName, e.nzbURL)
	c, _ := e.server.TestGrabMovie(m3, releaseName, e.nzbURL)
	d, _ := e.server.TestGrabMovie(m4, releaseName, e.nzbURL)

	resp := postJSON[map[string]any](t, e.client, e.base+"/api/queue/bulk", map[string]any{"ids": []int64{d, c}, "action": "front"}, http.StatusOK)
	if resp["done"].(float64) != 2 || len(resp["failed"].([]any)) != 0 {
		t.Fatalf("both should move, got %v", resp)
	}
	if e.position(t, d) != 1 || e.position(t, c) != 2 || e.position(t, b) != 3 {
		t.Fatalf("the line is d=%d c=%d b=%d, want 1 2 3", e.position(t, d), e.position(t, c), e.position(t, b))
	}
	e.nntp.open()
	e.waitAllDone(t, []int64{running, b, c, d})
}

// One that can't be done does not stop the rest, and is named in the answer.
func TestBulkActionsReportWhatFailed(t *testing.T) {
	e := newControlEnv(t, 2)
	m2, m3 := lineMovie(t, e, 2), lineMovie(t, e, 3)
	running := e.downloadHeld(t)
	b, _ := e.server.TestGrabMovie(m2, releaseName, e.nzbURL)
	c, _ := e.server.TestGrabMovie(m3, releaseName, e.nzbURL)

	resp := postJSON[map[string]any](t, e.client, e.base+"/api/queue/bulk", map[string]any{"ids": []int64{b, 99999, c}, "action": "pause"}, http.StatusOK)
	if resp["done"].(float64) != 2 {
		t.Fatalf("two should pause, got %v", resp)
	}
	failed := resp["failed"].([]any)
	if len(failed) != 1 || failed[0].(map[string]any)["id"].(float64) != 99999 {
		t.Fatalf("the missing one should be listed as failed, got %v", failed)
	}
	if e.status(t, b) != "paused" || e.status(t, c) != "paused" {
		t.Fatalf("b and c should be paused: %s, %s", e.status(t, b), e.status(t, c))
	}

	postJSON[map[string]any](t, e.client, e.base+"/api/queue/bulk", map[string]any{"ids": []int64{b, c}, "action": "resume"}, http.StatusOK)
	if e.status(t, b) != "queued" || e.status(t, c) != "queued" {
		t.Fatalf("b and c wait in line again: %s, %s", e.status(t, b), e.status(t, c))
	}

	for _, bad := range []map[string]any{
		{"ids": []int64{b}, "action": "explode"},
		{"ids": []int64{}, "action": "pause"},
		{"action": "pause"},
	} {
		postJSON[map[string]any](t, e.client, e.base+"/api/queue/bulk", bad, http.StatusBadRequest)
	}
	e.nntp.open()
	e.waitAllDone(t, []int64{running, b, c})
}

// Several paused downloads can be taken off the list in one go; one that is
// still running or waiting is refused and named.
func TestBulkRemoveTakesPausedDownloadsOffTheList(t *testing.T) {
	e := newControlEnv(t, 2)
	m2, m3 := lineMovie(t, e, 2), lineMovie(t, e, 3)
	running := e.downloadHeld(t)
	b, _ := e.server.TestGrabMovie(m2, releaseName, e.nzbURL)
	c, _ := e.server.TestGrabMovie(m3, releaseName, e.nzbURL)
	postJSON[map[string]any](t, e.client, e.base+"/api/queue/bulk", map[string]any{"ids": []int64{b, c}, "action": "pause"}, http.StatusOK)

	resp := postJSON[map[string]any](t, e.client, e.base+"/api/queue/bulk", map[string]any{"ids": []int64{b, c, running}, "action": "remove"}, http.StatusOK)
	failed := resp["failed"].([]any)
	if resp["done"].(float64) != 2 || len(failed) != 1 || failed[0].(map[string]any)["id"].(float64) != float64(running) {
		t.Fatalf("the two paused go, the running one is refused: %v", resp)
	}
	for _, id := range []int64{b, c} {
		if _, err := e.server.QueueRepo.Get(id); err == nil {
			t.Fatalf("download %d should be off the list", id)
		}
	}
	if e.status(t, running) != "downloading" {
		t.Fatalf("the running download carries on: %s", e.status(t, running))
	}
	e.nntp.open()
	e.waitAllDone(t, []int64{running})
}
