package quality

import (
	"strings"
	"testing"
)

func TestBadRelease(t *testing.T) {
	cases := []struct {
		title string
		want  string // part of the reason, "" when the release is fine
	}{
		{"Night.Harbour.2021.1080p.BluRay.x264-GRP", ""},
		{"Night.Harbour.2021.2160p.UHD.BluRay.REMUX.HDR.TrueHD.Atmos-GRP", ""},
		{"Night.Harbour.2021.COMPLETE.UHD.BLURAY-GRP", "whole Blu-ray disc"},
		{"Night Harbour 2021 1080p BDMV-GRP", "whole Blu-ray disc"},
		{"Night.Harbour.2021.BD50.1080p-GRP", "whole Blu-ray disc"},
		{"Night.Harbour.2021.1080p.3D.HSBS.BluRay.x264-GRP", "3D release"},
		{"Night.Harbour.2021.1080p.BluRay.Half-OU.x264-GRP", "3D release"},
		{"Step.Up.3D.2010.1080p.BluRay.x264-GRP", ""}, // a title with 3D in it
		{"Night.Harbour.2021.2160p.AI.Upscaled.WEB-DL-GRP", "upscaled"},
		{"Night.Harbour.2014.BluRay.2160p.Ai.DTS-HD.MA.5.1.AAC.H265-GRP", "upscaled"},
		{"Night.Harbour.2014.2160p.AI.HDR10.DV.HEVC-GRP", "upscaled"},
		{"A.I.Artificial.Intelligence.2001.2160p.UHD.BluRay.REMUX-GRP", ""},
		{"AI.Love.You.2022.1080p.WEB-DL.x264-GRP", ""},
		{"Night.Harbour.2021.1080p.WEB-DL.HC.x264-GRP", "burned into the picture"},
		{"Night.Harbour.2021.1080p.HDRip.KORSUB.x264-GRP", "burned into the picture"},
		{"Night.Harbour.2021.1080p.BluRay.x264-YIFY", "low-quality re-encodes"},
		{"Night Harbour (2021) [1080p] [BluRay] [5.1] [YTS.MX]", "low-quality re-encodes"},
		{"Night.Harbour.2021.720p.BluRay.x264-aXXo", "low-quality re-encodes"},
		{"Night.Harbour.2021.1080p.WEB-DL.DDP5.1.H.264-NTb", ""},
	}
	for _, tc := range cases {
		got := BadRelease(tc.title)
		if (tc.want == "") != (got == "") || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("BadRelease(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

func TestSkipBadReleasesInAProfile(t *testing.T) {
	p := Presets()[Preset1080p]
	if !p.SkipBadReleases {
		t.Fatal("presets skip bad releases")
	}
	bad := "Night.Harbour.2021.1080p.BluRay.x264-YIFY"
	if ok, why := p.TitleAllowed(bad); ok || !strings.Contains(why, `"Skip bad releases"`) {
		t.Fatalf("allowed %v (%s)", ok, why)
	}
	p.SkipBadReleases = false
	if ok, _ := p.TitleAllowed(bad); !ok {
		t.Fatal("switched off, it should be allowed")
	}
	p.SkipBadReleases = true
	if p.Score("Night.Harbour.2021.1080p.BluRay.PROPER.x264-GRP") <= p.Score("Night.Harbour.2021.1080p.BluRay.x264-GRP") {
		t.Fatal("a PROPER should score higher")
	}
}
