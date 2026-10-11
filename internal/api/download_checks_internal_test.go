package api

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/download"
	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/problems"
	"github.com/rdborg/mediarium/internal/queue"
)

func TestBeyondRetention(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	daysAgo := func(n int) time.Time { return now.Add(-time.Duration(n)*24*time.Hour - time.Hour) }
	usenet := func(posted time.Time) indexers.Result {
		return indexers.Result{Protocol: indexers.ProtocolUsenet, PublishDate: posted}
	}
	tests := []struct {
		name      string
		res       indexers.Result
		retention int
		wantOld   bool
		wantAge   int
	}{
		{"setting off keeps everything", usenet(daysAgo(5000)), 0, false, 0},
		{"inside the retention", usenet(daysAgo(100)), 1000, false, 0},
		{"exactly at the limit stays", usenet(now.Add(-1000 * 24 * time.Hour)), 1000, false, 0},
		{"a day past the limit goes", usenet(daysAgo(1001)), 1000, true, 1001},
		{"far past the limit", usenet(daysAgo(2500)), 1000, true, 2500},
		{"no post date can't be judged", usenet(time.Time{}), 1000, false, 0},
		{"a post date in the future stays", usenet(now.Add(48 * time.Hour)), 30, false, 0},
		{"torrents have no retention", indexers.Result{Protocol: indexers.ProtocolTorrent, PublishDate: daysAgo(5000)}, 30, false, 0},
		{"a torrent from a feed added as Usenet has none either", indexers.Result{Protocol: indexers.ProtocolUsenet, Torrent: true, PublishDate: daysAgo(5000)}, 30, false, 0},
		{"a negative setting is off", usenet(daysAgo(5000)), -5, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			age, old := beyondRetention(tt.res, tt.retention, now)
			if old != tt.wantOld {
				t.Fatalf("old = %v, want %v", old, tt.wantOld)
			}
			if old && age != tt.wantAge {
				t.Errorf("age = %d, want %d", age, tt.wantAge)
			}
		})
	}
}

func TestRetentionReasonIsPlain(t *testing.T) {
	got := retentionReason(1500, 1000)
	want := "Posted 1500 days ago, longer than your Usenet retention of 1000 days"
	if got != want {
		t.Errorf("retentionReason = %q, want %q", got, want)
	}
	if got := retentionReason(1, 1); !strings.Contains(got, "1 day ago") || strings.Contains(got, "1 days") {
		t.Errorf("singular not handled: %q", got)
	}
}

func TestAddExpiredCountsIntoTheSearchNote(t *testing.T) {
	var v searchVerdict
	v.total = 3
	v.addExpired(0, 1000)
	if v.total != 3 || len(v.reasons) != 0 {
		t.Fatalf("nothing expired should change nothing: %+v", v)
	}
	v.addExpired(4, 1000)
	if v.total != 7 {
		t.Errorf("total = %d, want 7", v.total)
	}
	msg, _ := v.message("Searched", "")
	if !strings.Contains(msg, "4 older than your Usenet retention of 1000 days") {
		t.Errorf("note %q doesn't say how many were too old", msg)
	}
}

func TestUnsafeFilesAreClassifiedAndBlocklistable(t *testing.T) {
	// The error as the pipeline produces it, in both places it can come from.
	fromNZB := badRelease(download.UnsafeInNZB(&download.NZB{Files: []download.NZBFile{{Subject: `"Player.exe" yEnc (1/1)`}}}))
	if !isBadRelease(fromNZB) {
		t.Fatal("a release with programs in it must be blocklisted like any bad release")
	}
	for name, err := range map[string]error{
		"from the file list": fromNZB,
		"after unpacking":    badRelease(&download.UnsafeFilesError{Names: []string{"setup.exe"}, Total: 1, After: true}),
		"wrapped":            errors.Join(errors.New("context"), fromNZB),
	} {
		if got := problems.CodeFor(err, problems.CodeDownloadFailed); got != problems.CodeDownloadUnsafe {
			t.Errorf("%s: code = %q, want %q", name, got, problems.CodeDownloadUnsafe)
		}
	}
	if _, ok := problems.Lookup(problems.CodeDownloadUnsafe); !ok {
		t.Error("the new code needs an entry in the problem catalog")
	}
}

func TestDuplicateErrorWording(t *testing.T) {
	tests := []struct {
		name string
		err  *duplicateReleaseError
		want string
	}{
		{"same name", &duplicateReleaseError{other: queue.Item{ReleaseTitle: "Movie.2024.1080p"}}, `"Movie.2024.1080p" is already in the queue or downloaded`},
		{"same NZB", &duplicateReleaseError{other: queue.Item{ReleaseTitle: "Movie.2024.1080p"}, byHash: true}, `same NZB as "Movie.2024.1080p"`},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); !strings.Contains(got, tt.want) {
			t.Errorf("%s: %q lacks %q", tt.name, got, tt.want)
		}
		if asDuplicate(errors.Join(errors.New("x"), tt.err)) == nil {
			t.Errorf("%s: asDuplicate should find it when wrapped", tt.name)
		}
	}
	if asDuplicate(errors.New("other")) != nil {
		t.Error("asDuplicate found a duplicate in an unrelated error")
	}
}

func TestDuplicateHoldsTitleOnlyWhileTheOtherIsOpen(t *testing.T) {
	for st, want := range map[queue.Status]bool{
		queue.StatusQueued: true, queue.StatusDownloading: true, queue.StatusImporting: true, queue.StatusPaused: true, queue.StatusConflict: true,
		queue.StatusCompleted: false, queue.StatusFailed: false, queue.StatusStopped: false,
	} {
		d := &duplicateReleaseError{other: queue.Item{Status: st}}
		if got := d.holdsTitle(); got != want {
			t.Errorf("holdsTitle with the other %s = %v, want %v", st, got, want)
		}
	}
}
