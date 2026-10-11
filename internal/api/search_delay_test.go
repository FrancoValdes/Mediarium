package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/library"
)

// knobIndexer is a fake Newznab indexer with one WEB-DL release whose age,
// size and answer the test can change while it runs.
type knobIndexer struct {
	*httptest.Server
	ageMinutes atomic.Int64
	sizeBytes  atomic.Int64
	status     atomic.Int64 // 0 = answer normally, otherwise this HTTP status
	retryAfter atomic.Int64 // seconds, sent with a failure status when above 0
	hits       atomic.Int64
}

const knobRelease = "The.Fixture.Movie.1999.1080p.WEB-DL.x264-FIXTURE"

func newKnobIndexer(t *testing.T) *knobIndexer {
	t.Helper()
	k := &knobIndexer{}
	k.sizeBytes.Store(8 << 30)
	k.ageMinutes.Store(10)
	k.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k.hits.Add(1)
		if st := k.status.Load(); st != 0 {
			if ra := k.retryAfter.Load(); ra > 0 {
				w.Header().Set("Retry-After", fmt.Sprint(ra))
			}
			w.WriteHeader(int(st))
			return
		}
		posted := time.Now().Add(-time.Duration(k.ageMinutes.Load()) * time.Minute).UTC().Format(time.RFC1123Z)
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
<item><title>%[1]s</title><guid>knob-1</guid><pubDate>%[2]s</pubDate><enclosure url="http://%[3]s/nzb/knob.nzb" length="%[4]d" type="application/x-nzb"/>
<newznab:attr name="size" value="%[4]d"/><newznab:attr name="category" value="2000"/></item></channel></rss>`,
			knobRelease, posted, r.Host, k.sizeBytes.Load())
	}))
	t.Cleanup(k.Close)
	return k
}

func addKnobIndexer(t *testing.T, client *http.Client, base, name string, k *knobIndexer) int64 {
	t.Helper()
	out := postJSON[map[string]any](t, client, base+"/api/indexers", map[string]any{
		"name": name, "definitionId": "fixture", "baseUrl": k.URL, "apiKey": "fixture-key",
	}, http.StatusCreated)
	return int64(out["id"].(float64))
}

func queueTitles(t *testing.T, client *http.Client, base string) []string {
	t.Helper()
	var out []string
	for _, q := range getJSON[[]map[string]any](t, client, base+"/api/queue") {
		out = append(out, q["releaseTitle"].(string))
	}
	return out
}

func TestDelayProfileSettings(t *testing.T) {
	_, base, client := loginNewServer(t)

	def := getJSON[map[string]any](t, client, base+"/api/search/delay")
	if def["preferredProtocol"] != "usenet" || def["usenetMinutes"] != float64(0) || def["torrentMinutes"] != float64(0) || def["bypassIfHighestQuality"] != false {
		t.Fatalf("a fresh install must not delay anything: %+v", def)
	}

	tests := []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"torrents preferred with a wait for usenet", map[string]any{"preferredProtocol": "torrent", "usenetMinutes": 90, "torrentMinutes": 0, "bypassIfHighestQuality": true}, http.StatusOK},
		{"back to default", map[string]any{"preferredProtocol": "usenet"}, http.StatusOK},
		{"negative wait", map[string]any{"preferredProtocol": "usenet", "usenetMinutes": -5}, http.StatusBadRequest},
		{"too long a wait", map[string]any{"torrentMinutes": 999999}, http.StatusBadRequest},
		{"unknown protocol", map[string]any{"preferredProtocol": "pigeon"}, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := getJSON[map[string]any](t, client, base+"/api/search/delay")
			out := postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/search/delay", tc.body, tc.status)
			after := getJSON[map[string]any](t, client, base+"/api/search/delay")
			if tc.status != http.StatusOK {
				if fmt.Sprint(before) != fmt.Sprint(after) {
					t.Fatalf("a refused change altered the profile: %v -> %v", before, after)
				}
				if msg, _ := out["error"].(string); msg == "" {
					t.Fatalf("expected a plain error message, got %+v", out)
				}
				return
			}
			if after["usenetMinutes"] != out["usenetMinutes"] || after["preferredProtocol"] != out["preferredProtocol"] {
				t.Fatalf("saved %v but read back %v", out, after)
			}
		})
	}
}

func TestDelayHoldsFreshReleaseThenGrabsItOnceOldEnough(t *testing.T) {
	server, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	signIn(t, client, base)
	k := newKnobIndexer(t)
	indexerID := addKnobIndexer(t, client, base, "Fixture Indexer", k)
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/search/delay", map[string]any{"preferredProtocol": "torrent", "usenetMinutes": 120}, http.StatusOK)

	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 701, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}

	// Posted ten minutes ago, Usenet waits two hours: held, nothing grabbed.
	server.TestHunt(context.Background())
	waitBackground(t, server)
	if got := queueTitles(t, client, base); len(got) != 0 {
		t.Fatalf("a fresh release must wait, but the queue has %v", got)
	}
	waiting := getJSON[[]map[string]any](t, client, base+"/api/search/waiting")
	if len(waiting) != 1 || waiting[0]["release"] != knobRelease || waiting[0]["movieId"] != float64(movie.ID) {
		t.Fatalf("expected the release to be listed as waiting, got %+v", waiting)
	}
	if note, _ := waiting[0]["note"].(string); !strings.HasPrefix(note, "Waiting for a better protocol until ") {
		t.Fatalf("note = %q", note)
	}
	until, _ := time.Parse(time.RFC3339, waiting[0]["until"].(string))
	if d := time.Until(until); d < 100*time.Minute || d > 111*time.Minute {
		t.Fatalf("the wait should end in about 110 minutes, got %v", d)
	}
	events := getJSON[[]map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d/events", base, movie.ID))
	var delayed int
	for _, e := range events {
		if e["kind"] == "delayed" && strings.Contains(e["message"].(string), "Waiting for a better protocol until") {
			delayed++
		}
	}
	if delayed != 1 {
		t.Fatalf("expected one 'delayed' line in the title's activity, got %d in %+v", delayed, events)
	}
	// The same search again must not write the line again.
	server.TestHunt(context.Background())
	events = getJSON[[]map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d/events", base, movie.ID))
	delayed = 0
	for _, e := range events {
		if e["kind"] == "delayed" {
			delayed++
		}
	}
	if delayed != 1 {
		t.Fatalf("a second search wrote the delayed line again (%d)", delayed)
	}

	// The release is now three hours old: the next search takes it.
	k.ageMinutes.Store(180)
	server.TestHunt(context.Background())
	if got := queueTitles(t, client, base); len(got) != 1 || got[0] != knobRelease {
		t.Fatalf("after the wait the release must be grabbed, queue = %v", got)
	}
	waitForQueueTerminal(t, client, base)

	// The grab is counted for the indexer that gave the release.
	stats := getJSON[map[string]any](t, client, base+"/api/stats/indexers")
	rows := stats["indexers"].([]any)
	if len(rows) != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	row := rows[0].(map[string]any)
	if row["id"] != float64(indexerID) || row["grabs"] != float64(1) || row["failures"] != float64(0) || row["successRate"] != float64(1) {
		t.Fatalf("indexer stats = %+v", row)
	}
	if row["queries"].(float64) < 3 {
		t.Fatalf("expected the three hunts to be counted as searches, got %+v", row)
	}
}

func TestDelayDoesNotApplyToSearchNow(t *testing.T) {
	server, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	signIn(t, client, base)
	k := newKnobIndexer(t)
	addKnobIndexer(t, client, base, "Fixture Indexer", k)
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/search/delay", map[string]any{"usenetMinutes": 600}, http.StatusOK)
	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 702, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	out := postJSON[map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d/search-now", base, movie.ID), nil, http.StatusOK)
	if out["grabbed"] != float64(1) {
		t.Fatalf("a search started by hand is never delayed: %+v", out)
	}
	waitForQueueTerminal(t, client, base)
}

func TestDelayedGrabsJobSearchesWhenTheWaitIsOver(t *testing.T) {
	server, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	signIn(t, client, base)
	k := newKnobIndexer(t)
	addKnobIndexer(t, client, base, "Fixture Indexer", k)
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/search/delay", map[string]any{"usenetMinutes": 120}, http.StatusOK)
	if _, err := server.MovieRepo.Add(library.Movie{TMDBID: 703, Title: "The Fixture Movie", Year: 1999, Monitored: true}); err != nil {
		t.Fatal(err)
	}

	server.TestHunt(context.Background())
	if got := queueTitles(t, client, base); len(got) != 0 {
		t.Fatalf("held release was grabbed: %v", got)
	}
	// Nothing is due yet: the job leaves it alone.
	hitsBefore := k.hits.Load()
	server.TestDelayedGrabsJob(context.Background())
	if k.hits.Load() != hitsBefore {
		t.Fatal("the job searched before the wait was over")
	}

	// Two hours pass: the release is old enough and its wait is over.
	k.ageMinutes.Store(125)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if err := server.TestExec(`UPDATE delayed_releases SET due_at = ?`, past); err != nil {
		t.Fatal(err)
	}
	server.TestDelayedGrabsJob(context.Background())
	if got := queueTitles(t, client, base); len(got) != 1 || got[0] != knobRelease {
		t.Fatalf("the job must search again and grab the release, queue = %v", got)
	}
	waitForQueueTerminal(t, client, base)
	if waiting := getJSON[[]map[string]any](t, client, base+"/api/search/waiting"); len(waiting) != 0 {
		t.Fatalf("nothing should be waiting any more: %+v", waiting)
	}
}

func TestSizeLimitSettings(t *testing.T) {
	_, base, client := loginNewServer(t)
	table := getJSON[map[string]any](t, client, base+"/api/quality/size-limits")
	if table["enabled"] != false {
		t.Fatalf("size limits must be off on a fresh install: %+v", table)
	}
	rows := table["rows"].([]any)
	if len(rows) < 10 {
		t.Fatalf("expected a row per quality, got %d", len(rows))
	}
	find := func(tbl map[string]any, q string) map[string]any {
		for _, r := range tbl["rows"].([]any) {
			if r.(map[string]any)["quality"] == q {
				return r.(map[string]any)
			}
		}
		t.Fatalf("no row for %s", q)
		return nil
	}
	web := find(table, "WEBDL-1080p")
	if web["min"] != web["defaultMin"] || web["max"] != web["defaultMax"] {
		t.Fatalf("a fresh table shows the defaults: %+v", web)
	}

	change := func(tbl map[string]any, enabled bool, q string, min, max float64) map[string]any {
		rows := tbl["rows"].([]any)
		for _, r := range rows {
			if r.(map[string]any)["quality"] == q {
				r.(map[string]any)["min"], r.(map[string]any)["max"] = min, max
			}
		}
		return map[string]any{"enabled": enabled, "rows": rows}
	}
	saved := postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/quality/size-limits", change(table, true, "WEBDL-1080p", 10, 50), http.StatusOK)
	if saved["enabled"] != true || find(saved, "WEBDL-1080p")["max"] != float64(50) {
		t.Fatalf("saved = %+v", find(saved, "WEBDL-1080p"))
	}
	again := getJSON[map[string]any](t, client, base+"/api/quality/size-limits")
	if again["enabled"] != true || find(again, "WEBDL-1080p")["min"] != float64(10) || find(again, "WEBDL-1080p")["defaultMax"] != web["defaultMax"] {
		t.Fatalf("read back = %+v", find(again, "WEBDL-1080p"))
	}
	// An untouched quality keeps following the defaults.
	if find(again, "Bluray-1080p")["max"] != find(again, "Bluray-1080p")["defaultMax"] {
		t.Fatalf("Bluray row changed: %+v", find(again, "Bluray-1080p"))
	}

	for name, body := range map[string]map[string]any{
		"max below min": change(again, true, "WEBDL-1080p", 80, 20),
		"negative":      change(again, true, "WEBDL-1080p", -1, 20),
	} {
		t.Run(name, func(t *testing.T) {
			out := postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/quality/size-limits", body, http.StatusBadRequest)
			if out["error"] == "" || out["error"] == nil {
				t.Fatalf("expected a plain error, got %+v", out)
			}
			if kept := getJSON[map[string]any](t, client, base+"/api/quality/size-limits"); find(kept, "WEBDL-1080p")["max"] != float64(50) {
				t.Fatal("a refused table replaced the saved one")
			}
		})
	}

	reset := postJSON[map[string]any](t, client, base+"/api/quality/size-limits/reset", nil, http.StatusOK)
	if reset["enabled"] != true {
		t.Fatal("reset must keep the switch where it was")
	}
	if r := find(reset, "WEBDL-1080p"); r["min"] != r["defaultMin"] || r["max"] != r["defaultMax"] {
		t.Fatalf("reset row = %+v", r)
	}
}

func TestSizeLimitsRefuseReleasesInSearchAndByAutomation(t *testing.T) {
	server, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	signIn(t, client, base)
	k := newKnobIndexer(t)
	k.sizeBytes.Store(40 << 30) // 40 GB for a film of 100 assumed minutes: 410 MB a minute
	addKnobIndexer(t, client, base, "Fixture Indexer", k)
	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 704, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	searchReasons := func() []any {
		res := getJSON[[]map[string]any](t, client, fmt.Sprintf("%s/api/movies/%d/search", base, movie.ID))
		if len(res) != 1 {
			t.Fatalf("expected the one release, got %+v", res)
		}
		r, _ := res[0]["rejections"].([]any)
		return r
	}
	hasSizeReason := func(rs []any) bool {
		for _, r := range rs {
			if strings.Contains(r.(string), "too large for WEBDL-1080p") {
				return true
			}
		}
		return false
	}

	// Off (the default): no reason is listed.
	if hasSizeReason(searchReasons()) {
		t.Fatal("the size table is off, yet it refused a release")
	}

	// On: the interactive list says why, and automation does not take it.
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/quality/size-limits", map[string]any{"enabled": true, "rows": []any{}}, http.StatusOK)
	if !hasSizeReason(searchReasons()) {
		t.Fatalf("expected a 'too large' reason, got %v", searchReasons())
	}
	server.TestHunt(context.Background())
	waitBackground(t, server)
	if got := queueTitles(t, client, base); len(got) != 0 {
		t.Fatalf("automation took a release outside the size limits: %v", got)
	}

	// A smaller release (8 GB, 82 MB a minute) passes.
	k.sizeBytes.Store(8 << 30)
	if hasSizeReason(searchReasons()) {
		t.Fatal("a release inside the limits was refused")
	}
	server.TestHunt(context.Background())
	if got := queueTitles(t, client, base); len(got) != 1 {
		t.Fatalf("expected the release inside the limits to be grabbed, got %v", got)
	}
	waitForQueueTerminal(t, client, base)
}

func TestFailingIndexerIsPausedShownAndResumed(t *testing.T) {
	_, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	signIn(t, client, base)
	k := newKnobIndexer(t)
	k.status.Store(http.StatusServiceUnavailable)
	id := addKnobIndexer(t, client, base, "Shaky Indexer", k)

	search := func() {
		resp, err := client.Get(base + "/api/search?q=matrix")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	indexerRow := func() map[string]any {
		for _, r := range getJSON[[]map[string]any](t, client, base+"/api/indexers") {
			if r["id"] == float64(id) {
				return r
			}
		}
		t.Fatal("indexer missing from the list")
		return nil
	}

	search()
	search()
	if indexerRow()["pausedUntil"] != nil {
		t.Fatal("paused after two failures")
	}
	search()
	row := indexerRow()
	if row["pausedUntil"] == nil || row["failures"] != float64(3) {
		t.Fatalf("expected a pause after three failures: %+v", row)
	}
	if note, _ := row["pausedNote"].(string); !strings.Contains(note, "Shaky Indexer is paused until") || !strings.Contains(note, "3 failures in a row") {
		t.Fatalf("pausedNote = %q", note)
	}
	if hl := healthByID(t, client, base)[fmt.Sprintf("indexer-paused-%d", id)]; hl == nil || hl["level"] != "warn" {
		t.Fatalf("Health should list the pause: %v", hl)
	}

	// While paused it is not asked at all.
	hits := k.hits.Load()
	search()
	if k.hits.Load() != hits {
		t.Fatal("a paused indexer was asked")
	}

	// "Try again now" clears it; a working answer keeps it clear.
	k.status.Store(0)
	postJSON[any](t, client, fmt.Sprintf("%s/api/indexers/%d/resume", base, id), nil, http.StatusOK)
	if r := indexerRow(); r["pausedUntil"] != nil || r["failures"] != nil {
		t.Fatalf("still paused after Try again now: %+v", r)
	}
	search()
	if k.hits.Load() <= hits {
		t.Fatal("the resumed indexer was not asked")
	}
	if healthByID(t, client, base)[fmt.Sprintf("indexer-paused-%d", id)] != nil {
		t.Fatal("Health still lists the pause")
	}

	stats := getJSON[map[string]any](t, client, base+"/api/stats/indexers")["indexers"].([]any)[0].(map[string]any)
	if stats["queries"] != float64(4) || stats["failures"] != float64(3) {
		t.Fatalf("expected 4 searches and 3 failures, got %+v", stats)
	}
	if rate := stats["successRate"].(float64); rate != 0.25 {
		t.Fatalf("success rate = %v, want 0.25", rate)
	}
}

func TestRateLimitedIndexerWaitsAsLongAsItAsked(t *testing.T) {
	_, httpSrv, client := newHuntTestServer(t)
	base := httpSrv.URL
	signIn(t, client, base)
	k := newKnobIndexer(t)
	k.status.Store(http.StatusTooManyRequests)
	k.retryAfter.Store(3600)
	id := addKnobIndexer(t, client, base, "Busy Indexer", k)
	resp, err := client.Get(base + "/api/search?q=matrix")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var row map[string]any
	for _, r := range getJSON[[]map[string]any](t, client, base+"/api/indexers") {
		if r["id"] == float64(id) {
			row = r
		}
	}
	until, err := time.Parse(time.RFC3339, fmt.Sprint(row["pausedUntil"]))
	if err != nil {
		t.Fatalf("one rate-limit answer with Retry-After should pause the indexer: %+v", row)
	}
	if d := time.Until(until); d < 55*time.Minute || d > 61*time.Minute {
		t.Fatalf("paused for %v, want about an hour", d)
	}
}

// A release without a posting date counts from the moment it was first seen,
// and a show's episode is held back and grabbed like a movie.
func TestDelayHoldsAnEpisodeFromItsFirstSightingAndGrabsItLater(t *testing.T) {
	env := newTVAutoEnv(t, []string{s1e2WEB}, []episodeSpec{{1, 2, "2020-01-08", library.StatusMissing, ""}})
	postJSONMethod[map[string]any](t, env.client, http.MethodPut, env.baseURL+"/api/search/delay", map[string]any{"usenetMinutes": 60}, http.StatusOK)

	env.server.TestHunt(context.Background())
	requireTitles(t, env.grabbedTitles(t))
	waiting := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/search/waiting")
	if len(waiting) != 1 || waiting[0]["seriesId"] != float64(env.seriesID) || waiting[0]["season"] != float64(1) || waiting[0]["episode"] != float64(2) {
		t.Fatalf("expected S01E02 to be waiting, got %+v", waiting)
	}
	events := getJSON[[]map[string]any](t, env.client, fmt.Sprintf("%s/api/series/%d/events", env.baseURL, env.seriesID))
	var found bool
	for _, e := range events {
		found = found || e["kind"] == "delayed"
	}
	if !found {
		t.Fatalf("the show's activity should say the release is held back: %+v", events)
	}

	// Searching again changes nothing: the first sighting is remembered.
	env.server.TestHunt(context.Background())
	requireTitles(t, env.grabbedTitles(t))

	// An hour and more later (first seen then, wait over), it is taken.
	earlier := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	if err := env.server.TestExec(`UPDATE delayed_releases SET first_seen_at = ?, due_at = ?`, earlier, earlier); err != nil {
		t.Fatal(err)
	}
	env.server.TestHunt(context.Background())
	requireTitles(t, env.grabbedTitles(t), s1e2WEB)
	env.finish(t)
}
