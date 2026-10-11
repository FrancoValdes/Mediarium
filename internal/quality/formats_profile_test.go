package quality

import (
	"testing"

	"github.com/rdborg/mediarium/internal/parser"
)

func trashProfile(t *testing.T) Profile {
	t.Helper()
	x265 := loadTrash(t, "radarr-x265-hd.json", AppRadarr).Format
	amzn := loadTrash(t, "radarr-amzn.json", AppRadarr).Format
	hdr := loadTrash(t, "radarr-hdr.json", AppRadarr).Format
	x265.ID, amzn.ID, hdr.ID = 1, 2, 3
	p := Presets()[Preset1080p]
	p.UpgradeAllowed = true
	p.FormatScores = map[int64]int{1: -10000, 2: 100, 3: 0}
	p.MinFormatScore = 0
	p.CutoffFormatScore = 100
	return AttachFormats(p, map[int64]*CustomFormat{1: &x265, 2: &amzn, 3: &hdr})
}

func TestProfileFormatScores(t *testing.T) {
	p := trashProfile(t)
	if len(p.Formats) != 2 {
		t.Fatalf("a format scored 0 isn't used: %+v", p.Formats)
	}
	tests := []struct {
		title   string
		score   int
		allowed bool
	}{
		{"Night.Harbour.2020.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP", 100, true},
		{"Night.Harbour.2020.1080p.WEB-DL.DDP5.1.H.264-GRP", 0, true},
		{"Night.Harbour.2020.1080p.BluRay.x265-GRP", -10000, false}, // below the minimum of 0
	}
	for _, tt := range tests {
		score, _ := p.FormatScore(tt.title)
		ok, why := p.TitleAllowed(tt.title)
		if score != tt.score || ok != tt.allowed {
			t.Errorf("%s: score %d allowed %v (%s)", tt.title, score, ok, why)
		}
		if got := p.Score(tt.title); got != tt.score {
			t.Errorf("%s: ranking score %d", tt.title, got)
		}
	}
}

func TestUpgradesByFormatScore(t *testing.T) {
	p := trashProfile(t)
	plain := "Night.Harbour.2020.1080p.WEB-DL.DDP5.1.H.264-GRP"
	amzn := "Night.Harbour.2020.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP"
	tests := []struct {
		name        string
		tier        Tier
		current     string
		candidate   string
		wantUpgrade bool
	}{
		{"same quality, higher score", TierWebDL1080p, plain, amzn, true},
		{"same quality, same score", TierWebDL1080p, plain, plain, false},
		{"already at the format cutoff", TierWebDL1080p, amzn, "Night.Harbour.2020.1080p.AMZN.WEB-DL.DDP5.1.H.264-OTHER", false},
		{"lower quality never, whatever the score", TierBluray1080p, plain, amzn, false},
		{"release unknown: no score upgrade", TierWebDL1080p, "", amzn, false},
		{"better quality still an upgrade", TierWebDL1080p, amzn, "Night.Harbour.2020.1080p.BluRay.x264-GRP", true},
	}
	for _, tt := range tests {
		if got := p.IsUpgradeOver(tt.tier, tt.current, tt.candidate); got != tt.wantUpgrade {
			t.Errorf("%s: %v", tt.name, got)
		}
	}
	if !p.WantsFormatUpgrade(plain) || p.WantsFormatUpgrade(amzn) || p.WantsFormatUpgrade("") {
		t.Error("WantsFormatUpgrade")
	}
	p.UpgradeAllowed = false
	if p.WantsFormatUpgrade(plain) || p.IsUpgradeOver(TierWebDL1080p, plain, amzn) {
		t.Error("better versions off means no score upgrades either")
	}
}

func TestNoSeasonPacks(t *testing.T) {
	p := Presets()[Preset1080p]
	pack := "Salt.and.Static.S01.1080p.WEB-DL-GRP"
	if ok, _ := p.TitleAllowed(pack); !ok {
		t.Fatal("packs are allowed by default")
	}
	p.NoSeasonPacks = true
	if ok, why := p.TitleAllowed(pack); ok || why == "" {
		t.Fatal("a season pack was allowed")
	}
	for _, ok := range []string{"Salt.and.Static.S01E04.1080p.WEB-DL-GRP", "Salt.and.Static.S01E04E05.1080p.WEB-DL-GRP", "Night.Harbour.2020.1080p.WEB-DL-GRP"} {
		if allowed, _ := p.TitleAllowed(ok); !allowed {
			t.Errorf("%s was refused", ok)
		}
	}
	if !isSeasonPack(parser.Parse("Salt.and.Static.Season.2.1080p")) {
		t.Error("Season 2 is a pack")
	}
}
