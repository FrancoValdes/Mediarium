package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/library"
)

// specialsEnv is a show with one regular episode and two specials, one of
// them posted under its title only, the other as S00E02.
func specialsEnv(t *testing.T) (*tvAutoEnv, library.Episode, library.Episode) {
	t.Helper()
	env := newTVAutoEnv(t, []string{
		"Fixture.Show.The.Christmas.Invasion.1080p.WEB-DL.x264-GRP",
		"Fixture.Show.S00E02.1080p.WEB-DL.x264-GRP",
	}, []episodeSpec{
		{1, 1, "2020-01-01", library.StatusDownloaded, "HDTV-720p"},
		{0, 1, "2020-12-25", library.StatusMissing, ""},
		{0, 2, "2021-12-25", library.StatusMissing, ""},
	})
	// The fixture has no episode titles: give the specials theirs.
	if err := env.server.MovieRepo.UpsertEpisodes(env.seriesID, []library.Episode{
		{Season: 0, Episode: 1, Title: "The Christmas Invasion", AirDate: "2020-12-25"},
		{Season: 0, Episode: 2, Title: "The Runaway Bride", AirDate: "2021-12-25"},
	}, false); err != nil {
		t.Fatal(err)
	}
	one, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 0, 1)
	two, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 0, 2)
	return env, one, two
}

func TestSpecialsAreLeftAloneUntilAskedFor(t *testing.T) {
	env, one, _ := specialsEnv(t)
	if one.Monitored {
		t.Fatal("specials start unmonitored")
	}
	// Searching the whole show by hand doesn't fetch unmonitored specials.
	res := postJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/search-now", env.baseURL, env.seriesID), map[string]any{}, http.StatusOK)
	if res["grabbed"] != float64(0) || len(env.grabbedTitles(t)) != 0 {
		t.Fatalf("whole show: %+v %v", res, env.grabbedTitles(t))
	}

	// Asking for one special by name gets it, though it isn't monitored.
	res = postJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/search-now", env.baseURL, env.seriesID), map[string]any{"season": 0, "episode": 1}, http.StatusOK)
	got := env.grabbedTitles(t)
	if res["grabbed"] != float64(1) || len(got) != 1 || got[0] != "Fixture.Show.The.Christmas.Invasion.1080p.WEB-DL.x264-GRP" {
		t.Fatalf("special by title: %+v %v", res, got)
	}
	if ep, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 0, 1); ep.Status != library.StatusDownloading {
		t.Fatalf("the special should be downloading: %s", ep.Status)
	}
	if ep, _ := env.server.MovieRepo.GetEpisode(env.seriesID, 0, 2); ep.Status != library.StatusMissing {
		t.Fatalf("the other special was claimed too: %s", ep.Status)
	}
}

func TestMonitoredSpecialsAreHuntedAndSearchable(t *testing.T) {
	env, _, two := specialsEnv(t)
	if err := env.server.MovieRepo.SetEpisodeMonitored(two.ID, true); err != nil {
		t.Fatal(err)
	}
	res := postJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/search-now", env.baseURL, env.seriesID), map[string]any{}, http.StatusOK)
	got := env.grabbedTitles(t)
	if res["grabbed"] != float64(1) || len(got) != 1 || got[0] != "Fixture.Show.S00E02.1080p.WEB-DL.x264-GRP" {
		t.Fatalf("monitored special: %+v %v", res, got)
	}

	// The interactive search lists only releases for that special.
	results := getJSON[[]map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/search?season=0&episode=1", env.baseURL, env.seriesID))
	if len(results) != 1 || !strings.Contains(results[0]["title"].(string), "Christmas.Invasion") {
		t.Fatalf("search for S00E01: %+v", results)
	}
	// Season 0 without an episode is refused: specials are one at a time.
	resp, err := env.client.Get(fmt.Sprintf("%s/api/series/%d/search?season=0", env.baseURL, env.seriesID))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("season 0 alone: %d", resp.StatusCode)
	}
}
