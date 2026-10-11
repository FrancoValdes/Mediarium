package api_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rdborg/mediarium/internal/api"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/notify"
)

// portalServer is a server with the fake TMDB (movies 100 to 105, show 200),
// an administrator and a member, Sam, who has to ask for titles.
func portalServer(t *testing.T) (server *api.Server, base string, admin, sam *http.Client, samID float64) {
	t.Helper()
	server, base, admin, sam, samID = familyServer(t)
	server.TestSetTMDBBaseURL("fixture-tmdb-key", fakeListsTMDB(t).URL)
	perms := map[string]any{"addDirect": false, "movies": true, "tv": true, "music": true, "books": true, "releases": true, "manage": true, "retry": true, "subtitles": true, "play": true}
	postJSONMethod[map[string]any](t, admin, http.MethodPut, fmt.Sprintf("%s/api/users/%v", base, samID), map[string]any{"permissions": perms}, http.StatusOK)
	return
}

func putRequestSettings(t *testing.T, admin *http.Client, base string, body map[string]any, want int) map[string]any {
	t.Helper()
	return postJSONMethod[map[string]any](t, admin, http.MethodPut, base+"/api/requests/settings", body, want)
}

type messages struct {
	mu   sync.Mutex
	list []notify.Event
	to   []int64
}

func (m *messages) record(server *api.Server) {
	server.TestOnPersonMessage(func(userID int64, ev notify.Event) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.list = append(m.list, ev)
		m.to = append(m.to, userID)
	})
}

func (m *messages) take() ([]notify.Event, []int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	evs, to := m.list, m.to
	m.list, m.to = nil, nil
	return evs, to
}

func TestRequestQuotaPerPerson(t *testing.T) {
	_, base, admin, sam, samID := portalServer(t)
	ask := func(tmdb int, want int) map[string]any {
		return postJSON[map[string]any](t, sam, base+"/api/movies", map[string]any{"tmdbId": tmdb}, want)
	}

	// Off and unlimited until set.
	if q := getJSON[map[string]any](t, sam, base+"/api/requests/status")["quota"].(map[string]any); q["limit"] != float64(0) {
		t.Fatalf("a quota is on by default: %v", q)
	}
	ask(100, http.StatusAccepted)
	ask(101, http.StatusAccepted)
	ask(102, http.StatusAccepted)

	res := putRequestSettings(t, admin, base, map[string]any{"quotaCount": 4, "quotaPeriod": "week"}, http.StatusOK)
	if res["options"].(map[string]any)["quotaCount"] != float64(4) {
		t.Fatalf("settings: %v", res)
	}
	q := getJSON[map[string]any](t, sam, base+"/api/requests/status")["quota"].(map[string]any)
	if q["limit"] != float64(4) || q["used"] != float64(3) || q["left"] != float64(1) || q["period"] != "week" || q["resetsAt"] == nil {
		t.Fatalf("quota after three requests: %v", q)
	}
	ask(103, http.StatusAccepted)
	refused := ask(104, http.StatusTooManyRequests)
	if msg := refused["error"].(string); !strings.Contains(msg, "4 requests in the last week") || !strings.Contains(msg, "You can ask again in about") {
		t.Fatalf("refusal: %q", msg)
	}

	// A declined request stops counting.
	var first any
	for _, r := range getJSON[map[string]any](t, admin, base+"/api/requests")["requests"].([]any) {
		if m := r.(map[string]any); m["title"] == "Alpha" {
			first = m["id"]
		}
	}
	postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/requests/%v/decline", base, first), map[string]string{"note": "no"}, http.StatusOK)
	if q = getJSON[map[string]any](t, sam, base+"/api/requests/status")["quota"].(map[string]any); q["left"] != float64(1) {
		t.Fatalf("after a decline: %v", q)
	}
	ask(104, http.StatusAccepted)

	// One person's own number beats the default; 0 means no limit for them.
	putRequestSettings(t, admin, base, map[string]any{"quotas": map[string]int{fmt.Sprint(int(samID)): 4}}, http.StatusOK)
	if q = getJSON[map[string]any](t, sam, base+"/api/requests/status")["quota"].(map[string]any); q["limit"] != float64(4) || q["left"] != float64(0) {
		t.Fatalf("own limit: %v", q)
	}
	ask(105, http.StatusTooManyRequests)
	putRequestSettings(t, admin, base, map[string]any{"quotas": map[string]int{fmt.Sprint(int(samID)): 0}}, http.StatusOK)
	ask(105, http.StatusAccepted)
	settings := getJSON[map[string]any](t, admin, base+"/api/requests/settings")
	var gotQuota any = "missing"
	for _, a := range settings["accounts"].([]any) {
		if m := a.(map[string]any); m["id"] == samID {
			gotQuota = m["quota"]
			if m["mustAsk"] != true {
				t.Fatalf("Sam has to ask: %v", m)
			}
		}
	}
	if gotQuota != float64(0) {
		t.Fatalf("Sam's own limit in the settings: %v", gotQuota)
	}

	// The day period, and bad values.
	putRequestSettings(t, admin, base, map[string]any{"quotas": map[string]int{fmt.Sprint(int(samID)): -1}, "quotaPeriod": "day", "quotaCount": 2}, http.StatusOK)
	if q = getJSON[map[string]any](t, sam, base+"/api/requests/status")["quota"].(map[string]any); q["period"] != "day" || q["limit"] != float64(2) {
		t.Fatalf("day quota: %v", q)
	}
	putRequestSettings(t, admin, base, map[string]any{"quotaCount": -1}, http.StatusBadRequest)
	putRequestSettings(t, admin, base, map[string]any{"quotaPeriod": "month"}, http.StatusBadRequest)
	putRequestSettings(t, admin, base, map[string]any{"quotas": map[string]int{"abc": 1}}, http.StatusBadRequest)

	// Members can't change the settings.
	if status, _ := doStatus(t, sam, http.MethodPut, base+"/api/requests/settings"); status != http.StatusForbidden {
		t.Fatalf("a member changed the request settings: %d", status)
	}
}

func TestFourKRequest(t *testing.T) {
	server, base, admin, sam, _ := portalServer(t)
	copies := filepath.Join(t.TempDir(), "movies-4k")
	if err := os.MkdirAll(copies, 0o755); err != nil {
		t.Fatal(err)
	}
	var fourK float64
	profiles := getJSON[map[string]any](t, admin, base+"/api/quality-profiles")
	for _, p := range profiles["profiles"].([]any) {
		if m := p.(map[string]any); strings.HasPrefix(m["name"].(string), "4K") {
			fourK = m["id"].(float64)
		}
	}
	ask := func(tmdb int, want int) map[string]any {
		return postJSON[map[string]any](t, sam, base+"/api/movies", map[string]any{"tmdbId": tmdb, "fourK": true}, want)
	}

	// Off by default, and not available until a profile and a folder are set.
	if f := getJSON[map[string]any](t, sam, base+"/api/requests/status")["fourK"].(map[string]any); f["movie"] != false {
		t.Fatalf("4K is on by default: %v", f)
	}
	ask(100, http.StatusBadRequest)
	putRequestSettings(t, admin, base, map[string]any{"fourK": true}, http.StatusBadRequest) // no profile yet
	putRequestSettings(t, admin, base, map[string]any{"fourK": true, "fourKProfileId": fourK}, http.StatusOK)
	ask(100, http.StatusBadRequest) // no folder for second copies
	putJSONStatus(t, admin, base+"/api/settings", map[string]any{"movieCopiesPath": copies}, http.StatusOK)
	if f := getJSON[map[string]any](t, sam, base+"/api/requests/status")["fourK"].(map[string]any); f["movie"] != true || f["tv"] != false {
		t.Fatalf("4K should be available for movies only: %v", f)
	}

	// A 4K request for a title that isn't in the library yet.
	res := ask(100, http.StatusAccepted)
	if res["request"].(map[string]any)["fourK"] != true {
		t.Fatalf("request: %v", res)
	}
	ask(100, http.StatusConflict) // asked already
	// The same title without 4K is its own request.
	postJSON[map[string]any](t, sam, base+"/api/movies", map[string]any{"tmdbId": 100}, http.StatusAccepted)

	var fourKReq, plainReq any
	for _, r := range getJSON[map[string]any](t, admin, base+"/api/requests")["requests"].([]any) {
		m := r.(map[string]any)
		if m["fourK"] == true {
			fourKReq = m["id"]
		} else {
			plainReq = m["id"]
		}
	}
	// Approving it adds the movie and then its 4K copy.
	postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/requests/%v/approve", base, fourKReq), nil, http.StatusOK)
	waitBackground(t, server)
	movies := getJSON[[]map[string]any](t, admin, base+"/api/movies")
	if len(movies) != 1 {
		t.Fatalf("movies: %v", movies)
	}
	cp := getJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/movies/%v/copies", base, movies[0]["id"]))["items"].([]any)
	if len(cp) != 1 || cp[0].(map[string]any)["profileId"] != fourK {
		t.Fatalf("4K copy: %v", cp)
	}
	// The plain request now finds the movie there already.
	approved := postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/requests/%v/approve", base, plainReq), nil, http.StatusOK)
	if approved["note"] != "It was already in the library." {
		t.Fatalf("plain request after the 4K one: %v", approved)
	}
	// It has its 4K copy now; a title without one can still be asked for in 4K.
	ask(100, http.StatusConflict)
	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 103, Title: "Delta", Year: 2022, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	postJSON[map[string]any](t, sam, base+"/api/movies", map[string]any{"tmdbId": 103}, http.StatusConflict) // not 4K: already there
	req := ask(103, http.StatusAccepted)["request"].(map[string]any)
	postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/requests/%v/approve", base, req["id"]), nil, http.StatusOK)
	waitBackground(t, server)
	cp = getJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/movies/%d/copies", base, movie.ID))["items"].([]any)
	if len(cp) != 1 {
		t.Fatalf("copy of a title that was there already: %v", cp)
	}
}

func TestMessagesToThePersonWhoAsked(t *testing.T) {
	server, base, admin, sam, samID := portalServer(t)
	var got messages
	got.record(server)

	ask := func(tmdb int) any {
		res := postJSON[map[string]any](t, sam, base+"/api/movies", map[string]any{"tmdbId": tmdb}, http.StatusAccepted)
		return res["request"].(map[string]any)["id"]
	}
	// Nothing is sent until an administrator switches it on.
	one := ask(100)
	postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/requests/%v/approve", base, one), nil, http.StatusOK)
	waitBackground(t, server)
	if evs, _ := got.take(); len(evs) != 0 {
		t.Fatalf("messages were sent while the option is off: %v", evs)
	}

	putRequestSettings(t, admin, base, map[string]any{"notifyRequester": true}, http.StatusOK)
	two := ask(101)
	postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/requests/%v/approve", base, two), nil, http.StatusOK)
	three := ask(102)
	postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/requests/%v/decline", base, three), map[string]string{"note": "Not this one"}, http.StatusOK)
	waitBackground(t, server)
	evs, to := got.take()
	if len(evs) != 2 || to[0] != int64(samID) {
		t.Fatalf("messages: %+v to %v", evs, to)
	}
	titles := evs[0].Title + " | " + evs[1].Title
	if !strings.Contains(titles, "Your request was approved: Bravo (1995)") || !strings.Contains(titles, "Your request was declined: Charlie (2020)") {
		t.Fatalf("titles: %s", titles)
	}
	if !strings.Contains(evs[1].Message, "Reason: Not this one.") {
		t.Fatalf("decline message: %q", evs[1].Message)
	}

	// The check that spots the title in the library: Alpha was approved while
	// the option was off, so it is not announced; Bravo is, once it is downloaded.
	server.TestRequestAvailableJob(context.Background())
	waitBackground(t, server)
	if evs, _ = got.take(); len(evs) != 0 {
		t.Fatalf("nothing is downloaded yet: %v", evs)
	}
	for _, m := range getJSON[[]map[string]any](t, admin, base+"/api/movies") {
		if m["title"] == "Bravo" || m["title"] == "Alpha" {
			if err := server.MovieRepo.SetStatus(int64(m["id"].(float64)), library.StatusDownloaded, "1080p", "/movies/x.mkv"); err != nil {
				t.Fatal(err)
			}
		}
	}
	server.TestRequestAvailableJob(context.Background())
	waitBackground(t, server)
	evs, _ = got.take()
	if len(evs) != 2 {
		t.Fatalf("expected a message for each approved request that arrived: %v", evs)
	}
	server.TestRequestAvailableJob(context.Background())
	waitBackground(t, server)
	if evs, _ = got.take(); len(evs) != 0 {
		t.Fatalf("announced twice: %v", evs)
	}

	// Sam switches the messages off.
	notices := getJSON[map[string]any](t, sam, base+"/api/auth/request-notices")
	if notices["enabled"] != true || notices["serverOn"] != true || notices["hasEmail"] != false {
		t.Fatalf("notices: %v", notices)
	}
	postJSONMethod[map[string]any](t, sam, http.MethodPut, base+"/api/auth/request-notices", map[string]any{"enabled": false}, http.StatusOK)
	four := ask(104)
	postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/requests/%v/decline", base, four), nil, http.StatusOK)
	waitBackground(t, server)
	if evs, _ = got.take(); len(evs) != 0 {
		t.Fatalf("a message went to someone who opted out: %v", evs)
	}
}

func TestPushDevicesOfAnAccount(t *testing.T) {
	_, base, admin, sam, _ := portalServer(t)
	b64 := func(n int) string {
		raw := make([]byte, n)
		raw[0] = 4
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	sub := func(endpoint string) string {
		return fmt.Sprintf(`{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q}}`, endpoint, b64(65), b64(16))
	}
	postJSON[map[string]any](t, sam, base+"/api/auth/request-notices/devices", map[string]any{"subscription": "nonsense"}, http.StatusBadRequest)
	postJSON[map[string]any](t, sam, base+"/api/auth/request-notices/devices", map[string]any{"subscription": sub("http://push.example/a")}, http.StatusBadRequest)
	res := postJSON[map[string]any](t, sam, base+"/api/auth/request-notices/devices", map[string]any{"subscription": sub("https://push.example/a"), "name": "Sam's phone"}, http.StatusOK)
	devices := res["devices"].([]any)
	if len(devices) != 1 || devices[0].(map[string]any)["name"] != "Sam's phone" {
		t.Fatalf("devices: %v", res)
	}
	// The same browser again replaces its entry.
	res = postJSON[map[string]any](t, sam, base+"/api/auth/request-notices/devices", map[string]any{"subscription": sub("https://push.example/a"), "name": "Phone"}, http.StatusOK)
	if len(res["devices"].([]any)) != 1 {
		t.Fatalf("same browser twice: %v", res)
	}
	// Someone else's list is theirs alone.
	if n := len(getJSON[map[string]any](t, admin, base+"/api/auth/request-notices")["devices"].([]any)); n != 0 {
		t.Fatalf("the administrator sees Sam's devices: %d", n)
	}
	id := res["devices"].([]any)[0].(map[string]any)["id"]
	if status, _ := doStatus(t, admin, http.MethodDelete, fmt.Sprintf("%s/api/auth/request-notices/devices/%v", base, id)); status != http.StatusNotFound {
		t.Fatalf("removing another account's device: %d", status)
	}
	for i := 0; i < 4; i++ {
		postJSON[map[string]any](t, sam, base+"/api/auth/request-notices/devices", map[string]any{"subscription": sub(fmt.Sprintf("https://push.example/%d", i+10))}, http.StatusOK)
	}
	postJSON[map[string]any](t, sam, base+"/api/auth/request-notices/devices", map[string]any{"subscription": sub("https://push.example/too-many")}, http.StatusConflict)
	if status, _ := doStatus(t, sam, http.MethodDelete, fmt.Sprintf("%s/api/auth/request-notices/devices/%v", base, id)); status != http.StatusOK {
		t.Fatalf("removing own device: %d", status)
	}
}

func TestReportAProblem(t *testing.T) {
	server, base, admin, sam, _ := portalServer(t)
	var got messages
	got.record(server)
	movie := postJSON[map[string]any](t, admin, base+"/api/movies", map[string]any{"tmdbId": 100}, http.StatusCreated)
	report := func(body map[string]any, want int) map[string]any {
		body["kind"], body["itemId"] = "movie", movie["id"]
		return postJSON[map[string]any](t, sam, base+"/api/issues", body, want)
	}

	// Off until an administrator switches it on.
	report(map[string]any{"category": "no-sound"}, http.StatusConflict)
	putRequestSettings(t, admin, base, map[string]any{"reports": true, "notifyRequester": true}, http.StatusOK)
	if getJSON[map[string]any](t, sam, base+"/api/requests/status")["reports"] != true {
		t.Fatal("the page can't tell reports are on")
	}

	report(map[string]any{"category": "weird"}, http.StatusBadRequest)
	report(map[string]any{"category": "other"}, http.StatusBadRequest) // "something else" needs words
	created := report(map[string]any{"category": "no-sound", "message": "The voices are missing after the first hour."}, http.StatusCreated)
	if created["title"] != "Alpha" || created["status"] != "open" || created["reporter"] != "Sam" {
		t.Fatalf("report: %v", created)
	}
	report(map[string]any{"category": "no-sound"}, http.StatusConflict) // already reported
	postJSON[map[string]any](t, sam, base+"/api/issues", map[string]any{"kind": "movie", "itemId": 9999, "category": "no-sound"}, http.StatusNotFound)
	postJSON[map[string]any](t, sam, base+"/api/issues", map[string]any{"kind": "music", "itemId": 1, "category": "no-sound"}, http.StatusBadRequest)

	// Sam sees his own; the administrator sees all.
	mine := getJSON[map[string]any](t, sam, base+"/api/issues")
	if mine["open"] != float64(1) || len(mine["issues"].([]any)) != 1 {
		t.Fatalf("Sam's reports: %v", mine)
	}
	all := getJSON[map[string]any](t, admin, base+"/api/issues")
	if len(all["issues"].([]any)) != 1 {
		t.Fatalf("all reports: %v", all)
	}

	// Only an administrator answers.
	id := created["id"]
	if status, _ := doStatus(t, sam, http.MethodPost, fmt.Sprintf("%s/api/issues/%v/resolve", base, id)); status != http.StatusForbidden {
		t.Fatalf("a member resolved a report: %d", status)
	}
	resolved := postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/issues/%v/resolve", base, id), map[string]string{"comment": "Replaced the file with one that has sound."}, http.StatusOK)
	if resolved["status"] != "resolved" || resolved["comment"] != "Replaced the file with one that has sound." || resolved["resolvedAt"] == "" {
		t.Fatalf("resolved: %v", resolved)
	}
	waitBackground(t, server)
	evs, _ := got.take()
	if len(evs) != 1 || evs[0].Title != "Your report about Alpha was answered" || !strings.Contains(evs[0].Message, "Replaced the file with one that has sound.") {
		t.Fatalf("the reporter wasn't told: %+v", evs)
	}
	// Answering again does not message again; reopening and deleting work.
	postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/issues/%v/resolve", base, id), map[string]string{"comment": "again"}, http.StatusOK)
	waitBackground(t, server)
	if evs, _ = got.take(); len(evs) != 0 {
		t.Fatalf("told twice: %v", evs)
	}
	reopened := postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/issues/%v/reopen", base, id), nil, http.StatusOK)
	if reopened["status"] != "open" {
		t.Fatalf("reopen: %v", reopened)
	}
	if status, _ := doStatus(t, sam, http.MethodDelete, fmt.Sprintf("%s/api/issues/%v", base, id)); status != http.StatusForbidden {
		t.Fatalf("a member deleted a report: %d", status)
	}
	if status, _ := doStatus(t, admin, http.MethodDelete, fmt.Sprintf("%s/api/issues/%v", base, id)); status != http.StatusNoContent {
		t.Fatalf("delete: %d", status)
	}
	postJSON[map[string]any](t, admin, fmt.Sprintf("%s/api/issues/%v/resolve", base, id), nil, http.StatusNotFound)

	// Shows can be reported too, naming an episode.
	show := postJSON[map[string]any](t, admin, base+"/api/series", map[string]any{"tmdbId": 200}, http.StatusCreated)
	r := postJSON[map[string]any](t, sam, base+"/api/issues", map[string]any{"kind": "tv", "itemId": show["id"], "category": "bad-subtitles", "episode": "S01E01"}, http.StatusCreated)
	if r["title"] != "Show One" || r["episode"] != "S01E01" {
		t.Fatalf("show report: %v", r)
	}
}
