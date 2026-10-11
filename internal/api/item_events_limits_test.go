package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/library"
)

func seriesEvents(t *testing.T, client *http.Client, base string, id int64) []map[string]any {
	t.Helper()
	return getJSON[[]map[string]any](t, client, fmt.Sprintf("%s/api/series/%d/events", base, id))
}

// The note of a search judges releases with the same limits as the grab: a
// release outside the size table or held back by the delay profile is not
// counted as acceptable.
func TestMovieSearchNoteCountsTheSizeTableAndTheDelayProfile(t *testing.T) {
	server, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	signIn(t, client, base)
	k := newKnobIndexer(t)
	addKnobIndexer(t, client, base, "Fixture Indexer", k)
	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 901, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}

	k.sizeBytes.Store(40 << 30)

	// The size table is on and the release is far too large.
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/quality/size-limits", map[string]any{"enabled": true, "rows": []any{}}, http.StatusOK)
	server.TestHunt(context.Background())
	if e := findEvent(movieEvents(t, client, base, movie.ID), "searched", "none acceptable: 1 outside the size limits"); e == nil {
		t.Fatalf("the note should say the size table refused it: %+v", movieEvents(t, client, base, movie.ID))
	}
	if got := queueTitles(t, client, base); len(got) != 0 {
		t.Fatalf("grabbed a release outside the size limits: %v", got)
	}

	// Inside the limits but too fresh for the delay profile.
	k.sizeBytes.Store(8 << 30)
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/search/delay", map[string]any{"usenetMinutes": 120}, http.StatusOK)
	server.TestHunt(context.Background())
	if e := findEvent(movieEvents(t, client, base, movie.ID), "searched", "none acceptable: 1 held back by the delay profile"); e == nil {
		t.Fatalf("the note should say the delay profile holds it: %+v", movieEvents(t, client, base, movie.ID))
	}
	if got := queueTitles(t, client, base); len(got) != 0 {
		t.Fatalf("grabbed a delayed release: %v", got)
	}
}

func TestTVSearchNoteCountsTheSizeTable(t *testing.T) {
	env := newTVAutoEnv(t, []string{"Fixture.Show.S01E01.1080p.WEB-DL.x264-GRP"}, []episodeSpec{{1, 1, "2020-01-01", library.StatusMissing, ""}})
	postJSONMethod[map[string]any](t, env.client, http.MethodPut, env.baseURL+"/api/quality/size-limits", map[string]any{"enabled": true, "rows": []any{}}, http.StatusOK)
	env.server.TestHunt(context.Background())
	requireTitles(t, env.grabbedTitles(t))
	var found bool
	for _, e := range seriesEvents(t, env.client, env.baseURL, env.seriesID) {
		msg, _ := e["message"].(string)
		if e["kind"] == "searched" && strings.Contains(msg, "none acceptable: 1 outside the size limits") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the note should say the size table refused the release: %+v", seriesEvents(t, env.client, env.baseURL, env.seriesID))
	}
	env.finish(t)
}

// A release named in the scene's numbering counts for the show's own episode
// in the note, as it does when the release is picked.
func TestTVSearchNoteReadsSceneNumbering(t *testing.T) {
	env := newTVAutoEnv(t, []string{"Bleach.S01E21.1080p.WEB-DL.x264-GRP"}, nil)
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
	postJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/numbering/refresh", env.baseURL, sr.ID), nil, http.StatusOK)

	env.server.TestHunt(context.Background())
	env.finish(t)
	var note string
	for _, e := range seriesEvents(t, env.client, env.baseURL, sr.ID) {
		msg, _ := e["message"].(string)
		if e["kind"] == "searched" && strings.HasPrefix(msg, "Searched for S02E01:") {
			note = msg
		}
	}
	if note == "" {
		t.Fatalf("no note for S02E01: %+v", seriesEvents(t, env.client, env.baseURL, sr.ID))
	}
	if strings.Contains(note, "not this episode") || strings.Contains(note, "none acceptable") {
		t.Fatalf("scene S01E21 is S02E01 of the show, but the note says %q", note)
	}
}
