package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/queue"
)

// listsIndexer offers a release for each of the two top rated movies of the
// fake TMDB, posted ageMinutes ago. Its downloads end at once (not found).
func listsIndexer(t *testing.T, ageMinutes int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/nzb/") {
			http.Error(w, "not available", http.StatusNotFound)
			return
		}
		posted := time.Now().Add(-time.Duration(ageMinutes) * time.Minute).UTC().Format(time.RFC1123Z)
		w.Header().Set("Content-Type", "application/xml")
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>`)
		for i, title := range []string{"Foxtrot.2021.1080p.WEB-DL.x264-GRP", "Alpha.2010.1080p.WEB-DL.x264-GRP"} {
			fmt.Fprintf(&b, `<item><title>%s</title><guid>l%d</guid><pubDate>%s</pubDate><enclosure url="http://%s/nzb/%d.nzb" length="8589934592" type="application/x-nzb"/><newznab:attr name="size" value="8589934592"/><newznab:attr name="category" value="2000"/></item>`,
				title, i, posted, r.Host, i)
		}
		b.WriteString(`</channel></rss>`)
		fmt.Fprint(&b, "")
		fmt.Fprint(w, b.String())
	}))
	t.Cleanup(srv.Close)
	return srv
}

// What an import list adds is searched for through the automatic path: the
// hourly limit and the delay profile apply, which a forced search ignored.
func TestImportListSearchFollowsTheAutomaticLimits(t *testing.T) {
	tests := []struct {
		name       string
		capReached bool
		delayMins  int
		want       int // downloads added by the list's search
	}{
		{"room for both", false, 0, 2},
		{"the hourly limit is reached", true, 0, 0},
		{"the delay profile holds fresh releases", false, 120, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, base, admin := listsServer(t)
			idx := listsIndexer(t, 10)
			postJSON[map[string]any](t, admin, base+"/api/indexers", map[string]any{
				"name": "Fixture Indexer", "definitionId": "fixture", "baseUrl": idx.URL, "apiKey": "k",
			}, http.StatusCreated)
			if tc.delayMins > 0 {
				postJSONMethod[map[string]any](t, admin, http.MethodPut, base+"/api/search/delay", map[string]any{"usenetMinutes": tc.delayMins}, http.StatusOK)
			}
			before := 0
			if tc.capReached {
				idle, err := server.MovieRepo.Add(library.Movie{TMDBID: 999, Title: "Something Else", Year: 1990, Monitored: false})
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 10; i++ {
					id, err := server.QueueRepo.Enqueue(queue.Item{MovieID: idle.ID, ReleaseTitle: fmt.Sprintf("Old.Release.%d", i), NZBURL: "http://example.invalid/x.nzb"})
					if err != nil {
						t.Fatal(err)
					}
					if err := server.QueueRepo.SetStatus(id, queue.StatusCompleted, ""); err != nil {
						t.Fatal(err)
					}
				}
				before = 10
			}
			created := postJSON[map[string]any](t, admin, base+"/api/import-lists", map[string]any{
				"source": "tmdb-chart", "media": "movie", "config": map[string]any{"chart": "top-rated", "count": 20}, "intervalHours": 24,
			}, http.StatusCreated)
			waitBackground(t, server)
			l := readList(t, admin, base, created["id"].(float64))
			if l["lastAdded"] != float64(2) {
				t.Fatalf("the list should have added both movies: %v", l)
			}
			if got := len(getJSON[[]map[string]any](t, admin, base+"/api/queue")) - before; got != tc.want {
				t.Fatalf("the list's search added %d downloads, want %d", got, tc.want)
			}
		})
	}
}

// A new list adds a few titles at a time unless told otherwise.
func TestImportListDefaultsToTenAtATime(t *testing.T) {
	_, base, admin := listsServer(t)
	created := postJSON[map[string]any](t, admin, base+"/api/import-lists", map[string]any{
		"source": "tmdb-chart", "media": "movie", "config": map[string]any{"chart": "top-rated", "count": 20}, "intervalHours": 24, "enabled": false,
	}, http.StatusCreated)
	if created["maxPerRun"] != float64(10) {
		t.Fatalf("maxPerRun = %v, want 10", created["maxPerRun"])
	}
}
