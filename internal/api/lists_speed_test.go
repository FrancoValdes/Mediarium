package api_test

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/books"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/queue"
)

// The Wanted page says, for each title, what the last automatic search found.
// It reads that for all titles at once, and it must be each title's own newest
// search, not another's and not an event of another kind.
func TestWantedShowsEachTitlesLastSearch(t *testing.T) {
	past := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	env := newTVAutoEnv(t, nil, []episodeSpec{
		{1, 1, past, library.StatusMissing, ""},
		{1, 2, past, library.StatusMissing, ""},
	})
	repo := env.server.MovieRepo
	one, _ := repo.Add(library.Movie{TMDBID: 1, Title: "Searched Twice", Monitored: true})
	two, _ := repo.Add(library.Movie{TMDBID: 2, Title: "Never Searched", Monitored: true})
	log := func(e queue.ItemEvent) {
		t.Helper()
		if err := env.server.QueueRepo.LogItemEvent(e); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond) // event times have one second resolution
	}
	log(queue.ItemEvent{MovieID: one.ID, Kind: "searched", Message: "first search", ItemOnly: true})
	log(queue.ItemEvent{MovieID: one.ID, Kind: "searched", Message: "second search", ItemOnly: true})
	log(queue.ItemEvent{MovieID: one.ID, Kind: "grabbed", Message: "a newer event that is not a search"})
	log(queue.ItemEvent{MovieID: two.ID, Kind: "failed", Message: "something else"})
	log(queue.ItemEvent{SeriesID: env.seriesID, Kind: "searched", Message: "show search", ItemOnly: true})

	byTitle := map[string]map[string]any{}
	for _, it := range getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/wanted?kind=missing") {
		label := it["title"].(string)
		if sub, ok := it["subtitle"].(string); ok && sub != "" {
			label += " " + sub[:6]
		}
		byTitle[label] = it
	}
	if got := byTitle["Searched Twice"]["lastSearch"]; got != "second search" {
		t.Fatalf("a movie's last search = %v", got)
	}
	if got, ok := byTitle["Never Searched"]["lastSearch"]; ok {
		t.Fatalf("a movie that was never searched shows %v", got)
	}
	for _, label := range []string{"Fixture Show S01E01", "Fixture Show S01E02"} {
		if got := byTitle[label]["lastSearch"]; got != "show search" {
			t.Fatalf("%s: last search = %v", label, got)
		}
	}
}

// The lists that are read again and again are tagged, so asking again with the
// tag costs no body, and they are sent compressed.
func TestBigListsAreTaggedAndCompressed(t *testing.T) {
	env := newTVAutoEnv(t, nil, []episodeSpec{{1, 1, "2020-01-01", library.StatusMissing, ""}})
	for i := 1; i <= 40; i++ { // enough to be worth compressing
		if _, err := env.server.MovieRepo.Add(library.Movie{TMDBID: i, Title: "A fairly long film title to fill the list", Monitored: true, Overview: "Some overview words to give the answer some size."}); err != nil {
			t.Fatal(err)
		}
	}
	plain := &http.Client{Jar: env.client.Jar, Transport: &http.Transport{DisableCompression: true}}
	for _, path := range []string{"/api/wanted?kind=missing", "/api/movies", "/api/queue", "/api/calendar", "/api/stats/history"} {
		req, _ := http.NewRequest(http.MethodGet, env.baseURL+path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := plain.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		etag := resp.Header.Get("ETag")
		encoding := resp.Header.Get("Content-Encoding")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || etag == "" {
			t.Fatalf("%s: status %d, etag %q", path, resp.StatusCode, etag)
		}
		if len(body) > 1024 && encoding != "gzip" {
			t.Fatalf("%s: a %d byte answer was not compressed", path, len(body))
		}
		req, _ = http.NewRequest(http.MethodGet, env.baseURL+path, nil)
		req.Header.Set("If-None-Match", etag)
		again, err := plain.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		left, _ := io.ReadAll(again.Body)
		again.Body.Close()
		if again.StatusCode != http.StatusNotModified || len(left) != 0 {
			t.Fatalf("%s: with its tag the answer was %d with %d bytes", path, again.StatusCode, len(left))
		}
	}
}

// ?brief=1 leaves the long texts out of the lists the Library draws from;
// without it nothing changes.
func TestBriefListsLeaveOutLongTexts(t *testing.T) {
	env := newTVAutoEnv(t, nil, []episodeSpec{{1, 1, "2020-01-01", library.StatusMissing, ""}})
	if _, err := env.server.MovieRepo.Add(library.Movie{TMDBID: 7, Title: "Has Overview", Overview: "A long overview.", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	postJSONMethod[map[string]any](t, env.client, http.MethodPut, env.baseURL+"/api/modules", map[string]any{"ebooks": true}, http.StatusOK)
	if _, err := env.server.BookRepo.Add(books.Book{OLKey: "OL1W", Title: "Has Description", Description: "A long description.", WantEbook: true}); err != nil {
		t.Fatal(err)
	}
	for path, field := range map[string]string{"/api/movies": "overview", "/api/books": "description"} {
		full := getJSON[[]map[string]any](t, env.client, env.baseURL+path)
		if len(full) != 1 || full[0][field] == nil {
			t.Fatalf("%s should carry %s: %+v", path, field, full)
		}
		brief := getJSON[[]map[string]any](t, env.client, env.baseURL+path+"?brief=1")
		if len(brief) != 1 || brief[0][field] != nil || brief[0]["title"] == nil {
			t.Fatalf("%s?brief=1 = %+v", path, brief)
		}
	}
	shows := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/series?brief=1")
	if len(shows) != 1 || shows[0]["overview"] != nil {
		t.Fatalf("series brief = %+v", shows)
	}
}

// ?active=1 asks the download list for only what is still going.
func TestQueueCanBeAskedForJustTheActiveOnes(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	m, _ := env.server.MovieRepo.Add(library.Movie{TMDBID: 1, Title: "Film", Monitored: true})
	for _, st := range []queue.Status{queue.StatusQueued, queue.StatusDownloading, queue.StatusCompleted, queue.StatusFailed, queue.StatusPaused} {
		id, err := env.server.QueueRepo.Enqueue(queue.Item{MovieID: m.ID, ReleaseTitle: "R." + string(st)})
		if err != nil {
			t.Fatal(err)
		}
		if err := env.server.QueueRepo.SetStatus(id, st, ""); err != nil {
			t.Fatal(err)
		}
	}
	all := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/queue")
	active := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/queue?active=1")
	if len(all) != 5 || len(active) != 2 {
		t.Fatalf("all %d, active %d", len(all), len(active))
	}
	for _, it := range active {
		if s := it["status"]; s != "queued" && s != "downloading" {
			t.Fatalf("an item that is not active came back: %+v", it)
		}
		if it["title"] != "Film" || it["tmdbId"] != float64(1) {
			t.Fatalf("the title was not looked up: %+v", it)
		}
	}
}

// What the dashboard counts, what it lists as coming up and what it says is
// wanted come from the database's own counting; they must agree with the
// lists they summarise.
func TestDashboardNumbersMatchTheLibrary(t *testing.T) {
	soon := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	later := time.Now().UTC().AddDate(0, 0, 9).Format("2006-01-02")
	past := time.Now().UTC().AddDate(0, 0, -5).Format("2006-01-02")
	env := newTVAutoEnv(t, nil, []episodeSpec{
		{1, 1, past, library.StatusMissing, ""},
		{1, 2, past, library.StatusDownloaded, "WEBDL-1080p"},
		{1, 3, soon, library.StatusMissing, ""},
		{1, 4, "", library.StatusMissing, ""},
	})
	repo := env.server.MovieRepo
	a, _ := repo.Add(library.Movie{TMDBID: 1, Title: "Have It", Monitored: true})
	_ = repo.SetStatus(a.ID, library.StatusDownloaded, "Bluray-1080p", "/m/a.mkv")
	repo.Add(library.Movie{TMDBID: 2, Title: "Want It", Monitored: true, ReleaseDate: later})
	repo.Add(library.Movie{TMDBID: 3, Title: "Old Release", Monitored: true, ReleaseDate: past})
	repo.Add(library.Movie{TMDBID: 4, Title: "Not Wanted", Monitored: false})
	b, _ := env.server.BookRepo.Add(books.Book{OLKey: "OL1W", Title: "Book", WantEbook: true})
	_ = env.server.BookRepo.SetState(b.ID, books.Ebook, books.StatusDownloaded, "epub", "/b/x.epub")
	env.server.BookRepo.Add(books.Book{OLKey: "OL2W", Title: "Other", WantAudiobook: true})

	d := getJSON[map[string]any](t, env.client, env.baseURL+"/api/dashboard")
	lib := d["library"].(map[string]any)
	movies := lib["movies"].(map[string]any)
	if movies["total"] != float64(4) || movies["downloaded"] != float64(1) || movies["missing"] != float64(3) || movies["downloading"] != float64(0) {
		t.Fatalf("movies = %v", movies)
	}
	series := lib["series"].(map[string]any)
	if series["total"] != float64(1) || series["episodes"] != float64(4) || series["episodesDownloaded"] != float64(1) || series["episodesMissing"] != float64(3) {
		t.Fatalf("series = %v", series)
	}
	wanted := lib["wanted"].(map[string]any)
	missingMovies := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/wanted?kind=missing")
	var wantedMovies, wantedEpisodes int
	for _, it := range missingMovies {
		if it["kind"] == "movie" {
			wantedMovies++
		} else {
			wantedEpisodes++
		}
	}
	if wanted["movies"] != float64(wantedMovies) || wanted["episodes"] != float64(wantedEpisodes) || wantedMovies != 2 || wantedEpisodes != 1 {
		t.Fatalf("dashboard wanted %v, Wanted page %d movies and %d episodes", wanted, wantedMovies, wantedEpisodes)
	}
	if e := lib["ebooks"].(map[string]any); e["books"] != float64(1) || e["downloaded"] != float64(1) || e["missing"] != float64(0) {
		t.Fatalf("ebooks = %v", e)
	}
	if a := lib["audiobooks"].(map[string]any); a["books"] != float64(1) || a["downloaded"] != float64(0) || a["missing"] != float64(1) {
		t.Fatalf("audiobooks = %v", a)
	}
	// Coming up: from today on, in date order, never what is already out.
	var upcoming []string
	for _, e := range d["upcoming"].([]any) {
		upcoming = append(upcoming, e.(map[string]any)["releaseDate"].(string))
	}
	if len(upcoming) != 2 || upcoming[0] != soon || upcoming[1] != later {
		t.Fatalf("upcoming = %v, want [%s %s]", upcoming, soon, later)
	}
}
