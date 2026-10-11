package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rdborg/mediarium/internal/library"
)

// fakeNumberingServices replays what TheXEM, the anime list and AniList really
// answered (the files are kept with the animemap package's tests).
func fakeNumberingServices(t *testing.T, down *atomic.Bool) (xem, lists, anilist string) {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "animemap", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	serve := func(h http.HandlerFunc) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if down != nil && down.Load() {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			h(w, r)
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	xem = serve(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/map/havemap":
			w.Write(read("xem_havemap.json"))
		case r.URL.Query().Get("id") == "267440":
			w.Write(read("xem_all_267440_attack_on_titan.json"))
		case r.URL.Query().Get("id") == "74796":
			w.Write(read("xem_all_74796_bleach.json"))
		default:
			w.Write(read("xem_all_81797_unknown.json"))
		}
	})
	lists = serve(func(w http.ResponseWriter, r *http.Request) { w.Write(read("anime_list_subset.json")) })
	anilist = serve(func(w http.ResponseWriter, r *http.Request) { w.Write(read("anilist_titles.json")) })
	return xem, lists, anilist
}

// addTitanShow adds Attack on Titan as an anime, with the episode counts TMDB gives.
func addTitanShow(t *testing.T, env *tvAutoEnv) int64 {
	t.Helper()
	var eps []library.Episode
	for s, n := range map[int]int{1: 25, 2: 12, 3: 22, 4: 30} {
		for e := 1; e <= n; e++ {
			eps = append(eps, library.Episode{Season: s, Episode: e, AirDate: "2020-01-01"})
		}
	}
	sr, err := env.server.MovieRepo.AddSeries(library.Series{TMDBID: 1429, Title: "Attack on Titan", Year: 2013, Monitored: true, SeriesType: library.SeriesAnime}, eps)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.server.MovieRepo.SetSeriesExternalIDs(sr.ID, 267440, "", ""); err != nil {
		t.Fatal(err)
	}
	return sr.ID
}

func TestRefreshMappingShowsIdsLinksAndEntries(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	xem, lists, anilist := fakeNumberingServices(t, nil)
	env.server.TestSetAnimeServices(xem, lists, anilist)
	id := addTitanShow(t, env)
	url := fmt.Sprintf("%s/api/series/%d/numbering", env.baseURL, id)

	before := getJSON[map[string]any](t, env.client, url)
	if before["sceneState"] != "" || before["anilist"] != nil || before["lookup"] != true {
		t.Fatalf("before any lookup: %+v", before)
	}

	got := postJSON[map[string]any](t, env.client, url+"/refresh", nil, http.StatusOK)
	if got["anilist"] != float64(16498) || got["anidb"] != float64(9541) || got["mal"] != float64(16498) {
		t.Fatalf("ids: %+v", got)
	}
	if got["anilistUrl"] != "https://anilist.co/anime/16498" || got["anidbUrl"] != "https://anidb.net/anime/9541" || got["malUrl"] != "https://myanimelist.net/anime/16498" {
		t.Fatalf("links: %v %v %v", got["anilistUrl"], got["anidbUrl"], got["malUrl"])
	}
	if got["sceneState"] != "mapped" || got["listsState"] != "found" || got["sceneRows"] != float64(89) || got["sceneDiffers"] != float64(0) {
		t.Fatalf("states: %+v", got)
	}
	if entries := got["entries"].([]any); len(entries) != 8 {
		t.Fatalf("entries: %v", entries)
	}
	if names := got["names"].([]any); len(names) == 0 || len(names) > 12 {
		t.Fatalf("names: %v", names)
	}
	if note, _ := got["note"].(string); note != "" {
		t.Fatalf("note: %q", note)
	}
	// What was saved is what the page reads.
	again := getJSON[map[string]any](t, env.client, url)
	if again["anilist"] != float64(16498) || again["checkedAt"] == nil {
		t.Fatalf("saved: %+v", again)
	}
}

func TestRefreshMappingWhenTheServicesAreDown(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	var down atomic.Bool
	xem, lists, anilist := fakeNumberingServices(t, &down)
	env.server.TestSetAnimeServices(xem, lists, anilist)
	id := addTitanShow(t, env)
	url := fmt.Sprintf("%s/api/series/%d/numbering/refresh", env.baseURL, id)
	postJSON[map[string]any](t, env.client, url, nil, http.StatusOK)

	down.Store(true)
	got := postJSON[map[string]any](t, env.client, url, nil, http.StatusOK)
	if got["sceneState"] != "offline" || got["listsState"] != "found" || got["anilist"] != float64(16498) {
		t.Fatalf("what was known must be kept: %+v", got)
	}
	note, _ := got["note"].(string)
	if !strings.Contains(note, "couldn't be reached") || strings.Contains(strings.ToLower(note), "error") {
		t.Fatalf("note: %q", note)
	}
}

func TestRefreshMappingOfAMissingShowIs404(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	postJSON[map[string]any](t, env.client, env.baseURL+"/api/series/9999/numbering/refresh", nil, http.StatusNotFound)
	postJSON[map[string]any](t, env.client, env.baseURL+"/api/series/abc/numbering/refresh", nil, http.StatusBadRequest)
}

func TestNumberingSettingIsOnByDefaultAndSticks(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	get := func() any {
		return getJSON[map[string]any](t, env.client, env.baseURL+"/api/settings")["numberingLookup"]
	}
	if get() != true {
		t.Fatalf("numberingLookup = %v, want true", get())
	}
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"numberingLookup": false}, http.StatusOK)
	if get() != false {
		t.Fatalf("numberingLookup = %v, want false", get())
	}
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleAutoDownload": true}, http.StatusOK)
	if get() != false {
		t.Fatal("another save changed it")
	}
}

// The releases an anime really gets are named in romaji, with the part of the
// season in the title: none of it matches "Attack on Titan" S03E17 until the
// show's other names and entries are known.
func TestSeriesSearchFindsReleasesUnderTheOtherNamesAfterTheMappingIsRead(t *testing.T) {
	titles := []string{
		"[SubsPlease] Shingeki no Kyojin Season 3 Part 2 - 05 (1080p) [ABCD1234]",
		"[SubsPlease] Shingeki no Kyojin Season 3 Part 2 - 06 (1080p) [ABCD1235]",
		"[SubsPlease] Shingeki no Kyojin - 05 (1080p) [ABCD1236]",
		"[SubsPlease] Another Show - 05 (1080p) [ABCD1237]",
	}
	env := newTVAutoEnv(t, titles, nil)
	xem, lists, anilist := fakeNumberingServices(t, nil)
	env.server.TestSetAnimeServices(xem, lists, anilist)
	id := addTitanShow(t, env)
	searchURL := fmt.Sprintf("%s/api/series/%d/search?season=3&episode=17", env.baseURL, id)
	titanURL := fmt.Sprintf("%s/api/series/%d/search?season=1&episode=5", env.baseURL, id)

	asList := func(u string) []map[string]any { return getJSON[[]map[string]any](t, env.client, u) }
	if got := resultTitles(asList(searchURL)); len(got) != 0 {
		t.Fatalf("before the mapping nothing matches the show: %v", got)
	}

	postJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/numbering/refresh", env.baseURL, id), nil, http.StatusOK)

	got := resultTitles(asList(searchURL))
	if !got[titles[0]] || got[titles[1]] || got[titles[2]] || got[titles[3]] {
		t.Fatalf("S03E17 is the fifth episode of the part: %v", got)
	}
	got = resultTitles(asList(titanURL))
	if !got[titles[2]] || got[titles[0]] {
		t.Fatalf("S01E05 is the plain number under the first name: %v", got)
	}
}

// A show that isn't an anime is left alone until someone presses the button;
// then a release named in scene numbering ("S01E21") is found for the show's
// own S02E01.
func TestSceneNumberedReleaseIsFoundForTheShowsOwnEpisode(t *testing.T) {
	titles := []string{"Bleach.S01E21.720p.HDTV.x264-GRP", "Bleach.S02E01.720p.HDTV.x264-GRP", "Bleach.S01E05.720p.HDTV.x264-GRP"}
	env := newTVAutoEnv(t, titles, nil)
	xem, lists, anilist := fakeNumberingServices(t, nil)
	env.server.TestSetAnimeServices(xem, lists, anilist)
	var eps []library.Episode
	for _, s := range []struct{ season, n int }{{1, 20}, {2, 20}} {
		for e := 1; e <= s.n; e++ {
			eps = append(eps, library.Episode{Season: s.season, Episode: e, AirDate: "2020-01-01"})
		}
	}
	sr, err := env.server.MovieRepo.AddSeries(library.Series{TMDBID: 30984, Title: "Bleach", Year: 2004, Monitored: true}, eps)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.server.MovieRepo.SetSeriesExternalIDs(sr.ID, 74796, "", ""); err != nil {
		t.Fatal(err)
	}
	search := func(season, episode int) map[string]bool {
		return resultTitles(getJSON[[]map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/search?season=%d&episode=%d", env.baseURL, sr.ID, season, episode)))
	}
	if got := search(2, 1); !got["Bleach.S02E01.720p.HDTV.x264-GRP"] || got["Bleach.S01E21.720p.HDTV.x264-GRP"] {
		t.Fatalf("before the button: %v", got)
	}

	postJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/numbering/refresh", env.baseURL, sr.ID), nil, http.StatusOK)

	if got := search(2, 1); !got["Bleach.S01E21.720p.HDTV.x264-GRP"] {
		t.Fatalf("scene S01E21 is S02E01: %v", got)
	}
	if got := search(1, 5); !got["Bleach.S01E05.720p.HDTV.x264-GRP"] {
		t.Fatalf("an episode that is numbered the same: %v", got)
	}
	// The scene name no longer counts as S01E21 of the show (it has none).
	if got := search(1, 20); got["Bleach.S01E21.720p.HDTV.x264-GRP"] {
		t.Fatalf("scene S01E21 is not season 1 of the show: %v", got)
	}
}
