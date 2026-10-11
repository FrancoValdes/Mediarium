package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/announce"
	"github.com/rdborg/mediarium/internal/library"
)

// An announced release goes through the size table and the delay profile like
// a release an automatic search finds.
func TestIRCAnnounceFollowsTheSizeTableAndTheDelayProfile(t *testing.T) {
	e := newIRCEnv(t)
	if _, err := e.server.MovieRepo.Add(library.Movie{TMDBID: 605, Title: "The Fixture Movie", Year: 1999, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	conn := e.connect(t, nil)
	waiting := func() []map[string]any {
		return getJSON[[]map[string]any](t, e.client, e.base+"/api/search/waiting")
	}
	decide := func(release string, id int, size string) ircLogRow {
		e.irc.say("AnnounceBot", "#announce", movieLine(release, id, size))
		return e.logFor(t, conn.ID, release)
	}

	// The size table is on: 40 GB for a film is far over the largest size.
	postJSONMethod[map[string]any](t, e.client, http.MethodPut, e.base+"/api/quality/size-limits", map[string]any{"enabled": true, "rows": []any{}}, http.StatusOK)
	huge := "The.Fixture.Movie.1999.1080p.BluRay.x264-HUGE"
	if row := decide(huge, 60, "40 GB"); row.Decision != announce.DecisionSkipped || !strings.Contains(row.Reason, "too large") {
		t.Fatalf("oversize release: %+v", row)
	}

	// The delay profile holds a torrent for two hours. The test box says so
	// without remembering anything.
	postJSONMethod[map[string]any](t, e.client, http.MethodPut, e.base+"/api/search/delay", map[string]any{"preferredProtocol": "usenet", "torrentMinutes": 120}, http.StatusOK)
	p, _ := announce.PresetByID("one-line-details-link")
	var res announce.TestResult
	raw := postJSON[json.RawMessage](t, e.client, e.base+"/api/irc-announce/test", map[string]any{
		"template": p.Template, "trackerUrl": "https://tracker.example.org", "lines": []string{movieLine("The.Fixture.Movie.1999.1080p.BluRay.x264-DRY", 61, "8 GB")},
	}, http.StatusOK)
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Decision != announce.DecisionSkipped || !strings.Contains(res.Reason, "delay profile") {
		t.Fatalf("test box under a delay: %s (%s)", res.Decision, res.Reason)
	}
	if len(waiting()) != 0 {
		t.Fatalf("the test box remembered a held release: %+v", waiting())
	}

	held := "The.Fixture.Movie.1999.1080p.BluRay.x264-HELD"
	row := decide(held, 62, "8 GB")
	if row.Decision != announce.DecisionSkipped || !strings.Contains(row.Reason, "delay profile") || !strings.Contains(row.Reason, "searched for again") {
		t.Fatalf("held release: %+v", row)
	}
	if w := waiting(); len(w) != 1 || w[0]["release"] != held {
		t.Fatalf("waiting = %+v", w)
	}
	if len(e.queue(t)) != 0 {
		t.Fatalf("a delayed release was grabbed: %+v", e.queue(t))
	}

	// With no wait and a release inside the size limits it is taken.
	postJSONMethod[map[string]any](t, e.client, http.MethodPut, e.base+"/api/search/delay", map[string]any{"preferredProtocol": "usenet"}, http.StatusOK)
	if row := decide("The.Fixture.Movie.1999.1080p.BluRay.x264-OKAY", 63, "8 GB"); row.Decision != announce.DecisionGrabbed {
		t.Fatalf("release inside the limits: %+v", row)
	}
	e.release()
	waitBackground(t, e.server)
}

func TestIRCAnnounceHoldsAnEpisodeUnderTheDelayProfile(t *testing.T) {
	e := newIRCEnv(t)
	series, err := e.server.MovieRepo.AddSeries(library.Series{TMDBID: 1399, Title: "Fixture Show", Year: 2020, Monitored: true}, []library.Episode{
		{Season: 1, Episode: 1, AirDate: "2020-01-01"}, {Season: 1, Episode: 2, AirDate: "2020-01-08"},
	})
	if err != nil {
		t.Fatal(err)
	}
	conn := e.connect(t, nil)
	postJSONMethod[map[string]any](t, e.client, http.MethodPut, e.base+"/api/search/delay", map[string]any{"preferredProtocol": "usenet", "torrentMinutes": 90}, http.StatusOK)
	postJSONMethod[map[string]any](t, e.client, http.MethodPut, e.base+"/api/quality/size-limits", map[string]any{"enabled": true, "rows": []any{}}, http.StatusOK)
	line := func(rel string, id int, size string) string {
		return fmt.Sprintf("[TV/HD] %s - https://tracker.example/details.php?id=%d - %s", rel, id, size)
	}
	const huge = "Fixture.Show.S01E01.1080p.WEB-DL.x264-HUGE"
	const fresh = "Fixture.Show.S01E01.1080p.WEB-DL.x264-FRESH"
	e.irc.say("AnnounceBot", "#announce", line(huge, 70, "60 GB"))
	if row := e.logFor(t, conn.ID, huge); row.Decision != announce.DecisionSkipped || !strings.Contains(row.Reason, "too large") {
		t.Fatalf("oversize episode: %+v", row)
	}
	e.irc.say("AnnounceBot", "#announce", line(fresh, 71, "1.5 GB"))
	if row := e.logFor(t, conn.ID, fresh); row.Decision != announce.DecisionSkipped || !strings.Contains(row.Reason, "delay profile") {
		t.Fatalf("delayed episode: %+v", row)
	}
	w := getJSON[[]map[string]any](t, e.client, e.base+"/api/search/waiting")
	if len(w) != 1 || w[0]["seriesId"] != float64(series.ID) || w[0]["release"] != fresh {
		t.Fatalf("waiting = %+v", w)
	}
	if len(e.queue(t)) != 0 {
		t.Fatalf("queue = %+v", e.queue(t))
	}
}
