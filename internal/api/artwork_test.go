package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/library"
)

// The Backdrops view: a title on the calendar gets its backdrop and logo from
// TMDB in the background, and the calendar shows them once they are known.
func TestCalendarGetsBackdropsAndLogos(t *testing.T) {
	server, base, client := loginNewServer(t)
	var calls atomic.Int32
	tmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/701/images" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		fmt.Fprint(w, `{"backdrops":[
			{"file_path":"/withtext.jpg","iso_639_1":"en","vote_average":9,"width":1920},
			{"file_path":"/clean.jpg","iso_639_1":null,"vote_average":5,"width":1920}],
		 "logos":[{"file_path":"/logo.png","iso_639_1":"en","vote_average":5,"vote_count":3,"width":500}]}`)
	}))
	defer tmdb.Close()
	server.TestSetTMDBBaseURL("key", tmdb.URL)

	release := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	m, err := server.MovieRepo.Add(library.Movie{TMDBID: 701, Title: "Night Harbour", Year: 2027, Monitored: true, ReleaseDate: release})
	if err != nil {
		t.Fatal(err)
	}
	_ = m
	find := func() map[string]any {
		for _, e := range getJSON[[]map[string]any](t, client, base+"/api/calendar") {
			if e["title"] == "Night Harbour" {
				return e
			}
		}
		t.Fatal("the movie should be on the calendar")
		return nil
	}
	if e := find(); e["backdropUrl"] != nil || e["logoUrl"] != nil {
		t.Fatalf("nothing is fetched until the job runs: %v", e)
	}
	server.TestRunArtworkJob()
	e := find()
	if e["backdropUrl"] != "https://image.tmdb.org/t/p/w780/clean.jpg" || e["logoUrl"] != "https://image.tmdb.org/t/p/w300/logo.png" {
		t.Fatalf("the backdrop with no text and the logo: %v", e)
	}
	server.TestRunArtworkJob() // already known: TMDB isn't asked again
	if calls.Load() != 1 {
		t.Fatalf("TMDB should be asked once, was asked %d times", calls.Load())
	}
}
