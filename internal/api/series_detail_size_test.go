package api_test

import (
	"fmt"
	"testing"

	"github.com/rdborg/mediarium/internal/library"
)

// A show's page gets the size of each downloaded episode's file (and none for
// one that isn't downloaded), and the show's status once TMDB has given it.
func TestSeriesDetailHasEpisodeSizesAndShowStatus(t *testing.T) {
	env := newTVAutoEnv(t, []string{"Fixture.Show.S01E01.1080p.WEB-DL.x264-GRP"}, []episodeSpec{
		{1, 1, "2020-01-01", library.StatusDownloaded, "WEBDL-1080p"},
		{1, 2, "2020-01-08", library.StatusMissing, ""},
	})
	detail := getJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d", env.baseURL, env.seriesID))
	if _, has := detail["showStatus"]; has {
		t.Fatalf("a show TMDB hasn't described yet has no status: %v", detail["showStatus"])
	}
	eps := detail["episodes"].([]any)
	if len(eps) != 2 {
		t.Fatalf("want 2 episodes, got %d", len(eps))
	}
	first, second := eps[0].(map[string]any), eps[1].(map[string]any)
	if first["sizeBytes"] != float64(len("old file")) {
		t.Fatalf("the downloaded episode has its file's size, got %v", first["sizeBytes"])
	}
	if _, has := second["sizeBytes"]; has {
		t.Fatalf("a missing episode has no size, got %v", second["sizeBytes"])
	}
}
