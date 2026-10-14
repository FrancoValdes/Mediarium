package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/queue"
	"github.com/rdborg/mediarium/internal/settings"
)

// brokenNZB is an NZB the release's own fault makes unusable (it lists no
// files): a bad release, blocklisted, unlike a reply that isn't an NZB at all.
const brokenNZB = `<?xml version="1.0" encoding="utf-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"></nzb>`

// A retry that reuses the files moves them into its own folder at once, so
// they stay with it while it waits in line (a restart or the daily clean-up
// used to lose them).
func TestRetryMovesTheKeptFilesAtOnce(t *testing.T) {
	server, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	signUpAdmin(t, client, base)
	// Outside the download hours: the retry waits in line.
	h := time.Now().Hour()
	if err := server.Settings.Set(settings.KeyDownloadHours, fmt.Sprintf("%d-%d", (h+2)%24, (h+3)%24), false); err != nil {
		t.Fatal(err)
	}
	m, err := server.MovieRepo.Add(library.Movie{TMDBID: 1, Title: "Some Movie", Year: 2001})
	if err != nil {
		t.Fatal(err)
	}
	release := "Some.Movie.2001.1080p.WEB-DL.x264-GRP"
	id, err := server.QueueRepo.Enqueue(queue.Item{MovieID: m.ID, ReleaseTitle: release, NZBURL: "http://127.0.0.1:1/never.nzb", Protocol: queue.ProtocolUsenet})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.QueueRepo.SetStatus(id, queue.StatusFailed, "import into library: disk full"); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(server.TestIncompleteDir(), fmt.Sprintf("queue-%d", id))
	writeBody(t, filepath.Join(old, release+".mkv"), "the movie")
	writeBody(t, filepath.Join(old, ".mediarium-downloaded"), "0")

	resp := postJSON[map[string]any](t, client, fmt.Sprintf("%s/api/queue/%d/retry", base, id), nil, http.StatusAccepted)
	newID := int64(resp["queueId"].(float64))
	it, err := server.QueueRepo.Get(newID)
	if err != nil || it.Status != queue.StatusQueued {
		t.Fatalf("the retry should wait in line: %+v %v", it, err)
	}
	moved := filepath.Join(server.TestIncompleteDir(), fmt.Sprintf("queue-%d", newID))
	if b, err := os.ReadFile(filepath.Join(moved, release+".mkv")); err != nil || string(b) != "the movie" {
		t.Fatalf("the kept files should be in the retry's folder now: %q %v", b, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("the old folder should be gone: %v", err)
	}
}

// Blocklisting a paused download hands its movie back (it was marked
// "downloading") so the new search can find it, and removes its files.
func TestBlocklistingAPausedDownloadFreesItsMovie(t *testing.T) {
	e := newControlEnv(t, 2)
	id := e.downloadHeld(t)
	postJSON[map[string]any](t, e.client, e.url(id, "pause"), nil, http.StatusOK)
	if got := e.status(t, id); got != "paused" {
		t.Fatalf("expected paused, got %s", got)
	}
	work := e.workDirFor(id)
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("the paused download keeps its folder: %v", err)
	}

	postJSON[map[string]any](t, e.client, e.url(id, "blocklist"), nil, http.StatusOK)
	if got := e.status(t, id); got != "gone" {
		t.Fatalf("the blocklisted download is still listed: %s", got)
	}
	waitFor(t, "the movie to be free again", func() bool { return e.movieStatus(t) != "downloading" })
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("the blocklisted download's folder is still there: %v", err)
	}
}
