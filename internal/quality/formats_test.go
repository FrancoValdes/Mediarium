package quality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTrash(t *testing.T, name string, app FormatApp) ImportedFormat {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "trash", name))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := ParseFormats(b, app)
	if err != nil || len(fs) != 1 {
		t.Fatalf("%s: %v %d", name, err, len(fs))
	}
	if p := fs[0].Format.Problems(); len(p) > 0 {
		t.Fatalf("%s: %v", name, p)
	}
	return fs[0]
}

// TRaSH formats, unchanged, against release names: the same answers
// Radarr and Sonarr give.
func TestTrashFormatsMatchLikeRadarrAndSonarr(t *testing.T) {
	tests := []struct {
		file    string
		app     FormatApp
		release string
		want    bool
	}{
		// x265 (HD): x265 is required, and the resolution is required NOT to be 2160p.
		{"radarr-x265-hd.json", AppRadarr, "Night.Harbour.2020.1080p.BluRay.x265-GRP", true},
		{"radarr-x265-hd.json", AppRadarr, "Night.Harbour.2020.1080p.WEB-DL.HEVC-GRP", true},
		{"radarr-x265-hd.json", AppRadarr, "Night.Harbour.2020.2160p.BluRay.x265-GRP", false},
		{"radarr-x265-hd.json", AppRadarr, "Night.Harbour.2020.1080p.BluRay.x264-GRP", false},
		// AMZN: the tag is required; the source must be WEB-DL or WEBRip (either).
		{"radarr-amzn.json", AppRadarr, "Night.Harbour.2020.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP", true},
		{"radarr-amzn.json", AppRadarr, "Night.Harbour.2020.1080p.AMZN.WEBRip.x264-GRP", true},
		{"radarr-amzn.json", AppRadarr, "Night.Harbour.2020.1080p.AMZN.BluRay.x264-GRP", false},
		{"radarr-amzn.json", AppRadarr, "Night.Harbour.2020.1080p.WEB-DL.DDP5.1.H.264-GRP", false},
		// BR-DISK: look-aheads and a look-behind, which Go's own regexp can't run.
		{"radarr-br-disk.json", AppRadarr, "Night.Harbour.2019.1080p.Blu-ray.AVC.DTS-HD.MA.5.1-GRP", true},
		{"radarr-br-disk.json", AppRadarr, "Night.Harbour.2019.COMPLETE.UHD.BLURAY-GRP", true},
		{"radarr-br-disk.json", AppRadarr, "Night.Harbour.2019.1080p.BluRay.x264-GRP", false},
		{"radarr-br-disk.json", AppRadarr, "Night.Harbour.2019.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP", false},
		// HDR: any of its HDR tags.
		{"radarr-hdr.json", AppRadarr, "Night.Harbour.2021.2160p.UHD.BluRay.x265.HDR.DTS-HD.MA-GRP", true},
		{"radarr-hdr.json", AppRadarr, "Night.Harbour.2021.2160p.WEB-DL.HDR10.H.265-GRP", true},
		{"radarr-hdr.json", AppRadarr, "Night.Harbour.2021.1080p.WEB-DL.H.264-GRP", false},
		// 1080p by resolution.
		{"radarr-1080p.json", AppRadarr, "Night.Harbour.2021.1080p.WEB-DL-GRP", true},
		{"radarr-1080p.json", AppRadarr, "Night.Harbour.2021.720p.WEB-DL-GRP", false},
		// DTS-HD MA by audio tag.
		{"radarr-dts-hd-ma.json", AppRadarr, "Night.Harbour.2021.1080p.BluRay.DTS-HD.MA.5.1.x264-GRP", true},
		{"radarr-dts-hd-ma.json", AppRadarr, "Night.Harbour.2021.1080p.BluRay.DTS.x264-GRP", false},
		// Sonarr's release type: a season pack, not a single episode.
		{"sonarr-season-pack.json", AppSonarr, "Salt.and.Static.S01.1080p.WEB-DL-GRP", true},
		{"sonarr-season-pack.json", AppSonarr, "Salt.and.Static.S01E04.1080p.WEB-DL-GRP", false},
	}
	for _, tt := range tests {
		f := loadTrash(t, tt.file, tt.app)
		if got := f.Format.Matches(InputFor(tt.release)); got != tt.want {
			t.Errorf("%s / %s: %v, want %v", tt.file, tt.release, got, tt.want)
		}
	}
}

func TestTrashImportDetails(t *testing.T) {
	f := loadTrash(t, "radarr-x265-hd.json", "")
	if f.Format.App != AppRadarr || f.Format.TrashID != "dc98083864ea246d05a42df0d05f81cc" || !f.HasScore || f.DefaultScore != -10000 {
		t.Fatalf("x265: %+v", f)
	}
	if s := loadTrash(t, "sonarr-season-pack.json", ""); s.Format.App != AppSonarr {
		t.Fatalf("a release-type condition makes it a Sonarr format: %s", s.Format.App)
	}
	if a := loadTrash(t, "radarr-amzn.json", AppRadarr); a.HasScore {
		t.Fatal("AMZN has no TRaSH score")
	}
}

func TestParseFormatsFromTheRadarrAPI(t *testing.T) {
	// The API's shape: fields as a list of name/value pairs, several formats.
	data := `[{"name":"Big Files","specifications":[{"name":"Over 30 GB","implementation":"SizeSpecification","negate":false,"required":true,"fields":[{"name":"min","value":30},{"name":"max","value":0}]}]},
	          {"name":"Not English","specifications":[{"name":"English","implementation":"LanguageSpecification","negate":true,"required":true,"fields":[{"name":"value","value":1},{"name":"exceptLanguage","value":false}]}]}]`
	fs, err := ParseFormats([]byte(data), AppRadarr)
	if err != nil || len(fs) != 2 {
		t.Fatalf("%v %d", err, len(fs))
	}
	big := fs[0].Format
	if !big.Matches(FormatInput{Title: "x", SizeBytes: 40 << 30}) || big.Matches(FormatInput{Title: "x", SizeBytes: 10 << 30}) || big.Matches(InputFor("x")) {
		t.Fatal("size condition")
	}
	notEnglish := fs[1].Format
	if notEnglish.Matches(InputFor("Night.Harbour.2020.1080p.WEB-DL-GRP")) {
		t.Fatal("an untagged release counts as English")
	}
	if !notEnglish.Matches(InputFor("Night.Harbour.2020.ITALIAN.1080p.WEB-DL-GRP")) {
		t.Fatal("an Italian release isn't English")
	}
}

func TestParseFormatsRefusesNonsense(t *testing.T) {
	for _, bad := range []string{``, `{}`, `{"name":"x"}`, `{"name":"","specifications":[{"implementation":"ReleaseTitleSpecification"}]}`, `[1,2]`, `{"name":"x","specifications":[{"name":"a","implementation":"ReleaseTitleSpecification","fields":"nope"}]}`} {
		if _, err := ParseFormats([]byte(bad), AppRadarr); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	// A pattern that can't be read, or an unknown condition, is accepted but
	// reported, and never matches.
	fs, err := ParseFormats([]byte(`{"name":"Broken","specifications":[{"name":"bad","implementation":"ReleaseTitleSpecification","required":true,"fields":{"value":"(unclosed"}},{"name":"odd","implementation":"SomethingNewSpecification","fields":{"value":1}}]}`), AppRadarr)
	if err != nil {
		t.Fatal(err)
	}
	if p := fs[0].Format.Problems(); len(p) != 2 {
		t.Fatalf("problems: %v", p)
	}
	if fs[0].Format.Matches(InputFor("(unclosed")) {
		t.Fatal("a broken format matched")
	}
}

// A pattern that backtracks forever is cut off rather than holding up a search.
func TestRunawayPatternIsStopped(t *testing.T) {
	fs, err := ParseFormats([]byte(`{"name":"Slow","specifications":[{"name":"slow","implementation":"ReleaseTitleSpecification","required":true,"fields":{"value":"^(a+)+$"}}]}`), AppRadarr)
	if err != nil {
		t.Fatal(err)
	}
	title := ""
	for range 40 {
		title += "a"
	}
	title += "!"
	if fs[0].Format.Matches(InputFor(title)) {
		t.Fatal("matched")
	}
}

// Every TRaSH format reads without a problem. Skipped unless pointed at a
// checkout of the TRaSH Guides (MEDIARIUM_TRASH_JSON=…/Guides/docs/json).
func TestEveryTrashFormatReads(t *testing.T) {
	root := os.Getenv("MEDIARIUM_TRASH_JSON")
	if root == "" {
		t.Skip("MEDIARIUM_TRASH_JSON not set")
	}
	for _, app := range []FormatApp{AppRadarr, AppSonarr} {
		files, _ := filepath.Glob(filepath.Join(root, string(app), "cf", "*.json"))
		if len(files) < 100 {
			t.Fatalf("%s: only %d formats found", app, len(files))
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			fs, err := ParseFormats(b, app)
			if err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
				continue
			}
			for _, p := range fs[0].Format.Problems() {
				if !strings.Contains(p, "indexer flags") {
					t.Errorf("%s: %s", filepath.Base(f), p)
				}
			}
			fs[0].Format.Matches(InputFor("Night.Harbour.2021.2160p.UHD.BluRay.REMUX.HDR.DV.TrueHD.Atmos.7.1-GRP"))
		}
		t.Logf("%s: %d formats read", app, len(files))
	}
}

// A format with the "Original" language condition (-2) matches a release that
// is in the language the title was made in, and nothing when that language
// isn't known.
func TestOriginalLanguageCondition(t *testing.T) {
	f := &CustomFormat{Name: "Original", App: AppRadarr, Specs: []FormatSpec{{Name: "Original", Kind: SpecLanguage, Required: true, Value: "-2"}}}
	not := &CustomFormat{Name: "Not original", App: AppRadarr, Specs: []FormatSpec{{Name: "Original", Kind: SpecLanguage, Negate: true, Required: true, Value: "-2"}}}
	cases := []struct {
		title, original string
		want, wantNot   bool
	}{
		{"Movie.2020.JAPANESE.1080p.BluRay.x264-GRP", "Japanese", true, false},
		{"Movie.2020.GERMAN.1080p.BluRay.x264-GRP", "Japanese", false, true},
		{"Movie.2020.1080p.BluRay.x264-GRP", "English", true, false}, // untagged is English
		{"Movie.2020.1080p.BluRay.x264-GRP", "", false, false},       // unknown original: neither matches
	}
	for _, tc := range cases {
		in := InputFor(tc.title)
		in.OriginalLanguage = tc.original
		if got := f.Matches(in); got != tc.want {
			t.Errorf("Original on %q (original %q) = %v, want %v", tc.title, tc.original, got, tc.want)
		}
		if got := not.Matches(in); got != tc.wantNot {
			t.Errorf("Not original on %q (original %q) = %v, want %v", tc.title, tc.original, got, tc.wantNot)
		}
	}
}
