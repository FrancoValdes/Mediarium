package animemap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The files in testdata are answers the real services gave on 2026-10-09:
//
//	xem_havemap.json                       GET thexem.info/map/havemap?origin=tvdb
//	xem_all_*.json                         GET thexem.info/map/all?id=<tvdb id>&origin=tvdb
//	anime_list_subset.json                 the entries for five shows cut out of Fribb's anime-list-mini.json
//	anilist_titles.json                    POST graphql.anilist.co for the AniList ids of those entries
//
// The tests never call the real services.

const testUA = "Mediarium/test (+https://mediarium.app)"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func testNorm(s string) string { return defaultNorm(s) }

func xemRows(t testing.TB, name string) []SceneEpisode {
	t.Helper()
	var ans xemAnswer
	if err := json.Unmarshal(fixture(t, name), &ans); err != nil {
		t.Fatal(err)
	}
	rows, _, err := parseAll(ans)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestParseAllReadsTheRealAnswers(t *testing.T) {
	tests := []struct {
		file      string
		wantRows  int
		wantFound bool
	}{
		{"xem_all_267440_attack_on_titan.json", 89, true},
		{"xem_all_305074_my_hero_academia.json", 171, true},
		{"xem_all_74796_bleach.json", 419, true},
		{"xem_all_81797_unknown.json", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			var ans xemAnswer
			if err := json.Unmarshal(fixture(t, tt.file), &ans); err != nil {
				t.Fatal(err)
			}
			rows, found, err := parseAll(ans)
			if err != nil || found != tt.wantFound || len(rows) != tt.wantRows {
				t.Fatalf("rows %d, found %v, err %v", len(rows), found, err)
			}
		})
	}
	// An answer that is not a success and not "no such show" is a problem to try again.
	if _, _, err := parseAll(xemAnswer{Result: "failure", Message: "database is busy"}); !errors.Is(err, ErrOffline) {
		t.Fatalf("err = %v", err)
	}
}

func TestMapSceneWithBleach(t *testing.T) {
	// TheXEM numbers Bleach's first 366 episodes as scene seasons 1 to 16 and
	// TheTVDB as seasons 1 to 16 with other splits: the scene season 1 runs
	// into TheTVDB's second.
	n := &Numbering{Scene: xemRows(t, "xem_all_74796_bleach.json")}
	tests := []struct {
		name       string
		season, ep int
		wantS      int
		wantE      int
		wantOK     bool
	}{
		{"scene S01E21 is TVDB S02E01", 1, 21, 2, 1, true},
		{"scene S01E20 stays in season 1", 1, 20, 1, 20, true},
		{"scene S01E22", 1, 22, 2, 2, true},
		{"an episode TheXEM has no row for", 40, 1, 0, 0, false},
		{"season 0 has none", 0, 1, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, e, ok := n.MapScene(tt.season, tt.ep)
			if ok != tt.wantOK || s != tt.wantS || e != tt.wantE {
				t.Fatalf("MapScene(%d,%d) = %d,%d,%v; want %d,%d,%v", tt.season, tt.ep, s, e, ok, tt.wantS, tt.wantE, tt.wantOK)
			}
		})
	}
	// A whole scene season that spreads over two seasons is not mapped as a pack.
	if _, ok := n.MapSceneSeason(1); ok {
		t.Error("scene season 1 is in two seasons of the show: a pack can't be mapped")
	}
	// Reverse: what the release for TVDB S02E01 is called.
	if r, ok := n.SceneOf(2, 1); !ok || r.SceneSeason != 1 || r.SceneEpisode != 21 {
		t.Errorf("SceneOf(2,1) = %+v, %v", r, ok)
	}
}

func TestMapAbsoluteWithMyHeroAcademia(t *testing.T) {
	// Here the scene numbers start over every season (scene season 2 episode 1
	// is absolute 1) while TheTVDB counts on (absolute 14).
	n := &Numbering{Scene: xemRows(t, "xem_all_305074_my_hero_academia.json")}
	if s, e, ok := n.MapScene(2, 1); !ok || s != 2 || e != 1 {
		t.Fatalf("scene S02E01 = %d,%d,%v", s, e, ok)
	}
	tests := []struct {
		name   string
		abs    int
		wantS  int
		wantE  int
		wantOK bool
	}{
		// 14 is no scene absolute number of more than one episode, and is TVDB's absolute 14.
		{"TVDB absolute 14 is season 2 episode 1", 14, 2, 1, true},
		{"scene absolute 5 repeats in every season, so the TVDB number decides", 5, 1, 5, true},
		{"zero is nothing", 0, 0, 0, false},
		{"past the end", 500, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, e, ok := n.MapAbsolute(tt.abs)
			if ok != tt.wantOK || s != tt.wantS || e != tt.wantE {
				t.Fatalf("MapAbsolute(%d) = %d,%d,%v; want %d,%d,%v", tt.abs, s, e, ok, tt.wantS, tt.wantE, tt.wantOK)
			}
		})
	}
}

func TestNumberingOfAShowWhoseNumbersAreTheSame(t *testing.T) {
	// Attack on Titan: every scene number is the TVDB number, so mapping
	// changes nothing and an absolute number is a single episode.
	n := &Numbering{Scene: xemRows(t, "xem_all_267440_attack_on_titan.json")}
	if s, e, ok := n.MapScene(3, 13); !ok || s != 3 || e != 13 {
		t.Errorf("MapScene(3,13) = %d,%d,%v", s, e, ok)
	}
	if s, e, ok := n.MapAbsolute(50); !ok || s != 3 || e != 13 {
		t.Errorf("MapAbsolute(50) = %d,%d,%v", s, e, ok)
	}
	if s, ok := n.MapSceneSeason(2); !ok || s != 2 {
		t.Errorf("MapSceneSeason(2) = %d,%v", s, ok)
	}
}

func TestEmptyAndNilNumberingMapNothing(t *testing.T) {
	for _, n := range []*Numbering{nil, {}} {
		if !n.Empty() {
			t.Error("should be empty")
		}
		if _, _, ok := n.MapScene(1, 1); ok {
			t.Error("MapScene")
		}
		if _, ok := n.MapSceneSeason(1); ok {
			t.Error("MapSceneSeason")
		}
		if _, _, ok := n.MapAbsolute(1); ok {
			t.Error("MapAbsolute")
		}
		if _, ok := n.SceneOf(1, 1); ok {
			t.Error("SceneOf")
		}
		if _, _, ok := n.EntryFor(1, 1); ok {
			t.Error("EntryFor")
		}
		if got := n.EntriesNamed(testNorm, "Anything"); got != nil {
			t.Error("EntriesNamed")
		}
		if _, ok := n.Primary(); ok {
			t.Error("Primary")
		}
		if got := n.Aliases(testNorm); got != nil {
			t.Error("Aliases")
		}
	}
}

// attackOnTitan builds the show's entries the way a refresh does: the list
// entries with the titles AniList gave.
func attackOnTitan(t testing.TB) *Numbering {
	t.Helper()
	l := NewLists(testUA, t.TempDir())
	idx, err := parseList(strings.NewReader(string(fixture(t, "anime_list_subset.json"))))
	if err != nil {
		t.Fatal(err)
	}
	l.idx, l.idxAt = idx, time.Now()
	entries, err := l.Lookup(context.Background(), 1429, 267440, false)
	if err != nil {
		t.Fatal(err)
	}
	titles, err := parseAniList(fixture(t, "anilist_titles.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		entries[i].Titles = titles[entries[i].AniList]
	}
	return &Numbering{Entries: entries}
}

func TestEntriesOfTheRealList(t *testing.T) {
	n := attackOnTitan(t)
	type row struct {
		AniDB, AniList, Season, Offset int
		Type                           string
	}
	var got []row
	for _, e := range n.Entries {
		got = append(got, row{e.AniDB, e.AniList, e.Season, e.Offset, e.Type})
	}
	want := []row{
		{9826, 18397, 0, 0, "OVA"},
		{9541, 16498, 1, 0, "TV"},
		{10944, 20958, 2, 0, "TV"},
		{13241, 99147, 3, 0, "TV"},
		{14444, 104578, 3, 12, "TV"}, // Season 3 Part 2: its episode 1 is season 3 episode 13
		{14977, 110277, 4, 0, "TV"},
		{16177, 131681, 4, 16, "TV"},
		{17303, 146984, 4, 28, "SPECIAL"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries:\n got %v\nwant %v", got, want)
	}
	// The film entries of the same show are not in the list.
	for _, e := range n.Entries {
		if e.AniDB == 10583 || e.AniDB == 13546 {
			t.Errorf("a film entry came through: %+v", e)
		}
	}
	if p, ok := n.Primary(); !ok || p.AniDB != 9541 || p.AniList != 16498 || p.MAL != 16498 {
		t.Errorf("Primary = %+v, %v", p, ok)
	}
}

func TestEntryForAndEntriesNamed(t *testing.T) {
	n := attackOnTitan(t)
	t.Run("an episode of season 3 part 2 is counted from 1 within its entry", func(t *testing.T) {
		e, ep, ok := n.EntryFor(3, 17)
		if !ok || e.AniDB != 14444 || ep != 5 {
			t.Fatalf("EntryFor(3,17) = %d, %d, %v", e.AniDB, ep, ok)
		}
		e, ep, ok = n.EntryFor(3, 12)
		if !ok || e.AniDB != 13241 || ep != 12 {
			t.Fatalf("EntryFor(3,12) = %d, %d, %v", e.AniDB, ep, ok)
		}
	})
	t.Run("specials and unknown seasons have no entry", func(t *testing.T) {
		if _, _, ok := n.EntryFor(0, 1); ok {
			t.Error("season 0")
		}
		if _, _, ok := n.EntryFor(9, 1); ok {
			t.Error("season 9")
		}
	})
	tests := []struct {
		title string
		want  []int // AniDB ids
	}{
		{"Shingeki no Kyojin Season 3 Part 2", []int{14444}},
		{"shingeki.no.kyojin.season.3.part.2", []int{14444}},
		{"Attack on Titan Season 3 Part 2", []int{14444}},
		{"Shingeki no Kyojin Season 2", []int{10944}},
		{"Shingeki no Kyojin: The Final Season", []int{14977}},
		{"Shingeki no Kyojin", []int{9541}},
		{"AoT", []int{9541}}, // a synonym of the first season only
		{"SnK 3", nil},       // two entries share it: it can't say which part
		{"Another Show", nil},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			var got []int
			for _, e := range n.EntriesNamed(testNorm, tt.title) {
				got = append(got, e.AniDB)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("EntriesNamed(%q) = %v, want %v", tt.title, got, tt.want)
			}
		})
	}
}

func TestAliasesLeaveOutTheShowsOwnTitleAndOtherAlphabets(t *testing.T) {
	n := attackOnTitan(t)
	got := n.Aliases(testNorm, "Attack on Titan")
	has := func(s string) bool {
		for _, a := range got {
			if a == s {
				return true
			}
		}
		return false
	}
	if !has("Shingeki no Kyojin") || !has("Shingeki no Kyojin Season 3 Part 2") || !has("Attack on Titan Season 3 Part 2") {
		t.Errorf("aliases: %v", got)
	}
	if has("Attack on Titan") {
		t.Error("the show's own title is not an alias of itself")
	}
	for _, a := range got {
		if latinCount(a) < 3 || otherAlphabet(a) {
			t.Errorf("an alias that can't be in a release name: %q", a)
		}
	}
	seen := map[string]bool{}
	for _, a := range got {
		if seen[strings.ToLower(a)] {
			t.Errorf("%q twice", a)
		}
		seen[strings.ToLower(a)] = true
	}
}

func TestParseAniListKeepsLatinNamesOnly(t *testing.T) {
	got, err := parseAniList(fixture(t, "anilist_titles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 11 {
		t.Fatalf("%d anime", len(got))
	}
	titles := got[104578]
	if len(titles) == 0 || titles[0] != "Shingeki no Kyojin Season 3 Part 2" || titles[1] != "Attack on Titan Season 3 Part 2" {
		t.Fatalf("titles: %v", titles)
	}
	for id, ts := range got {
		if len(ts) > maxTitles {
			t.Errorf("%d has %d names", id, len(ts))
		}
		for _, s := range ts {
			if latinCount(s) < 3 || otherAlphabet(s) {
				t.Errorf("%d: %q", id, s)
			}
		}
	}
	if _, err := parseAniList([]byte(`{"errors":[{"message":"Too Many Requests."}]}`)); !errors.Is(err, ErrOffline) {
		t.Errorf("err = %v", err)
	}
	if _, err := parseAniList([]byte(`<html>`)); !errors.Is(err, ErrOffline) {
		t.Errorf("err = %v", err)
	}
}

// ---- the clients, against local servers that replay the recorded answers

func TestXEMAsksPolitelyAndOnlyForShowsItHasMapsFor(t *testing.T) {
	var havemaps, alls atomic.Int32
	var ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		switch r.URL.Path {
		case "/map/havemap":
			havemaps.Add(1)
			if r.URL.Query().Get("origin") != "tvdb" {
				t.Errorf("origin = %q", r.URL.Query().Get("origin"))
			}
			w.Write(fixture(t, "xem_havemap.json"))
		case "/map/all":
			alls.Add(1)
			file := map[string]string{"74796": "xem_all_74796_bleach.json", "81797": "xem_all_81797_unknown.json"}[r.URL.Query().Get("id")]
			if file == "" {
				http.NotFound(w, r)
				return
			}
			w.Write(fixture(t, file))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cache := t.TempDir()
	x := NewXEM(testUA, cache)
	x.SetBase(srv.URL)
	ctx := context.Background()

	have, err := x.HaveMap(ctx, 74796)
	if err != nil || !have {
		t.Fatalf("Bleach is in the list: %v, %v", have, err)
	}
	if have, _ := x.HaveMap(ctx, 81797); have {
		t.Fatal("One Piece is not in the list")
	}
	if havemaps.Load() != 1 {
		t.Fatalf("the list was fetched %d times", havemaps.Load())
	}
	if ua.Load() != testUA {
		t.Fatalf("User-Agent = %v", ua.Load())
	}

	// A new client with the same cache folder reads the saved list, no request.
	x2 := NewXEM(testUA, cache)
	x2.SetBase(srv.URL)
	if have, err := x2.HaveMap(ctx, 74796); err != nil || !have {
		t.Fatalf("from the cache: %v, %v", have, err)
	}
	if havemaps.Load() != 1 {
		t.Fatalf("the saved list was not used: %d requests", havemaps.Load())
	}

	rows, found, err := x.All(ctx, 74796)
	if err != nil || !found || len(rows) != 419 {
		t.Fatalf("All: %d rows, %v, %v", len(rows), found, err)
	}
	if _, found, err := x.All(ctx, 81797); err != nil || found {
		t.Fatalf("an unknown show is not an error: %v, %v", found, err)
	}
}

func TestXEMWhenItCannotBeReached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Cloudflare says no", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	x := NewXEM(testUA, "")
	x.SetBase(srv.URL)
	if _, err := x.HaveMap(context.Background(), 1); !errors.Is(err, ErrOffline) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := x.All(context.Background(), 1); !errors.Is(err, ErrOffline) {
		t.Fatalf("err = %v", err)
	}
	// And a week-old saved list is used when the service is down.
	cache := t.TempDir()
	if err := os.WriteFile(filepath.Join(cache, "xem-havemap.json"), fixture(t, "xem_havemap.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(cache, "xem-havemap.json"), old, old); err != nil {
		t.Fatal(err)
	}
	x = NewXEM(testUA, cache)
	x.SetBase(srv.URL)
	if have, err := x.HaveMap(context.Background(), 74796); err != nil || !have {
		t.Fatalf("stale list: %v, %v", have, err)
	}
}

func TestListsDownloadsOnceAndChecksWithTheETag(t *testing.T) {
	var full, notModified atomic.Int32
	var ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		if r.Header.Get("If-None-Match") == `"v1"` {
			notModified.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		full.Add(1)
		w.Header().Set("ETag", `"v1"`)
		w.Write(fixture(t, "anime_list_subset.json"))
	}))
	defer srv.Close()
	cache := t.TempDir()
	l := NewLists(testUA, cache)
	l.SetURL(srv.URL)
	ctx := context.Background()

	entries, err := l.Lookup(ctx, 1429, 267440, false)
	if err != nil || len(entries) != 8 {
		t.Fatalf("%d entries, %v", len(entries), err)
	}
	if ua.Load() != testUA {
		t.Fatalf("User-Agent = %v", ua.Load())
	}
	// Within the day nothing is asked, even by a new reader.
	l2 := NewLists(testUA, cache)
	l2.SetURL(srv.URL)
	if _, err := l2.Lookup(ctx, 65930, 305074, false); err != nil {
		t.Fatal(err)
	}
	if full.Load() != 1 || notModified.Load() != 0 {
		t.Fatalf("full %d, not modified %d", full.Load(), notModified.Load())
	}
	// A manual refresh asks whether it changed, and the answer is "no".
	l3 := NewLists(testUA, cache)
	l3.SetURL(srv.URL)
	if _, err := l3.Lookup(ctx, 65930, 305074, true); err != nil {
		t.Fatal(err)
	}
	if full.Load() != 1 || notModified.Load() != 1 {
		t.Fatalf("full %d, not modified %d", full.Load(), notModified.Load())
	}
}

func TestListsLookupFindsShowsByTMDBThenTVDB(t *testing.T) {
	l := NewLists(testUA, t.TempDir())
	idx, err := parseList(strings.NewReader(string(fixture(t, "anime_list_subset.json"))))
	if err != nil {
		t.Fatal(err)
	}
	l.idx, l.idxAt = idx, time.Now()
	ctx := context.Background()
	tests := []struct {
		name        string
		tmdb, tvdb  int
		wantAniDB   []int
		wantSeasons []int
	}{
		{"by TMDB id", 65930, 0, []int{12344, 11739, 12233, 13485, 14416, 15672, 15484, 16793, 17356, 17931, 18921, 20039}, nil},
		{"Bleach: TMDB splits its seasons differently from TVDB", 30984, 74796, []int{2369, 15449, 17765, 18220, 19079}, []int{1, 2, 2, 2, 2}},
		// Every entry of that show names a TMDB id, so the TVDB id alone is not trusted for them.
		{"an entry that names another TMDB show is not matched by TVDB id", 0, 267440, nil, nil},
		{"a show the list does not have", 999999, 999999, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := l.Lookup(ctx, tt.tmdb, tt.tvdb, false)
			if err != nil {
				t.Fatal(err)
			}
			var ids, seasons []int
			for _, e := range got {
				ids = append(ids, e.AniDB)
				seasons = append(seasons, e.Season)
			}
			if tt.wantAniDB == nil && len(got) == 0 {
				return
			}
			if !reflect.DeepEqual(sortedCopy(ids), sortedCopy(tt.wantAniDB)) {
				t.Fatalf("AniDB ids %v, want %v", ids, tt.wantAniDB)
			}
			if tt.wantSeasons != nil && !reflect.DeepEqual(seasons, tt.wantSeasons) {
				t.Fatalf("seasons %v, want %v", seasons, tt.wantSeasons)
			}
		})
	}
	// Bleach's later parts carry their offsets within TMDB's second season.
	got, _ := l.Lookup(ctx, 30984, 74796, false)
	offsets := map[int]int{}
	for _, e := range got {
		offsets[e.AniDB] = e.Offset
	}
	if offsets[15449] != 0 || offsets[17765] != 13 || offsets[18220] != 26 || offsets[19079] != 40 {
		t.Fatalf("offsets: %v", offsets)
	}
}

func sortedCopy(in []int) []int {
	out := append([]int(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestListsOfflineUsesTheSavedCopyAndRefusesAJunkDownload(t *testing.T) {
	cache := t.TempDir()
	// Nothing saved and no connection: an offline answer.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 502) }))
	defer down.Close()
	l := NewLists(testUA, cache)
	l.SetURL(down.URL)
	if _, err := l.Lookup(context.Background(), 1429, 0, false); !errors.Is(err, ErrOffline) {
		t.Fatalf("err = %v", err)
	}
	// A copy, however old, is used when the download fails.
	p := filepath.Join(cache, "anime-list-mini.json")
	if err := os.WriteFile(p, fixture(t, "anime_list_subset.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	_ = os.Chtimes(p, old, old)
	l = NewLists(testUA, cache)
	l.SetURL(down.URL)
	got, err := l.Lookup(context.Background(), 1429, 0, false)
	if err != nil || len(got) == 0 {
		t.Fatalf("saved copy: %d entries, %v", len(got), err)
	}
	// A page that is not the list does not replace it.
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>rate limited</html>") }))
	defer junk.Close()
	l = NewLists(testUA, cache)
	l.SetURL(junk.URL)
	if got, err := l.Lookup(context.Background(), 1429, 0, true); err != nil || len(got) == 0 {
		t.Fatalf("after a junk answer: %d entries, %v", len(got), err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"anidb_id"`) {
		t.Fatal("the saved copy was replaced by the junk")
	}
}

func TestAniListChunksAndHandlesBusy(t *testing.T) {
	var asked [][]int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string `json:"query"`
			Variables struct {
				IDs     []int `json:"ids"`
				PerPage int   `json:"perPage"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !strings.Contains(body.Query, "id_in") {
			t.Errorf("request: %v %+v", err, body)
		}
		if r.Header.Get("User-Agent") != testUA || r.Method != http.MethodPost {
			t.Errorf("method %s, UA %q", r.Method, r.Header.Get("User-Agent"))
		}
		asked = append(asked, body.Variables.IDs)
		w.Write(fixture(t, "anilist_titles.json"))
	}))
	defer srv.Close()
	a := NewAniList(testUA)
	a.SetURL(srv.URL)
	ids := make([]int, 120)
	for i := range ids {
		ids[i] = i + 1
	}
	got, err := a.Titles(context.Background(), ids)
	if err != nil || len(got) != 11 {
		t.Fatalf("%d anime, %v", len(got), err)
	}
	if len(asked) != 3 || len(asked[0]) != 50 || len(asked[2]) != 20 {
		t.Fatalf("requests: %d, sizes %d %d %d", len(asked), len(asked[0]), len(asked[1]), len(asked[2]))
	}

	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer busy.Close()
	a = NewAniList(testUA)
	a.SetURL(busy.URL)
	_, err = a.Titles(context.Background(), []int{1})
	if !errors.Is(err, ErrOffline) || !strings.Contains(err.Error(), "60 seconds") {
		t.Fatalf("err = %v", err)
	}
}

func TestGateSpacesRequests(t *testing.T) {
	g := gate{gap: 40 * time.Millisecond}
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := g.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Fatalf("three requests took %v", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.wait(ctx); err == nil {
		t.Fatal("a cancelled wait should stop")
	}
}
