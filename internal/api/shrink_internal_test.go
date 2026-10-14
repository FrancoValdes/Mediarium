package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/organizer"
	"github.com/rdborg/mediarium/internal/quality"
)

const gb = int64(1) << 30

func TestSmallerEnough(t *testing.T) {
	tests := []struct {
		name          string
		size, current int64
		want          bool
	}{
		{"much smaller", 10 * gb, 50 * gb, true},
		{"just under a tenth smaller", 89, 100, true},
		{"exactly a tenth smaller is not enough", 90, 100, false},
		{"a little smaller is not worth it", 95, 100, false},
		{"same size", 100, 100, false},
		{"bigger", 120, 100, false},
		{"size unknown", 0, 100, false},
		{"file size unknown", 10, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := smallerEnough(tc.size, tc.current); got != tc.want {
				t.Fatalf("smallerEnough(%d, %d) = %v, want %v", tc.size, tc.current, got, tc.want)
			}
		})
	}
}

func TestPickSmallerResult(t *testing.T) {
	var (
		uhd50   = indexers.Result{Title: "Film.2026.2160p.WEB-DL.x265-GRP", DownloadURL: "uhd50", SizeBytes: 50 * gb}
		uhd40   = indexers.Result{Title: "Film.2026.2160p.WEB-DL.x265-SML", DownloadURL: "uhd40", SizeBytes: 40 * gb}
		web1080 = indexers.Result{Title: "Film.2026.1080p.WEB-DL.x264-GRP", DownloadURL: "web1080", SizeBytes: 8 * gb}
		blu1080 = indexers.Result{Title: "Film.2026.1080p.BluRay.x264-GRP", DownloadURL: "blu1080", SizeBytes: 12 * gb}
		web720  = indexers.Result{Title: "Film.2026.720p.WEB-DL.x264-GRP", DownloadURL: "web720", SizeBytes: 4 * gb}
		huge    = indexers.Result{Title: "Film.2026.1080p.BluRay.REMUX-GRP", DownloadURL: "huge", SizeBytes: 60 * gb}
		oldYear = indexers.Result{Title: "Film.2019.1080p.WEB-DL.x264-GRP", DownloadURL: "oldYear", SizeBytes: 8 * gb}
		noSize  = indexers.Result{Title: "Film.2026.1080p.WEB-DL.x264-NOSIZE", DownloadURL: "noSize"}
	)
	tests := []struct {
		name    string
		profile string // quality preset
		tier    quality.Tier
		size    int64
		results []indexers.Result
		want    string // DownloadURL, "" = nothing
	}{
		{"best smaller release the profile accepts", quality.Preset1080p, quality.TierWebDL2160p, 50 * gb, []indexers.Result{uhd40, web720, web1080, blu1080}, "blu1080"},
		{"a bigger release is never picked", quality.Preset1080p, quality.TierWebDL2160p, 50 * gb, []indexers.Result{huge}, ""},
		{"the profile decides what is accepted", quality.Preset720p, quality.TierWebDL2160p, 50 * gb, []indexers.Result{web1080, blu1080, web720}, "web720"},
		{"nothing smaller", quality.Preset1080p, quality.TierWebDL2160p, 5 * gb, []indexers.Result{web1080, blu1080}, ""},
		{"a higher quality is not a smaller version", quality.Preset4K, quality.TierWebDL1080p, 30 * gb, []indexers.Result{uhd40}, ""},
		{"the same quality is fine when it is clearly smaller", quality.Preset4K, quality.TierWebDL2160p, 50 * gb, []indexers.Result{uhd50, uhd40}, "uhd40"},
		{"a file of unknown quality is only compared by size", quality.Preset1080p, quality.TierUnknown, 50 * gb, []indexers.Result{web1080}, "web1080"},
		{"other years are skipped", quality.Preset1080p, quality.TierWebDL2160p, 50 * gb, []indexers.Result{oldYear}, ""},
		{"a release without a size is skipped", quality.Preset1080p, quality.TierWebDL2160p, 50 * gb, []indexers.Result{noSize}, ""},
		{"no results", quality.Preset1080p, quality.TierWebDL2160p, 50 * gb, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := quality.Presets()[tc.profile]
			best := pickSmallerResult(tc.results, profile, tc.tier, tc.size, 2026)
			got := ""
			if best != nil {
				got = best.DownloadURL
			}
			if got != tc.want {
				t.Fatalf("picked %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSmallerVersionPolicy(t *testing.T) {
	skip := organizer.ConflictSkip
	tests := []struct {
		name                       string
		replaces, current, dest    string
		wantPolicy                 organizer.ConflictPolicy
		wantKeepIsBetter, wantTake bool
	}{
		{"same name: overwritten in place", "/m/F/F.mkv", "/m/F/F.mkv", "/m/F/F.mkv", organizer.ConflictOverwrite, false, true},
		{"another name: the old file is removed afterwards", "/m/F/F [2160p].mkv", "/m/F/F [2160p].mkv", "/m/F/F.mkv", skip, true, true},
		{"not a smaller-version download", "", "/m/F/F.mkv", "/m/F/F.mkv", skip, true, false},
		{"the movie's file changed meanwhile", "/m/F/old.mkv", "/m/F/new.mkv", "/m/F/new.mkv", skip, true, false},
		{"the movie has no file now", "/m/F/old.mkv", "", "/m/F/F.mkv", skip, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isBetter := func() bool { return false }
			policy, gotBetter, take := smallerVersionPolicy(tc.replaces, tc.current, tc.dest, skip, isBetter)
			if policy != tc.wantPolicy || take != tc.wantTake || (gotBetter != nil) != tc.wantKeepIsBetter {
				t.Fatalf("got policy %v, isBetter set %v, remove old %v; want %v, %v, %v", policy, gotBetter != nil, take, tc.wantPolicy, tc.wantKeepIsBetter, tc.wantTake)
			}
		})
	}
}

func TestShrinkProblem(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "Film.mkv")
	if err := os.WriteFile(file, []byte("twelve bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		movie       library.Movie
		wantProblem bool
		wantSize    int64
	}{
		{"downloaded, file there", library.Movie{Status: library.StatusDownloaded, FilePath: file}, false, 12},
		{"missing", library.Movie{Status: library.StatusMissing}, true, 0},
		{"downloading", library.Movie{Status: library.StatusDownloading, FilePath: file}, true, 0},
		{"downloaded but no path", library.Movie{Status: library.StatusDownloaded}, true, 0},
		{"downloaded but the file is gone", library.Movie{Status: library.StatusDownloaded, FilePath: filepath.Join(dir, "gone.mkv")}, true, 0},
		{"the path is a folder", library.Movie{Status: library.StatusDownloaded, FilePath: dir}, true, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, size, problem := shrinkProblem(tc.movie)
			if (problem != "") != tc.wantProblem || size != tc.wantSize {
				t.Fatalf("got path %q, size %d, problem %q; want problem %v, size %d", path, size, problem, tc.wantProblem, tc.wantSize)
			}
		})
	}
}
