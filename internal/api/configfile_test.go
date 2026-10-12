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
	if f, err := configfile.Parse([]byte("settings:\n  publicUrl: ${PUB_URL:-https://mediarium.example}\n"), envFrom(nil)); err != nil || f["settings"]["publicUrl"] != "https://mediarium.example" {
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
