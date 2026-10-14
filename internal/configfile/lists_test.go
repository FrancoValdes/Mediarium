package configfile

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeMediarium answers GET requests with fixed JSON, enough for a plan.
func fakeMediarium(t *testing.T, answers map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := answers[r.URL.Path]
		if r.Method != http.MethodGet || !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL}
}

const fakeIndexers = `[
 {"id":1,"name":"Site","kind":"cardigann","definitionId":"site","baseUrl":"https://site.example/","protocol":"torrent","enabled":true,"settings":{"username":"me"},"priority":1},
 {"id":2,"name":"NZB","kind":"newznab","baseUrl":"https://nzb.example","protocol":"usenet","enabled":true,"hasApiKey":true,"priority":1}
]`

const fakeDefinitions = `{"definitions":[{"id":"site","settings":[{"name":"username"},{"name":"password"},{"name":"freeleech"}]}]}`

func planText(t *testing.T, c *Client, yaml string) (string, error) {
	t.Helper()
	f, err := Parse([]byte(yaml), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	res, err := Plan(context.Background(), c, f)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, r := range res {
		for _, ch := range r.Changes {
			lines = append(lines, ch.String())
		}
		if r.Err != nil {
			lines = append(lines, "error: "+r.Err.Error())
		}
	}
	return strings.Join(lines, "\n"), nil
}

func TestIndexerSettingsAreChecked(t *testing.T) {
	c := fakeMediarium(t, map[string]string{"/api/indexers": fakeIndexers, "/api/indexer-definitions": fakeDefinitions})
	cases := []struct {
		name, yaml, err string
	}{
		{"a typo in a site setting", "indexers:\n  - name: Site\n    settings:\n      usernmae: me\n", "indexers[Site].settings.usernmae"},
		{"settings on a Newznab indexer", "indexers:\n  - name: NZB\n    settings:\n      foo: 1\n", "indexers[NZB].settings"},
		{"a new site indexer with a typo", "indexers:\n  - name: New\n    kind: cardigann\n    definitionId: site\n    settings:\n      freelech: true\n", "indexers[New].settings.freelech"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := planText(t, c, tc.yaml)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("want an error naming %q, got %v", tc.err, err)
			}
		})
	}
	// Known names pass; a setting not shown back (the password) is a secret.
	got, err := planText(t, c, "indexers:\n  - name: Site\n    settings:\n      username: me\n      password: pw\n")
	if err != nil || got != "indexers[Site].settings.password: (secret) will be set" {
		t.Fatalf("known settings: %q %v", got, err)
	}

	// The site list can't be read: the names are not checked.
	plain := fakeMediarium(t, map[string]string{"/api/indexers": fakeIndexers})
	if _, err := planText(t, plain, "indexers:\n  - name: Site\n    settings:\n      anything: 1\n"); err != nil {
		t.Fatalf("without the site list the settings are passed on: %v", err)
	}
}

// Values Mediarium ignores when an item is changed are never shown as changes.
func TestFixedValuesAreNotChanges(t *testing.T) {
	c := fakeMediarium(t, map[string]string{
		"/api/indexers":            fakeIndexers,
		"/api/indexer-definitions": fakeDefinitions,
		"/api/media-servers":       `[{"id":1,"name":"Plex","kind":"plex","baseUrl":"http://192.168.1.10:32400","enabled":true,"hasToken":true}]`,
	})
	got, err := planText(t, c, `
indexers:
  - name: Site
    kind: torznab
    definitionId: another
    protocol: usenet
    apiKey: key
    settings:
      username: me
  - name: NZB
    kind: torznab
    protocol: usenet
mediaServers:
  - name: Plex
    kind: Plex
    baseUrl: http://192.168.1.10:32400
`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("nothing should change:\n%s", got)
	}
}

func TestNewItemsAreSwitchedOn(t *testing.T) {
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, "[]")
		case r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			posted = append(posted, string(b))
			_, _ = io.WriteString(w, `{"id":1}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer srv.Close()
	f, err := Parse([]byte("usenetServers:\n  - name: Main\n    host: news.example.com\n  - name: Spare\n    host: spare.example.com\n    enabled: false\n"), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), &Client{BaseURL: srv.URL}, f); err != nil {
		t.Fatal(err)
	}
	if len(posted) != 2 || !strings.Contains(posted[0], `"enabled":true`) || !strings.Contains(posted[1], `"enabled":false`) {
		t.Fatalf("a server is switched on unless the file says otherwise: %v", posted)
	}
}
