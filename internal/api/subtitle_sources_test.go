package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

// The three subtitle sources (OpenSubtitles, SubDL, add-ons) each have an
// on/off switch. A source that is switched off is never asked, by any search.

// threeSources sets up OpenSubtitles, SubDL and one add-on, each a fake server
// that counts how often it is searched.
type threeSources struct {
	env       *tvAutoEnv
	os        *fakeOpenSubtitles
	subdl     *fakeSubDL
	addonHits atomic.Int32
}

func newThreeSources(t *testing.T) *threeSources {
	t.Helper()
	ts := &threeSources{env: newTVAutoEnv(t, nil, nil)}
	ts.os = newFakeOpenSubtitles(t)
	ts.env.server.TestSetSubtitlesBaseURL("os-key", ts.os.srv.URL)
	ts.subdl = newFakeSubDL(t)
	ts.env.server.TestSetSubDLBaseURL("subdl-key", ts.subdl.srv.URL)

	tmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "title": "Some Movie", "release_date": "2001-01-01", "imdb_id": "tt0000009"})
	}))
	t.Cleanup(tmdb.Close)
	ts.env.server.TestSetTMDBBaseURL("k", tmdb.URL)

	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x.subs","name":"Fixture Subs","version":"1","resources":["subtitles"],"types":["movie","series"]}`))
	})
	mux.HandleFunc("/subtitles/", func(w http.ResponseWriter, r *http.Request) {
		ts.addonHits.Add(1)
		_, _ = w.Write([]byte(`{"subtitles":[{"id":"9","url":"http://` + r.Host + `/file.srt","lang":"eng","title":"Some.Movie.2001.1080p.WEB-GRP"}]}`))
	})
	mux.HandleFunc("/file.srt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nFrom the add-on\n"))
	})
	addon := httptest.NewServer(mux)
	t.Cleanup(addon.Close)
	postJSON[map[string]any](t, ts.env.client, ts.env.baseURL+"/api/subtitles/addons", map[string]string{"url": addon.URL + "/manifest.json"}, http.StatusCreated)
	return ts
}

func (ts *threeSources) set(t *testing.T, osOn, subdlOn, addonsOn bool) {
	t.Helper()
	putJSONStatus(t, ts.env.client, ts.env.baseURL+"/api/settings", map[string]any{
		"openSubtitlesEnabled": osOn, "subdlEnabled": subdlOn, "subtitleAddonsEnabled": addonsOn,
	}, http.StatusOK)
}

// hits are how many searches each source has answered so far.
func (ts *threeSources) hits() (osN, subdlN, addonN int32) {
	return ts.os.searches.Load(), ts.subdl.searches.Load(), ts.addonHits.Load()
}

func TestOnlyTheSourcesSwitchedOnAreSearched(t *testing.T) {
	cases := []struct {
		name                   string
		os, subdl, addons      bool
		wantOS, wantSD, wantAd bool
	}{
		{"all three", true, true, true, true, true, true},
		{"OpenSubtitles off", false, true, true, false, true, true},
		{"SubDL off", true, false, true, true, false, true},
		{"add-ons off", true, true, false, true, true, false},
		{"only OpenSubtitles", true, false, false, true, false, false},
		{"only SubDL", false, true, false, false, true, false},
		{"only add-ons", false, false, true, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := newThreeSources(t)
			m := seedDownloadedMovie(t, ts.env, 9, "Some Movie")
			ts.set(t, c.os, c.subdl, c.addons)
			url := fmt.Sprintf("%s/api/movies/%d/subtitles?lang=en", ts.env.baseURL, m.ID)

			// A manual search.
			list := getJSON[[]map[string]any](t, ts.env.client, url)
			got := map[string]bool{}
			for _, r := range list {
				src, _ := r["source"].(string)
				if src == "" {
					src = "opensubtitles"
				}
				got[src] = true
			}
			want := map[string]bool{"opensubtitles": c.wantOS, "subdl": c.wantSD, "addon": c.wantAd}
			for src, on := range want {
				if got[src] != on {
					t.Errorf("manual search: results from %s = %v, want %v (all: %v)", src, got[src], on, got)
				}
			}
			osN, sdN, adN := ts.hits()
			if (osN > 0) != c.wantOS || (sdN > 0) != c.wantSD || (adN > 0) != c.wantAd {
				t.Errorf("manual search asked os=%d subdl=%d addon=%d, want os=%v subdl=%v addon=%v", osN, sdN, adN, c.wantOS, c.wantSD, c.wantAd)
			}

			// The "get subtitles" job asks the same sources and no others.
			before := [3]int32{osN, sdN, adN}
			res := postJSON[map[string]any](t, ts.env.client, ts.env.baseURL+"/api/subtitles/get", map[string]any{"all": true}, http.StatusOK)
			if res["downloaded"] != float64(1) {
				t.Errorf("get: %+v", res)
			}
			osN, sdN, adN = ts.hits()
			if (osN > before[0]) != c.wantOS || (sdN > before[1]) != c.wantSD || (adN > before[2]) != c.wantAd {
				t.Errorf("get job asked os=%d subdl=%d addon=%d (before %v), want os=%v subdl=%v addon=%v", osN, sdN, adN, before, c.wantOS, c.wantSD, c.wantAd)
			}
		})
	}
}

func TestAutomaticSweepSkipsSourcesSwitchedOff(t *testing.T) {
	ts := newThreeSources(t)
	seedDownloadedMovie(t, ts.env, 9, "Some Movie")
	putJSONStatus(t, ts.env.client, ts.env.baseURL+"/api/settings", map[string]any{"subtitleAutoDownload": true}, http.StatusOK)
	ts.set(t, false, true, false)
	ts.env.server.TestSubtitleSweepJob(context.Background())
	osN, sdN, adN := ts.hits()
	if osN != 0 || adN != 0 || sdN == 0 {
		t.Fatalf("the sweep asked os=%d subdl=%d addon=%d, want SubDL only", osN, sdN, adN)
	}
}

func TestNothingIsAskedWhenEverySourceIsSwitchedOff(t *testing.T) {
	ts := newThreeSources(t)
	m := seedDownloadedMovie(t, ts.env, 9, "Some Movie")
	putJSONStatus(t, ts.env.client, ts.env.baseURL+"/api/settings", map[string]any{"subtitleAutoDownload": true}, http.StatusOK)
	ts.set(t, false, false, false)

	for _, req := range []struct{ method, path string }{
		{http.MethodGet, fmt.Sprintf("/api/movies/%d/subtitles?lang=en", m.ID)},
		{http.MethodPost, "/api/subtitles/get"},
		{http.MethodPost, "/api/subtitles/sweep"},
	} {
		var body any
		if req.path == "/api/subtitles/get" {
			body = map[string]any{"all": true}
		}
		var status int
		var reply map[string]any
		if req.method == http.MethodGet {
			status, reply = doStatus(t, ts.env.client, req.method, ts.env.baseURL+req.path)
		} else {
			status, reply = doJSONStatus(t, ts.env.client, req.method, ts.env.baseURL+req.path, body)
		}
		if status != http.StatusPreconditionFailed || !strings.Contains(fmt.Sprint(reply["error"]), "No subtitle source is switched on") {
			t.Errorf("%s %s: %d %v, want 412 saying no source is switched on", req.method, req.path, status, reply)
		}
	}
	ts.env.server.TestSubtitleSweepJob(context.Background())
	if osN, sdN, adN := ts.hits(); osN+sdN+adN != 0 {
		t.Fatalf("a source was asked: os=%d subdl=%d addon=%d", osN, sdN, adN)
	}

	q := getJSON[map[string]any](t, ts.env.client, ts.env.baseURL+"/api/subtitles/quota")
	if q["hasSource"] != false || q["sourcesSetUp"] != true || q["hasKey"] != false {
		t.Fatalf("quota should say every source is off: %+v", q)
	}
	health := healthItemIDs(t, ts.env)
	if _, ok := health["subtitles-no-source"]; !ok {
		t.Fatalf("expected the no-source notice, got %v", health)
	}
	if _, ok := health["subtitles-off"]; ok {
		t.Fatal("the not-set-up notice is for when nothing is set up")
	}
}

func TestDownloadFromASourceSwitchedOffIsRefused(t *testing.T) {
	ts := newThreeSources(t)
	m := seedDownloadedMovie(t, ts.env, 9, "Some Movie")
	ts.set(t, true, false, true)
	url := fmt.Sprintf("%s/api/movies/%d/subtitles/download", ts.env.baseURL, m.ID)
	// A SubDL result an earlier search listed, picked after SubDL was switched off.
	r := postJSON[map[string]any](t, ts.env.client, url, map[string]any{"source": "subdl", "link": "/subtitle/11-22.zip", "language": "en"}, http.StatusConflict)
	if !strings.Contains(fmt.Sprint(r["error"]), "SubDL is switched off") {
		t.Fatalf("reply: %v", r)
	}
	if ts.subdl.downloads.Load() != 0 {
		t.Fatal("SubDL was asked for the file")
	}
}

func TestSourceSwitchesInTheSettingsPayload(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	sources := func() map[string]map[string]any {
		s := getJSON[map[string]any](t, env.client, env.baseURL+"/api/settings")
		out := map[string]map[string]any{}
		for _, raw := range s["subtitleSources"].([]any) {
			row := raw.(map[string]any)
			out[row["id"].(string)] = row
		}
		if len(out) != 3 {
			t.Fatalf("expected three sources, got %v", s["subtitleSources"])
		}
		for key, id := range map[string]string{"openSubtitlesEnabled": "opensubtitles", "subdlEnabled": "subdl", "subtitleAddonsEnabled": "addons"} {
			if s[key] != out[id]["enabled"] {
				t.Fatalf("%s = %v but the %s row says %v", key, s[key], id, out[id]["enabled"])
			}
		}
		return out
	}

	// A new install: every switch reads on (today's behaviour), nothing is set up.
	got := sources()
	for id, row := range got {
		if row["enabled"] != true || row["ready"] != false || row["state"] != "needs_key" {
			t.Errorf("%s on a new install: %v", id, row)
		}
	}

	// A source can be switched off without a key, but not on again until it has one.
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subdlEnabled": false}, http.StatusOK)
	r := putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subdlEnabled": true}, http.StatusBadRequest)
	if !strings.Contains(fmt.Sprint(r["error"]), "SubDL API key first") {
		t.Fatalf("reply: %v", r)
	}
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"openSubtitlesEnabled": false}, http.StatusOK)
	r = putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"openSubtitlesEnabled": true}, http.StatusBadRequest)
	if !strings.Contains(fmt.Sprint(r["error"]), "OpenSubtitles API key first") {
		t.Fatalf("reply: %v", r)
	}
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleAddonsEnabled": false}, http.StatusOK)
	r = putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleAddonsEnabled": true}, http.StatusBadRequest)
	if !strings.Contains(fmt.Sprint(r["error"]), "add-on first") {
		t.Fatalf("reply: %v", r)
	}

	// A key saved in the same request makes it fine; and a saved key keeps the
	// source's own switch as it was.
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subdlApiKey": "a-fake-subdl-key", "subdlEnabled": true}, http.StatusOK)
	if row := sources()["subdl"]; row["state"] != "on" || row["ready"] != true {
		t.Fatalf("subdl: %v", row)
	}
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subdlEnabled": false}, http.StatusOK)
	if row := sources()["subdl"]; row["state"] != "off" || row["enabled"] != false || row["ready"] != true {
		t.Fatalf("subdl switched off: %v", row)
	}
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subdlApiKey": "another-fake-subdl-key"}, http.StatusOK)
	if row := sources()["subdl"]; row["state"] != "off" {
		t.Fatalf("a new key should not switch a source back on: %v", row)
	}
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subdlEnabled": true}, http.StatusOK)
	if row := sources()["subdl"]; row["state"] != "on" {
		t.Fatalf("subdl switched on: %v", row)
	}

	// Sending the whole object back (every switch as it reads) is accepted.
	whole := getJSON[map[string]any](t, env.client, env.baseURL+"/api/settings")
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{
		"subdlEnabled": whole["subdlEnabled"], "openSubtitlesEnabled": whole["openSubtitlesEnabled"], "subtitleAddonsEnabled": whole["subtitleAddonsEnabled"],
	}, http.StatusOK)
}

func TestOpenSubtitlesLimitShowsOnlyWhileItIsSwitchedOn(t *testing.T) {
	ts := newThreeSources(t)
	seedDownloadedMovie(t, ts.env, 9, "Some Movie")
	// Today's OpenSubtitles downloads are used up.
	ts.os.dlLimit.Store(1)
	seedDownloadedMovie(t, ts.env, 10, "Other Movie")
	ts.set(t, true, false, false)
	postJSON[map[string]any](t, ts.env.client, ts.env.baseURL+"/api/subtitles/get", map[string]any{"all": true}, http.StatusOK)

	states := func() []string {
		s := getJSON[map[string]any](t, ts.env.client, ts.env.baseURL+"/api/settings")
		var out []string
		for _, raw := range s["subtitleSources"].([]any) {
			row := raw.(map[string]any)
			out = append(out, row["id"].(string)+"="+row["state"].(string))
		}
		sort.Strings(out)
		return out
	}
	if got := strings.Join(states(), " "); got != "addons=off opensubtitles=limit subdl=off" {
		t.Fatalf("with its limit used up: %s", got)
	}
	// Switched off, its limit counts for nothing: it reads as off, and the quota says no key is in play.
	ts.set(t, false, true, false)
	if got := strings.Join(states(), " "); got != "addons=off opensubtitles=off subdl=on" {
		t.Fatalf("switched off: %s", got)
	}
	if q := getJSON[map[string]any](t, ts.env.client, ts.env.baseURL+"/api/subtitles/quota"); q["hasKey"] != false || q["hasSource"] != true {
		t.Fatalf("quota: %+v", q)
	}
}

func TestSubtitleOfferSpeaksOnlyOfSourcesThatAreOn(t *testing.T) {
	cases := []struct {
		name              string
		os, subdl, addons bool
		contains          []string
		absent            []string
	}{
		{"OpenSubtitles alone", true, false, false, []string{"About 5 downloads left today"}, nil},
		{"SubDL alone", false, true, false, []string{"Subtitles download only when you ask."}, []string{"left today", "OpenSubtitles"}},
		{"add-ons alone", false, false, true, []string{"Subtitles download only when you ask."}, []string{"left today", "OpenSubtitles"}},
		{"OpenSubtitles and SubDL", true, true, false, []string{"left today on OpenSubtitles, then the other sources carry on"}, []string{"the rest takes"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := newThreeSources(t)
			seedDownloadedMovie(t, ts.env, 9, "Some Movie")
			ts.set(t, c.os, c.subdl, c.addons)
			offer := healthItemIDs(t, ts.env)["subtitles-offer"]
			if offer == nil {
				t.Fatalf("expected an offer, got %v", healthItemIDs(t, ts.env))
			}
			impact := offer["impact"].(string)
			for _, s := range c.contains {
				if !strings.Contains(impact, s) {
					t.Errorf("impact %q should contain %q", impact, s)
				}
			}
			for _, s := range c.absent {
				if strings.Contains(impact, s) {
					t.Errorf("impact %q should not contain %q", impact, s)
				}
			}
		})
	}
}
