package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/configfile"
)

func envFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestConfigFileExportApplyAndDiff(t *testing.T) {
	e := newClientEnv(t)
	ctx := context.Background()
	c := &configfile.Client{BaseURL: e.url, HTTP: e.client}

	// Export: every block, nothing derived, no secrets.
	out, err := configfile.Export(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"settings:", "downloadClient:", "debrid:", "proxy:", "systemOptions:", "huntIntervalHours: 6"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the export should have %q:\n%s", want, text)
		}
	}
	for _, not := range []string{"hasTmdbApiKey", "hasPassword", "tmdbKeyBuiltIn"} {
		if strings.Contains(text, not) {
			t.Fatalf("a worked-out value %q is not exported", not)
		}
	}
	// What was exported is already what Mediarium has.
	back, err := configfile.Parse(out, envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := configfile.Plan(ctx, c, back)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range plan {
		if len(r.Changes) != 0 || r.Err != nil {
			t.Fatalf("an export applied straight back should change nothing: %+v", r)
		}
	}

	// A file with a few lines: diff shows them, apply makes them, a second
	// apply has nothing left to do.
	file, err := configfile.Parse([]byte(`
settings:
  huntIntervalHours: 12
  trashDays: 14
downloadClient:
  kind: qbittorrent
  url: ${QB_URL}
  username: admin
  password: ${QB_PASSWORD}
`), envFrom(map[string]string{"QB_URL": e.qbURL, "QB_PASSWORD": "adminpass"}))
	if err != nil {
		t.Fatal(err)
	}
	plan, err = configfile.Plan(ctx, c, file)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, r := range plan {
		for _, ch := range r.Changes {
			lines = append(lines, ch.String())
		}
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"settings.huntIntervalHours: 6 -> 12", "settings.trashDays: 7 -> 14", `downloadClient.kind: "" -> "qbittorrent"`, "downloadClient.password: (secret) will be set"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the diff should say %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "adminpass") {
		t.Fatal("a secret is never printed")
	}
	if got := getJSON[map[string]any](t, e.client, e.url+"/api/settings")["huntIntervalHours"]; got != float64(6) {
		t.Fatalf("a diff changes nothing, hunt interval is %v", got)
	}

	res, err := configfile.Apply(ctx, c, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Err != nil {
			t.Fatalf("apply: %v", r.Err)
		}
	}
	settings := getJSON[map[string]any](t, e.client, e.url+"/api/settings")
	if settings["huntIntervalHours"] != float64(12) || settings["trashDays"] != float64(14) {
		t.Fatalf("applied settings: hunt %v trash %v", settings["huntIntervalHours"], settings["trashDays"])
	}
	dc := getJSON[map[string]any](t, e.client, e.url+"/api/download-client")
	if dc["kind"] != "qbittorrent" || dc["hasPassword"] != true {
		t.Fatalf("applied download client: %v", dc)
	}

	again, err := configfile.Apply(ctx, c, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again {
		// The password can't be compared (Mediarium never shows it), so it is
		// sent again; everything else is already as the file says.
		for _, ch := range r.Changes {
			if !ch.Secret {
				t.Fatalf("a second apply has nothing left to change but a secret: %v", ch)
			}
		}
	}
}

func TestConfigFileRefusesWhatItDoesNotKnow(t *testing.T) {
	e := newClientEnv(t)
	ctx := context.Background()
	c := &configfile.Client{BaseURL: e.url, HTTP: e.client}

	if _, err := configfile.Parse([]byte("nonsense:\n  a: 1\n"), envFrom(nil)); err == nil || !strings.Contains(err.Error(), "isn't a block Mediarium knows") {
		t.Fatalf("an unknown block: %v", err)
	}
	if _, err := configfile.Parse([]byte("settings:\n  tmdbApiKey: ${NOT_SET}\n"), envFrom(nil)); err == nil || !strings.Contains(err.Error(), "NOT_SET isn't set") {
		t.Fatalf("an unset variable: %v", err)
	}
	if f, err := configfile.Parse([]byte("settings:\n  publicUrl: ${PUB_URL:-https://mediarium.example}\n"), envFrom(nil)); err != nil || f.Blocks["settings"]["publicUrl"] != "https://mediarium.example" {
		t.Fatalf("a default: %v %v", f, err)
	}
	if _, err := configfile.Parse([]byte("settings: [1, 2]\n"), envFrom(nil)); err == nil {
		t.Fatal("a block must be name: value lines")
	}

	// A typo is refused, and nothing in the file is applied.
	typo, err := configfile.Parse([]byte("settings:\n  huntIntervalHour: 12\n  trashDays: 30\n"), envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configfile.Apply(ctx, c, typo); err == nil || !strings.Contains(err.Error(), "settings.huntIntervalHour") {
		t.Fatalf("a typo should be named: %v", err)
	}
	if got := getJSON[map[string]any](t, e.client, e.url+"/api/settings")["trashDays"]; got != float64(7) {
		t.Fatalf("nothing is applied when the file has a typo, trashDays is %v", got)
	}

	// A value Mediarium refuses is reported for its block, and the other
	// blocks still go through.
	mixed, err := configfile.Parse([]byte("settings:\n  trashDays: -5\ndebrid:\n  preferCached: false\n"), envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	res, err := configfile.Apply(ctx, c, mixed)
	if err != nil {
		t.Fatal(err)
	}
	var sawBad, sawGood bool
	for _, r := range res {
		switch r.Section {
		case "settings":
			sawBad = r.Err != nil && !r.Applied
		case "debrid":
			sawGood = r.Err == nil && r.Applied
		}
	}
	if !sawBad || !sawGood {
		t.Fatalf("a refused block is reported and the others applied: %+v", res)
	}
	if got := getJSON[map[string]any](t, e.client, e.url+"/api/debrid")["preferCached"]; got != false {
		t.Fatalf("the debrid block was applied, preferCached is %v", got)
	}
}

func TestConfigFileLists(t *testing.T) {
	e := newClientEnv(t)
	ctx := context.Background()
	c := &configfile.Client{BaseURL: e.url, HTTP: e.client}

	file, err := configfile.Parse([]byte(`
usenetServers:
  - name: Main server
    host: news.example.com
    port: 563
    useSsl: true
    username: me
    password: ${NEWS_PASSWORD}
    connections: 8
indexers:
  - name: My Newznab
    kind: newznab
    baseUrl: https://indexer.example.com
    apiKey: ${NZB_KEY}
    protocol: usenet
    priority: 1
qualityProfiles:
  - name: Only 1080p
    allowed: [WEBDL-1080p, Bluray-1080p]
    cutoff: WEBDL-1080p
    upgradeAllowed: true
`), envFrom(map[string]string{"NEWS_PASSWORD": "pw-1", "NZB_KEY": "key-1"}))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := configfile.Plan(ctx, c, file)
	if err != nil {
		t.Fatal(err)
	}
	var added int
	for _, r := range plan {
		for _, ch := range r.Changes {
			if ch.New {
				added++
			}
		}
	}
	if added != 3 {
		t.Fatalf("three new items should be planned, got %d: %+v", added, plan)
	}
	if list := getJSON[[]map[string]any](t, e.client, e.url+"/api/usenet-servers"); len(list) != 0 {
		t.Fatalf("a plan changes nothing: %v", list)
	}

	res, err := configfile.Apply(ctx, c, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Err != nil {
			t.Fatalf("apply %s: %v", r.Section, r.Err)
		}
	}
	servers := getJSON[[]map[string]any](t, e.client, e.url+"/api/usenet-servers")
	if len(servers) != 1 || servers[0]["host"] != "news.example.com" || servers[0]["connections"] != float64(8) || servers[0]["hasPassword"] != true {
		t.Fatalf("the usenet server: %v", servers)
	}
	if servers[0]["enabled"] != true {
		t.Fatalf("a server added from the file is switched on when the file doesn't say: %v", servers[0])
	}
	idx := getJSON[[]map[string]any](t, e.client, e.url+"/api/indexers")
	if len(idx) != 1 || idx[0]["name"] != "My Newznab" || idx[0]["hasApiKey"] != true {
		t.Fatalf("the indexer: %v", idx)
	}

	// Applying again changes only what can't be compared (the passwords).
	again, err := configfile.Apply(ctx, c, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again {
		if r.Err != nil {
			t.Fatalf("second apply %s: %v", r.Section, r.Err)
		}
		for _, ch := range r.Changes {
			if !ch.Secret {
				t.Fatalf("a second apply has nothing left to change but a secret: %v", ch)
			}
		}
	}

	// A changed value is found by name and changed; an item not in the file stays.
	edit, err := configfile.Parse([]byte("usenetServers:\n  - name: main SERVER\n    connections: 12\n"), envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configfile.Apply(ctx, c, edit); err != nil {
		t.Fatal(err)
	}
	servers = getJSON[[]map[string]any](t, e.client, e.url+"/api/usenet-servers")
	if len(servers) != 1 || servers[0]["connections"] != float64(12) || servers[0]["hasPassword"] != true {
		t.Fatalf("the server is edited by its name and keeps its password: %v", servers)
	}

	// An export of all this applied straight back changes nothing.
	out, err := configfile.Export(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "usenetServers:") || !strings.Contains(string(out), "qualityProfiles:") || strings.Contains(string(out), "pw-1") {
		t.Fatalf("the export has the lists and no secrets:\n%s", out)
	}
	back, err := configfile.Parse(out, envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	plan, err = configfile.Plan(ctx, c, back)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range plan {
		if len(r.Changes) != 0 || r.Err != nil {
			t.Fatalf("an export applied straight back should change nothing: %+v", r)
		}
	}

	// A typo inside an item refuses the whole file.
	bad, _ := configfile.Parse([]byte("usenetServers:\n  - name: Main server\n    conections: 3\n"), envFrom(nil))
	if _, err := configfile.Plan(ctx, c, bad); err == nil || !strings.Contains(err.Error(), "usenetServers[Main server].conections") {
		t.Fatalf("a typo in an item: %v", err)
	}
	if _, err := configfile.Parse([]byte("indexers:\n  - kind: newznab\n"), envFrom(nil)); err == nil || !strings.Contains(err.Error(), "needs a name") {
		t.Fatalf("an item with no name: %v", err)
	}
}

// Profiles name their fallback profiles and custom formats: the numbers
// behind them mean something else on another install.
func TestConfigFileProfilesUseNames(t *testing.T) {
	e := newClientEnv(t)
	ctx := context.Background()
	c := &configfile.Client{BaseURL: e.url, HTTP: e.client}
	postJSON[map[string]any](t, e.client, e.url+"/api/custom-formats", map[string]any{"json": fixture(t, "radarr-hdr.json"), "app": "radarr"}, 200)
	formats := getJSON[map[string]any](t, e.client, e.url+"/api/custom-formats")["formats"].([]any)
	hdr := formatIDByName(t, formats, "HDR")

	file, err := configfile.Parse([]byte(`
qualityProfiles:
  - name: Base
    allowed: [WEBDL-1080p]
    cutoff: WEBDL-1080p
  - name: Picky
    allowed: [Bluray-1080p]
    cutoff: Bluray-1080p
    fallback: [base]
    formatScores:
      - format: hdr
        score: 25
`), envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	res, err := configfile.Apply(ctx, c, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Err != nil {
			t.Fatalf("apply %s: %v", r.Section, r.Err)
		}
	}
	var baseID float64
	var picky map[string]any
	for _, raw := range getJSON[map[string]any](t, e.client, e.url+"/api/quality-profiles")["profiles"].([]any) {
		p := raw.(map[string]any)
		switch p["name"] {
		case "Base":
			baseID = p["id"].(float64)
		case "Picky":
			picky = p
		}
	}
	if picky == nil || baseID == 0 {
		t.Fatal("both profiles should be added")
	}
	if fb := picky["fallback"].([]any); len(fb) != 1 || fb[0] != baseID {
		t.Fatalf("the fallback should be Base (%v): %v", baseID, fb)
	}
	if fs := picky["formatScores"].([]any); len(fs) != 1 || fs[0].(map[string]any)["formatId"] != hdr || fs[0].(map[string]any)["score"] != float64(25) {
		t.Fatalf("the HDR score: %v", fs)
	}

	// The export names them too, and applied straight back changes nothing.
	out, err := configfile.Export(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if strings.Contains(text, "formatId") || !strings.Contains(text, "format: HDR") || !strings.Contains(text, "- Base") {
		t.Fatalf("the export should use names:\n%s", text)
	}
	back, err := configfile.Parse(out, envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := configfile.Plan(ctx, c, back)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range plan {
		if len(r.Changes) != 0 || r.Err != nil {
			t.Fatalf("an export applied straight back should change nothing: %+v", r)
		}
	}

	// A name Mediarium doesn't have is an error naming the item; nothing changes.
	for _, tc := range []struct{ yaml, want string }{
		{"qualityProfiles:\n  - name: Picky\n    fallback: [Nope]\n", `qualityProfiles[Picky]: the fallback profile "Nope"`},
		{"qualityProfiles:\n  - name: Picky\n    formatScores:\n      - format: Nope\n        score: 5\n", `qualityProfiles[Picky]: the custom format "Nope"`},
		{"qualityProfiles:\n  - name: Picky\n    fallback: [1]\n", "qualityProfiles[Picky]: fallback must list profiles by name"},
		{"qualityProfiles:\n  - name: Picky\n    formatScores:\n      - formatId: 1\n        score: 5\n", `qualityProfiles[Picky]: formatScores has "formatId"`},
	} {
		f, err := configfile.Parse([]byte(tc.yaml), envFrom(nil))
		if err != nil {
			t.Fatal(err)
		}
		res, err := configfile.Apply(ctx, c, f)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), tc.want) || res[0].Applied {
			t.Fatalf("%q: want an error with %q, got %+v", tc.yaml, tc.want, res)
		}
	}
}

func TestSpeedScheduleSavesThroughSettings(t *testing.T) {
	e := newClientEnv(t)
	body := map[string]any{"speedSchedule": []map[string]any{{"days": []int{1, 2}, "from": 8, "to": 18, "limitMB": 3}}}
	putJSON(t, e.client, e.url+"/api/settings", body, 200)
	got := getJSON[map[string]any](t, e.client, e.url+"/api/settings")["speedSchedule"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["limitMB"] != float64(3) {
		t.Fatalf("the schedule is saved and shown back: %v", got)
	}
	bad := map[string]any{"speedSchedule": []map[string]any{{"days": []int{9}, "from": 8, "to": 18, "limitMB": 3}}}
	putJSON(t, e.client, e.url+"/api/settings", bad, 400)
	putJSON(t, e.client, e.url+"/api/settings", map[string]any{"speedSchedule": []map[string]any{}}, 200)
	if got := getJSON[map[string]any](t, e.client, e.url+"/api/settings")["speedSchedule"].([]any); len(got) != 0 {
		t.Fatalf("an empty list clears it: %v", got)
	}
}
