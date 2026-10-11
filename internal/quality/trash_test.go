package quality

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTrashProfileTiers(t *testing.T) {
	cases := []struct {
		name        string
		items       []TrashItem
		cutoff      string
		wantAllowed []Tier
		wantCutoff  Tier
	}{
		{
			name: "single qualities and a WEB group",
			items: []TrashItem{
				{Name: "Bluray-1080p", Allowed: true},
				{Name: "WEB 1080p", Allowed: true, Items: []string{"WEBRip-1080p", "WEBDL-1080p"}},
				{Name: "Bluray-720p", Allowed: true},
				{Name: "Remux-2160p", Allowed: false},
			},
			cutoff:      "Bluray-1080p",
			wantAllowed: []Tier{TierBluray720p, TierWebDL1080p, TierBluray1080p},
			wantCutoff:  TierBluray1080p,
		},
		{
			name: "cutoff on a group takes its best tier",
			items: []TrashItem{
				{Name: "Remux 1080p", Allowed: true, Items: []string{"Remux-1080p", "Bluray-1080p"}},
				{Name: "WEB 1080p", Allowed: true, Items: []string{"HDTV-1080p", "WEBRip-1080p", "WEBDL-1080p"}},
			},
			cutoff:      "Remux 1080p",
			wantAllowed: []Tier{TierHDTV1080p, TierWebDL1080p, TierBluray1080p, TierRemux1080p},
			wantCutoff:  TierRemux1080p,
		},
		{
			name: "Sonarr remux names",
			items: []TrashItem{
				{Name: "Bluray-2160p Remux", Allowed: true},
				{Name: "WEB 2160p", Allowed: true, Items: []string{"WEBRip-2160p", "WEBDL-2160p"}},
				{Name: "Raw-HD", Allowed: true},
			},
			cutoff:      "Bluray-2160p Remux",
			wantAllowed: []Tier{TierWebDL2160p, TierRemux2160p},
			wantCutoff:  TierRemux2160p,
		},
		{
			name:        "unknown cutoff falls back to the best allowed tier",
			items:       []TrashItem{{Name: "DVD", Allowed: true}, {Name: "SDTV", Allowed: true}},
			cutoff:      "Something else",
			wantAllowed: []Tier{TierSDTV, TierDVD},
			wantCutoff:  TierDVD,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tp := TrashProfile{Name: "x", Items: tc.items, Cutoff: tc.cutoff}
			allowed, cutoff, err := tp.Tiers()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(allowed, tc.wantAllowed) || cutoff != tc.wantCutoff {
				t.Errorf("got %v / %s, want %v / %s", allowed, cutoff, tc.wantAllowed, tc.wantCutoff)
			}
		})
	}
	if _, _, err := (TrashProfile{Name: "x", Items: []TrashItem{{Name: "BR-DISK", Allowed: true}}}).Tiers(); err == nil {
		t.Error("a profile with no usable quality should be refused")
	}
}

func TestTrashProfileFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/trash/radarr-profile-hd-bluray-web.json")
	if err != nil {
		t.Fatal(err)
	}
	tp, err := ParseTrashProfile(b)
	if err != nil {
		t.Fatal(err)
	}
	if tp.Name != "HD Bluray + WEB" || tp.CutoffFormatScore != 10000 || !tp.UpgradeAllowed || len(tp.FormatTrashIDs()) != 9 {
		t.Errorf("read %+v", tp)
	}
	allowed, cutoff, err := tp.Tiers()
	if err != nil || cutoff != TierBluray1080p || len(allowed) != 3 {
		t.Errorf("tiers %v %s %v", allowed, cutoff, err)
	}
	f := ImportedFormat{DefaultScore: 1700, Scores: map[string]int{"default": 1700, "anime-radarr": 5}}
	if tp.ScoreFor(f) != 1700 {
		t.Error("no score set: the default score")
	}
	tp.ScoreSet = "anime-radarr"
	if tp.ScoreFor(f) != 5 {
		t.Error("the profile's score set wins")
	}
	tp.ScoreSet = "missing"
	if tp.ScoreFor(f) != 1700 {
		t.Error("a missing score set falls back to the default")
	}
}

// TestEveryTrashProfileReads reads every TRaSH quality profile from a local
// checkout of the TRaSH Guides (MEDIARIUM_TRASH_JSON=…/Guides/docs/json).
func TestEveryTrashProfileReads(t *testing.T) {
	root := os.Getenv("MEDIARIUM_TRASH_JSON")
	if root == "" {
		t.Skip("MEDIARIUM_TRASH_JSON not set")
	}
	for _, app := range []FormatApp{AppRadarr, AppSonarr} {
		files, _ := filepath.Glob(filepath.Join(root, string(app), "quality-profiles", "*.json"))
		if len(files) < 10 {
			t.Fatalf("%s: only %d profiles found", app, len(files))
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			tp, err := ParseTrashProfile(b)
			if err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
				continue
			}
			allowed, cutoff, err := tp.Tiers()
			if err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
				continue
			}
			p := Profile{Name: tp.Name, Allowed: allowed, Cutoff: cutoff, MinFormatScore: tp.MinFormatScore, CutoffFormatScore: tp.CutoffFormatScore}
			if _, err := Validate(p); err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
			}
		}
		t.Logf("%s: %d profiles read", app, len(files))
	}
}
