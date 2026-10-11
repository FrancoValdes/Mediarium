package quality

import (
	"strings"
	"testing"
)

func TestSizeLimitsCheck(t *testing.T) {
	on := SizeLimits{Enabled: true}
	custom := SizeLimits{Enabled: true, Limits: map[Tier]SizeLimit{TierWebDL1080p: {Min: 10, Max: 50}, TierHDTV720p: {Min: 0, Max: 0}}}
	gb := int64(1) << 30
	tests := []struct {
		name     string
		table    SizeLimits
		tier     Tier
		size     int64
		runtime  float64
		episodes int
		want     string // a part of the reason, "" = passes
	}{
		{"off table never refuses", SizeLimits{}, TierWebDL1080p, 1 * mb, 100, 0, ""},
		{"fine 1080p WEB movie", on, TierWebDL1080p, 6 * gb, 120, 0, ""},
		{"1080p WEB movie too small", on, TierWebDL1080p, 200 * mb, 120, 0, "too small for WEBDL-1080p"},
		{"1080p WEB movie too large", on, TierWebDL1080p, 40 * gb, 120, 0, "too large for WEBDL-1080p"},
		{"just inside the minimum", on, TierWebDL1080p, 4 * 120 * mb, 120, 0, ""},
		{"just inside the maximum", on, TierWebDL1080p, 130 * 120 * mb, 120, 0, ""},
		{"remux has no maximum", on, TierRemux1080p, 90 * gb, 120, 0, ""},
		{"remux minimum applies", on, TierRemux1080p, 2 * gb, 120, 0, "too small for Remux-1080p"},
		{"size unknown passes", on, TierWebDL1080p, 0, 120, 0, ""},
		{"runtime unknown passes", on, TierWebDL1080p, 100 * mb, 0, 0, ""},
		{"quality unknown has no row", on, TierUnknown, 1 * mb, 120, 0, ""},
		{"one episode of 45 minutes", on, TierWebDL1080p, 1 * gb, 45, 1, ""},
		{"two episodes in one file are counted twice", on, TierWebDL1080p, 20 * gb, 45, 2, "too large"},
		{"three episodes, fine", on, TierWebDL1080p, 3 * gb, 45, 3, ""},
		{"season pack judged at three episodes for the minimum", on, TierWebDL1080p, 300 * mb, 45, SeasonPack, "too small"},
		{"season pack is not judged for the maximum", on, TierWebDL1080p, 60 * gb, 45, SeasonPack, ""},
		{"own limit replaces the default", custom, TierWebDL1080p, 7 * gb, 120, 0, "too large"},
		{"own limit lets a fitting size through", custom, TierWebDL1080p, 5 * gb, 120, 0, ""},
		{"own 0 and 0 means no limits", custom, TierHDTV720p, 1 * mb, 120, 0, ""},
		{"too small reads the minimum", custom, TierWebDL1080p, 500 * mb, 120, 0, "the smallest is 10 MB a minute"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.table.Check(tc.tier, tc.size, tc.runtime, tc.episodes)
			if tc.want == "" && got != "" {
				t.Fatalf("expected a pass, got %q", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("reason = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestDefaultSizeLimitsCoverEveryTier(t *testing.T) {
	d := DefaultSizeLimits()
	for _, tier := range SizeTiers() {
		l, ok := d[tier]
		if !ok {
			t.Errorf("no default for %s", tier)
		}
		if l.Max != 0 && l.Max < l.Min {
			t.Errorf("%s: default max %v is below min %v", tier, l.Max, l.Min)
		}
	}
	if len(d) != len(SizeTiers()) {
		t.Errorf("%d defaults for %d tiers", len(d), len(SizeTiers()))
	}
}

// The default table must not refuse what the built-in minimums already
// accept at a believable size: a typical release of each quality passes.
func TestDefaultsAcceptTypicalReleases(t *testing.T) {
	on := SizeLimits{Enabled: true}
	gb := int64(1) << 30
	typical := map[Tier]int64{
		TierWebDL480p: 1 * gb, TierHDTV720p: 3 * gb, TierWebDL720p: 3 * gb, TierBluray720p: 5 * gb,
		TierWebDL1080p: 8 * gb, TierBluray1080p: 14 * gb, TierRemux1080p: 28 * gb,
		TierWebDL2160p: 18 * gb, TierBluray2160p: 40 * gb, TierRemux2160p: 60 * gb,
	}
	for tier, size := range typical {
		if reason := on.Check(tier, size, 120, 0); reason != "" {
			t.Errorf("%s at %d GB for a 2 hour film: %s", tier, size/gb, reason)
		}
	}
}

func TestSizeLimitsValidate(t *testing.T) {
	tests := []struct {
		name    string
		limits  map[Tier]SizeLimit
		wantErr string
	}{
		{"none", nil, ""},
		{"good", map[Tier]SizeLimit{TierWebDL1080p: {Min: 4, Max: 100}}, ""},
		{"no maximum", map[Tier]SizeLimit{TierWebDL1080p: {Min: 4, Max: 0}}, ""},
		{"max below min", map[Tier]SizeLimit{TierWebDL1080p: {Min: 50, Max: 10}}, "can't be smaller"},
		{"negative", map[Tier]SizeLimit{TierWebDL1080p: {Min: -1}}, "below 0"},
		{"huge", map[Tier]SizeLimit{TierWebDL1080p: {Max: 99999}}, "can't be above"},
		{"unknown quality", map[Tier]SizeLimit{"Betamax": {Min: 1}}, "not a quality"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := SizeLimits{Limits: tc.limits}.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
