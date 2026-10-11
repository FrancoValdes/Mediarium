package api

import (
	"reflect"
	"testing"

	"github.com/rdborg/mediarium/internal/animemap"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/parser"
)

// The numbering below is cut from what TheXEM and the anime list really say
// (see internal/animemap/testdata): Bleach's scene season 1 runs into season 2
// of the show, and Attack on Titan's third season is two AniDB entries.

func bleachNumbering() *animemap.Numbering {
	var rows []animemap.SceneEpisode
	for e := 1; e <= 20; e++ { // scene S01E01..20 are the show's S01E01..20
		rows = append(rows, animemap.SceneEpisode{SceneSeason: 1, SceneEpisode: e, SceneAbsolute: e, Season: 1, Episode: e, Absolute: e})
	}
	for e := 21; e <= 40; e++ { // scene S01E21..40 are the show's S02E01..20
		rows = append(rows, animemap.SceneEpisode{SceneSeason: 1, SceneEpisode: e, SceneAbsolute: e, Season: 2, Episode: e - 20, Absolute: e})
	}
	return &animemap.Numbering{Scene: rows}
}

func titanNumbering() *animemap.Numbering {
	return &animemap.Numbering{Entries: []animemap.Entry{
		{AniDB: 9541, AniList: 16498, MAL: 16498, Type: "TV", Season: 1, Titles: []string{"Shingeki no Kyojin", "Attack on Titan"}},
		{AniDB: 13241, AniList: 99147, Type: "TV", Season: 3, Titles: []string{"Shingeki no Kyojin Season 3", "Attack on Titan Season 3"}},
		{AniDB: 14444, AniList: 104578, Type: "TV", Season: 3, Offset: 12, Titles: []string{"Shingeki no Kyojin Season 3 Part 2", "Attack on Titan Season 3 Part 2"}},
		{AniDB: 14977, AniList: 110277, Type: "TV", Season: 4, Titles: []string{"Shingeki no Kyojin: The Final Season"}},
	}}
}

func episodeList(counts map[int]int) []library.Episode {
	var out []library.Episode
	for s := 1; s <= len(counts); s++ {
		for e := 1; e <= counts[s]; e++ {
			out = append(out, library.Episode{Season: s, Episode: e})
		}
	}
	return out
}

func TestMapNumbered(t *testing.T) {
	titanEpisodes := episodeList(map[int]int{1: 25, 2: 12, 3: 22, 4: 28})
	tests := []struct {
		name       string
		release    string
		num        *animemap.Numbering
		episodes   []library.Episode
		wantMapped bool
		season     int
		eps        []int
	}{
		{name: "scene episode is the show's episode of the next season", release: "Bleach.S01E21.720p.HDTV.x264-GRP", num: bleachNumbering(), wantMapped: true, season: 2, eps: []int{1}},
		{name: "scene episodes in one season", release: "Bleach.S01E21E22.720p.HDTV.x264-GRP", num: bleachNumbering(), wantMapped: true, season: 2, eps: []int{1, 2}},
		{name: "a scene episode that is the same stays as it is", release: "Bleach.S01E05.720p.HDTV.x264-GRP", num: bleachNumbering()},
		{name: "episodes TheXEM has no row for stay as they are", release: "Bleach.S05E01.720p.HDTV.x264-GRP", num: bleachNumbering()},
		{name: "episodes split over two seasons stay as they are", release: "Bleach.S01E19E20E21.720p.HDTV.x264-GRP", num: bleachNumbering()},
		{name: "a pack of a scene season that is two seasons of the show stays", release: "Bleach.S01.720p.BluRay.x264-GRP", num: bleachNumbering()},
		{name: "the number alone: scene absolute 25 is one episode", release: "[Group] Bleach - 25 (720p)", num: bleachNumbering(), episodes: episodeList(map[int]int{1: 20, 2: 20}), wantMapped: true, season: 2, eps: []int{5}},
		{name: "a name with no numbering to apply", release: "Show.S01E02.1080p.WEB-DL.x264-GRP", num: &animemap.Numbering{}},
		{name: "a special is left alone", release: "Bleach.S00E21.720p.HDTV.x264-GRP", num: bleachNumbering()},

		{name: "the part of a season its title names", release: "[SubsPlease] Shingeki no Kyojin Season 3 Part 2 - 05 (1080p) [ABCD1234]", num: titanNumbering(), episodes: titanEpisodes, wantMapped: true, season: 3, eps: []int{17}},
		{name: "a batch within an entry", release: "[Group] Shingeki no Kyojin Season 3 Part 2 - 01-04 (1080p)", num: titanNumbering(), episodes: titanEpisodes, wantMapped: true, season: 3, eps: []int{13, 14, 15, 16}},
		{name: "the English name of the entry", release: "[Group] Attack on Titan Season 3 Part 2 - 10 (1080p)", num: titanNumbering(), episodes: titanEpisodes, wantMapped: true, season: 3, eps: []int{22}},
		{name: "an entry number past the end of the season is not made up", release: "[Group] Shingeki no Kyojin Season 3 Part 2 - 11 (1080p)", num: titanNumbering(), episodes: titanEpisodes},
		{name: "the first entry of a show is its own numbering", release: "[Group] Shingeki no Kyojin - 05 (1080p)", num: titanNumbering(), episodes: titanEpisodes, wantMapped: true, season: 1, eps: []int{5}},
		{name: "a title that is no entry's name", release: "[Group] Something Else Entirely - 05 (1080p)", num: titanNumbering(), episodes: titanEpisodes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel := parser.Parse(tt.release)
			got, mapped := mapNumbered(rel, tt.num, tt.episodes)
			if mapped != tt.wantMapped {
				t.Fatalf("mapped = %v, want %v (parsed: season %d, episodes %v, absolute %v, title %q)", mapped, tt.wantMapped, rel.Season, rel.Episodes, rel.Absolute, rel.Title)
			}
			if !mapped {
				if !reflect.DeepEqual(got, rel) {
					t.Fatalf("a release that was not mapped must come back unchanged: %+v", got)
				}
				return
			}
			if got.Season != tt.season || !equalInts(got.Episodes, tt.eps) || got.Episode != tt.eps[0] {
				t.Fatalf("season %d, episodes %v (episode %d), want season %d, episodes %v", got.Season, got.Episodes, got.Episode, tt.season, tt.eps)
			}
		})
	}
}

func TestMapNumberedPack(t *testing.T) {
	// A scene season that is one season of the show is renumbered as a pack.
	n := &animemap.Numbering{Scene: []animemap.SceneEpisode{
		{SceneSeason: 2, SceneEpisode: 1, Season: 3, Episode: 1},
		{SceneSeason: 2, SceneEpisode: 2, Season: 3, Episode: 2},
	}}
	rel := parser.Parse("Show.S02.1080p.BluRay.x264-GRP")
	got, mapped := mapNumbered(rel, n, nil)
	if !mapped || got.Season != 3 || len(got.Episodes) != 0 {
		t.Fatalf("pack: %+v, %v", got, mapped)
	}
}

func TestNumberingQueries(t *testing.T) {
	tests := []struct {
		name     string
		num      *animemap.Numbering
		season   int
		episode  int
		absolute int
		want     []string
	}{
		{"nothing known, nothing extra", &animemap.Numbering{}, 1, 5, 5, nil},
		{"specials get none", bleachNumbering(), 0, 5, 5, nil},
		{"scene numbering that differs from the show's", bleachNumbering(), 2, 1, 21, []string{"Bleach S01E21"}},
		{"scene numbering that is the same adds nothing", bleachNumbering(), 1, 5, 5, nil},
		{"the part of a season: its name and the number within it", titanNumbering(), 3, 17, 50, []string{"Shingeki no Kyojin Season 3 Part 2 05", "Attack on Titan Season 3 Part 2 05", "Shingeki no Kyojin 50"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title := "Bleach"
			if tt.num.Entries != nil {
				title = "Attack on Titan"
			}
			got := numberingQueries(title, tt.num, tt.season, tt.episode, tt.absolute)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("queries = %q, want %q", got, tt.want)
			}
			if len(got) > maxNumberingQueries {
				t.Fatalf("%d queries is more than the limit", len(got))
			}
		})
	}
}

func TestMatchesShowUsesTheOtherNames(t *testing.T) {
	series := library.Series{Title: "Attack on Titan", Aliases: []string{"Shingeki no Kyojin", "Shingeki no Kyojin Season 3 Part 2", "SnK", "進撃の巨人"}}
	tests := []struct {
		release string
		want    bool
	}{
		{"Attack.on.Titan.S03E17.1080p.WEB.x264-GRP", true},
		{"[Group] Shingeki no Kyojin - 05 (1080p)", true},
		{"[Group] Shingeki no Kyojin Season 3 Part 2 - 05 (1080p)", true},
		{"[Group] SnK - 05 (1080p)", true},
		{"[Group] Shingeki no Kyojin Chronicle - 05 (1080p)", false},
		{"Other.Show.S01E02.1080p.WEB-DL.x264-GRP", false},
	}
	for _, tt := range tests {
		if got := matchesShow(tt.release, series); got != tt.want {
			t.Errorf("matchesShow(%q) = %v, want %v", tt.release, got, tt.want)
		}
	}
	// A name that reduces to nothing (all Japanese) is never a match for another all-Japanese release.
	if matchesShow("進撃の巨人.S01E01.1080p.WEB-DL.x264-GRP", series) {
		t.Error("a name with no Latin letters must not match")
	}
	// Without aliases nothing changed.
	if matchesShow("[Group] Shingeki no Kyojin - 05 (1080p)", library.Series{Title: "Attack on Titan"}) {
		t.Error("no aliases, no match")
	}
}
