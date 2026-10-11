package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/rdborg/mediarium/internal/api"
	"github.com/rdborg/mediarium/internal/library"
)

func heldRows(t *testing.T, server *api.Server) string {
	t.Helper()
	v, err := server.TestScalar(`SELECT COUNT(*) FROM delayed_releases`)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Held releases do not outlive the title: removing it, or grabbing a release
// for it by hand, forgets them.
func TestHeldReleasesAreForgottenWhenTheTitleIsRemovedOrGrabbed(t *testing.T) {
	for _, tc := range []string{"removed", "grabbed"} {
		t.Run(tc, func(t *testing.T) {
			server, httpSrv, client := newHuntTestServer(t)
			base := httpSrv.URL
			signIn(t, client, base)
			k := newKnobIndexer(t)
			addKnobIndexer(t, client, base, "Fixture Indexer", k)
			postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/search/delay", map[string]any{"usenetMinutes": 120}, http.StatusOK)
			movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 802, Title: "The Fixture Movie", Year: 1999, Monitored: true})
			if err != nil {
				t.Fatal(err)
			}
			server.TestHunt(context.Background())
			if heldRows(t, server) != "1" {
				t.Fatalf("expected one held release, have %s", heldRows(t, server))
			}
			if tc == "removed" {
				if status, _ := doJSONStatus(t, client, http.MethodDelete, fmt.Sprintf("%s/api/movies/%d", base, movie.ID), nil); status >= 300 {
					t.Fatalf("remove = %d", status)
				}
			} else {
				postJSON[map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d/search-now", base, movie.ID), nil, http.StatusOK)
				waitForQueueTerminal(t, client, base)
			}
			if heldRows(t, server) != "0" {
				t.Fatalf("held releases stayed after the title was %s", tc)
			}
			if w := getJSON[[]map[string]any](t, client, base+"/api/search/waiting"); len(w) != 0 {
				t.Fatalf("still waiting: %+v", w)
			}
		})
	}
}

// A downloaded title only holds releases that would be a real upgrade.
func TestDownloadedTitleDoesNotHoldReleasesItWouldNotTake(t *testing.T) {
	server, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	signIn(t, client, base)
	enableUpgrades(t, server)
	k := newKnobIndexer(t) // offers a 1080p WEB-DL
	addKnobIndexer(t, client, base, "Fixture Indexer", k)
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/search/delay", map[string]any{"usenetMinutes": 120}, http.StatusOK)
	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 803, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}

	// Downloaded as a 1080p Blu-ray already: the WEB-DL is no upgrade, so it is not held.
	if err := server.MovieRepo.SetStatus(movie.ID, library.StatusDownloaded, "Bluray-1080p", "/movies/fixture.mkv"); err != nil {
		t.Fatal(err)
	}
	server.TestHunt(context.Background())
	if n := heldRows(t, server); n != "0" {
		t.Fatalf("a release that is no upgrade was held (%s rows)", n)
	}

	// Downloaded as 720p: the same WEB-DL is an upgrade, and a fresh one waits.
	if err := server.MovieRepo.SetStatus(movie.ID, library.StatusDownloaded, "WEBDL-720p", "/movies/fixture.mkv"); err != nil {
		t.Fatal(err)
	}
	server.TestHunt(context.Background())
	if n := heldRows(t, server); n != "1" {
		t.Fatalf("an upgrade should be held for the wait, rows = %s", n)
	}
}
