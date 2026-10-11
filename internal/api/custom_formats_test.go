package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const trashFixtures = "../quality/testdata/trash"

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(trashFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func formatIDByName(t *testing.T, formats []any, name string) float64 {
	t.Helper()
	for _, raw := range formats {
		f := raw.(map[string]any)
		if f["name"] == name {
			return f["id"].(float64)
		}
	}
	t.Fatalf("no format %q in %v", name, formats)
	return 0
}

func TestCustomFormatsPasteScoreAndTest(t *testing.T) {
	_, httpSrv, client := newHuntTestServer(t)
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/onboarding/admin", map[string]string{
		"username": "ryan", "password": "correct-horse-battery-staple", "firstName": "Ryan", "lastName": "Tester",
	}, http.StatusCreated)
	base := httpSrv.URL + "/api/custom-formats"

	// Custom formats stay out of sight until someone has some, or asks.
	if s := getJSON[map[string]any](t, client, httpSrv.URL+"/api/settings"); s["showAdvanced"] != false {
		t.Fatalf("advanced options should start hidden: %v", s["showAdvanced"])
	}

	// Bad input.
	postJSON[map[string]any](t, client, base, map[string]any{"json": "", "app": "radarr"}, http.StatusBadRequest)
	postJSON[map[string]any](t, client, base, map[string]any{"json": "{nope", "app": "radarr"}, http.StatusBadRequest)
	postJSON[map[string]any](t, client, base, map[string]any{"json": "{}", "app": "lidarr"}, http.StatusBadRequest)

	// A list pasted in one go, then the same format again: it is updated.
	list := "[" + fixture(t, "radarr-x265-hd.json") + "," + fixture(t, "radarr-hdr.json") + "," + fixture(t, "radarr-1080p.json") + "]"
	res := postJSON[map[string]any](t, client, base, map[string]any{"json": list, "app": "radarr"}, http.StatusOK)
	if res["added"].(float64) != 3 || res["updated"].(float64) != 0 {
		t.Fatalf("first import: %v", res)
	}
	res = postJSON[map[string]any](t, client, base, map[string]any{"json": fixture(t, "radarr-hdr.json"), "app": "radarr"}, http.StatusOK)
	if res["added"].(float64) != 0 || res["updated"].(float64) != 1 {
		t.Fatalf("second import: %v", res)
	}
	if s := getJSON[map[string]any](t, client, httpSrv.URL+"/api/settings"); s["showAdvanced"] != true {
		t.Fatalf("with formats imported, advanced options show: %v", s["showAdvanced"])
	}
	putJSONStatus(t, client, httpSrv.URL+"/api/settings", map[string]any{"showAdvanced": false}, http.StatusOK)
	if s := getJSON[map[string]any](t, client, httpSrv.URL+"/api/settings"); s["showAdvanced"] != false {
		t.Fatalf("switched off: %v", s["showAdvanced"])
	}
	formats := getJSON[map[string]any](t, client, base)["formats"].([]any)
	if len(formats) != 3 {
		t.Fatalf("want 3 formats, got %v", formats)
	}
	x265 := formatIDByName(t, formats, "x265 (HD)")
	hdr := formatIDByName(t, formats, "HDR")

	// Test a name without a profile: just the formats it matches.
	got := postJSON[map[string]any](t, client, base+"/test", map[string]any{"title": "Night.Harbour.2021.1080p.BluRay.HDR.x265-GRP"}, http.StatusOK)
	if names := matchedNames(got); names != "1080p,HDR,x265 (HD)" {
		t.Fatalf("matched %q", names)
	}

	// Scores in a profile: x265 refused through the minimum score, HDR a bonus.
	profiles := getJSON[map[string]any](t, client, httpSrv.URL+"/api/quality-profiles")
	pid := profileIDByName(t, profiles, "1080p")
	pURL := httpSrv.URL + "/api/quality-profiles/" + itoa(pid)
	updated := putJSONStatus(t, client, pURL, map[string]any{
		"name": "1080p", "allowed": []string{"WEBDL-1080p", "Bluray-1080p"}, "cutoff": "Bluray-1080p", "upgradeAllowed": true,
		"formatScores":   []map[string]any{{"formatId": x265, "score": -10000}, {"formatId": hdr, "score": 500}, {"formatId": 99999, "score": 7}},
		"minFormatScore": 0, "cutoffFormatScore": 1000, "noSeasonPacks": true,
	}, http.StatusOK)
	if fs := updated["formatScores"].([]any); len(fs) != 2 {
		t.Fatalf("an unknown format id should be dropped: %v", fs)
	}
	if updated["noSeasonPacks"] != true || updated["cutoffFormatScore"].(float64) != 1000 || updated["skipBadReleases"] != true {
		t.Fatalf("profile: %v", updated)
	}
	// Leaving the format fields out keeps them.
	kept := putJSONStatus(t, client, pURL, map[string]any{
		"name": "1080p", "allowed": []string{"WEBDL-1080p", "Bluray-1080p"}, "cutoff": "Bluray-1080p", "upgradeAllowed": true,
	}, http.StatusOK)
	if len(kept["formatScores"].([]any)) != 2 || kept["noSeasonPacks"] != true || kept["cutoffFormatScore"].(float64) != 1000 {
		t.Fatalf("an update without the format fields should keep them: %v", kept)
	}

	got = postJSON[map[string]any](t, client, base+"/test", map[string]any{"title": "Night.Harbour.2021.1080p.BluRay.HDR.x265-GRP", "profileId": pid}, http.StatusOK)
	if got["score"].(float64) != -9500 || got["accepted"] != false || !strings.Contains(got["reason"].(string), "Scores -9500") {
		t.Fatalf("x265 release: %v", got)
	}
	got = postJSON[map[string]any](t, client, base+"/test", map[string]any{"title": "Night.Harbour.2021.1080p.BluRay.HDR.x264-GRP", "profileId": pid}, http.StatusOK)
	if got["score"].(float64) != 500 || got["accepted"] != true {
		t.Fatalf("x264 HDR release: %v", got)
	}
	got = postJSON[map[string]any](t, client, base+"/test", map[string]any{"title": "Night.Harbour.S01.1080p.WEB-DL.x264-GRP", "profileId": pid}, http.StatusOK)
	if got["accepted"] != false || !strings.Contains(got["reason"].(string), "season pack") {
		t.Fatalf("season pack: %v", got)
	}

	// Rename, then delete: the score goes from the profile too.
	renamed := putJSONStatus(t, client, base+"/"+itoa(int64(hdr)), map[string]any{"name": "HDR (any)"}, http.StatusOK)
	if renamed["name"] != "HDR (any)" || len(renamed["usedBy"].([]any)) != 1 {
		t.Fatalf("rename: %v", renamed)
	}
	putJSONStatus(t, client, base+"/"+itoa(int64(hdr)), map[string]any{"name": "1080p"}, http.StatusConflict)
	if code := deleteReq(t, client, base+"/"+itoa(int64(hdr))); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	after := profileByID(t, client, httpSrv.URL, pid)
	if len(after["formatScores"].([]any)) != 1 {
		t.Fatalf("the deleted format's score should be gone: %v", after["formatScores"])
	}
}

func profileByID(t *testing.T, client *http.Client, base string, id int64) map[string]any {
	t.Helper()
	list := getJSON[map[string]any](t, client, base+"/api/quality-profiles")
	for _, raw := range list["profiles"].([]any) {
		if p := raw.(map[string]any); int64(p["id"].(float64)) == id {
			return p
		}
	}
	t.Fatalf("no profile %d", id)
	return nil
}

func matchedNames(res map[string]any) string {
	var out []string
	for _, raw := range res["matched"].([]any) {
		out = append(out, raw.(map[string]any)["name"].(string))
	}
	return strings.Join(out, ",")
}

// fakeTrashGuides serves the fixtures the way GitHub's contents API and
// raw.githubusercontent.com do.
func fakeTrashGuides(t *testing.T, profile string) *httptest.Server {
	t.Helper()
	cf := map[string]string{}
	entries, _ := os.ReadDir(trashFixtures)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "radarr-") && !strings.Contains(e.Name(), "profile") {
			cf[strings.TrimPrefix(e.Name(), "radarr-")] = fixture(t, e.Name())
		}
	}
	folders := map[string]map[string]string{
		"radarr/cf":               cf,
		"radarr/quality-profiles": {"made-up.json": profile},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no user agent", http.StatusForbidden)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"):
			files, ok := folders[strings.TrimPrefix(r.URL.Path, "/api/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			var list []map[string]string
			for name := range files {
				list = append(list, map[string]string{"name": name, "type": "file"})
			}
			list = append(list, map[string]string{"name": "sub", "type": "dir"})
			_ = json.NewEncoder(w).Encode(list)
		case strings.HasPrefix(r.URL.Path, "/raw/"):
			p := strings.TrimPrefix(r.URL.Path, "/raw/")
			i := strings.LastIndex(p, "/")
			body, ok := folders[p[:i]][p[i+1:]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTrashGuidesImport(t *testing.T) {
	server, httpSrv, client := newHuntTestServer(t)
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/onboarding/admin", map[string]string{
		"username": "ryan", "password": "correct-horse-battery-staple", "firstName": "Ryan", "lastName": "Tester",
	}, http.StatusCreated)
	profile := `{
	  "trash_id": "0000made0up0000", "name": "1080p", "upgradeAllowed": true, "cutoff": "WEB 1080p",
	  "minFormatScore": 0, "cutoffFormatScore": 10000,
	  "items": [
	    {"name": "WEB 1080p", "allowed": true, "items": ["WEBRip-1080p", "WEBDL-1080p"]},
	    {"name": "Bluray-720p", "allowed": true},
	    {"name": "BR-DISK", "allowed": false}
	  ],
	  "formatItems": {"x265 (HD)": "dc98083864ea246d05a42df0d05f81cc", "HDR": "493b6d1dbec3c3364c59d7607f7e3405", "Gone": "ffffffffffffffffffffffffffffffff"}
	}`
	fake := fakeTrashGuides(t, profile)
	server.SetTrashGuidesSource(fake.URL+"/api", fake.URL+"/raw")
	base := httpSrv.URL + "/api/trash-guides"

	if code, _ := doJSONStatus(t, client, http.MethodGet, base+"/lidarr", nil); code != http.StatusBadRequest {
		t.Fatalf("an unknown app: %d", code)
	}
	listing := getJSON[map[string]any](t, client, base+"/radarr")
	if n := len(listing["formats"].([]any)); n != 9 {
		t.Fatalf("want the 9 fixture formats, got %d", n)
	}
	profiles := listing["profiles"].([]any)
	if len(profiles) != 1 || profiles[0].(map[string]any)["cutoff"] != "WEBDL-1080p" {
		t.Fatalf("profiles: %v", profiles)
	}

	// Import two formats into the 1080p profile with their default scores.
	pid := profileIDByName(t, getJSON[map[string]any](t, client, httpSrv.URL+"/api/quality-profiles"), "1080p")
	res := postJSON[map[string]any](t, client, base+"/radarr/formats", map[string]any{
		"trashIds": []string{"493b6d1dbec3c3364c59d7607f7e3405", "eecf3a857724171f968a66cb5719e152"}, "profileId": pid,
	}, http.StatusOK)
	if res["added"].(float64) != 2 {
		t.Fatalf("import: %v", res)
	}
	p := profileByID(t, client, httpSrv.URL, pid)
	if fs := p["formatScores"].([]any); len(fs) != 2 {
		t.Fatalf("scores: %v", fs)
	}
	listing = getJSON[map[string]any](t, client, base+"/radarr")
	imported := 0
	for _, raw := range listing["formats"].([]any) {
		if raw.(map[string]any)["importedId"].(float64) > 0 {
			imported++
		}
	}
	if imported != 2 {
		t.Fatalf("2 formats should show as imported, got %d", imported)
	}

	// A whole profile: the name is taken, so it gets "(2)"; HDR is updated,
	// x265 added, the missing one reported.
	created := postJSON[map[string]any](t, client, base+"/radarr/profiles", map[string]any{"trashId": "0000made0up0000"}, http.StatusCreated)
	np := created["profile"].(map[string]any)
	if np["name"] != "1080p (2)" || np["cutoff"] != "WEBDL-1080p" || np["cutoffFormatScore"].(float64) != 10000 {
		t.Fatalf("profile: %v", np)
	}
	if allowed := np["allowed"].([]any); len(allowed) != 2 {
		t.Fatalf("allowed: %v", allowed)
	}
	fr := created["formats"].(map[string]any)
	if fr["added"].(float64) != 1 || fr["updated"].(float64) != 1 || len(fr["missing"].([]any)) != 1 {
		t.Fatalf("formats: %v", fr)
	}
	if n := len(np["formatScores"].([]any)); n != 2 {
		t.Fatalf("scores: %v", np["formatScores"])
	}
	postJSON[map[string]any](t, client, base+"/radarr/profiles", map[string]any{"trashId": "nope"}, http.StatusNotFound)
}
