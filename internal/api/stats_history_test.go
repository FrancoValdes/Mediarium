package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/library"
)

func TestStatsHistory(t *testing.T) {
	server, httpSrv, client := newHuntTestServer(t)
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/onboarding/admin", map[string]string{
		"username": "ryan", "password": "correct-horse-battery-staple", "firstName": "Ryan", "lastName": "Tester",
	}, http.StatusCreated)
	base := httpSrv.URL

	// Nothing yet: an empty list.
	if got := getJSON[map[string]any](t, client, base+"/api/stats/history"); len(got["days"].([]any)) != 0 {
		t.Fatalf("empty library: %v", got)
	}

	// Two movies, one added ten days ago: the days before the first snapshot
	// are filled in from when they were added.
	a, _ := server.MovieRepo.Add(library.Movie{TMDBID: 801, Title: "The Long Equation", Year: 2019, Monitored: true})
	if _, err := server.MovieRepo.Add(library.Movie{TMDBID: 802, Title: "Iron Dust", Year: 2020, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	tenDaysAgo := time.Now().UTC().AddDate(0, 0, -10)
	if err := server.TestExec(`UPDATE movies SET added_at = ? WHERE id = ?`, tenDaysAgo.Format(time.RFC3339), a.ID); err != nil {
		t.Fatal(err)
	}
	if err := server.TestStatsSnapshot(time.Now()); err != nil {
		t.Fatal(err)
	}
	days := getJSON[map[string]any](t, client, base+"/api/stats/history")["days"].([]any)
	if len(days) != 11 {
		t.Fatalf("want 11 days (ten days ago to today), got %d", len(days))
	}
	first, last := days[0].(map[string]any), days[len(days)-1].(map[string]any)
	if first["movies"] != float64(1) || first["estimated"] != true || first["libraryBytes"] != float64(-1) {
		t.Fatalf("first day: %v", first)
	}
	if last["movies"] != float64(2) || last["estimated"] != nil || last["libraryBytes"] != float64(0) || last["day"] != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("today: %v", last)
	}
	if got := getJSON[map[string]any](t, client, base+"/api/stats/history?days=3")["days"].([]any); len(got) != 3 {
		t.Fatalf("last 3 days: %d", len(got))
	}
	// Taking today's snapshot again replaces it.
	if err := server.TestStatsSnapshot(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := getJSON[map[string]any](t, client, base+"/api/stats/history")["days"].([]any); len(got) != 11 {
		t.Fatalf("a second snapshot of today should replace the first: %d days", len(got))
	}
}
