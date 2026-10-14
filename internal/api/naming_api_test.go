package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestNamingFormatsFromRadarrAndSonarr(t *testing.T) {
	_, base, client := loginNewServer(t)

	// A format copied from Radarr previews, saves and comes back.
	radarr := "{Movie CleanTitle} ({Release Year}) - {Custom Formats}{ - Edition Tags}"
	p := getJSON[map[string]any](t, client, base+"/api/settings/naming-preview?format="+url.QueryEscape(radarr))
	if p["filename"] != "Example Movie (2024) - Extended.mkv" || p["problem"] != nil {
		t.Fatalf("movie preview = %v", p)
	}
	sonarr := "{Series TitleYear} - S{season:00}E{episode:00} - {Episode CleanTitle} [{Quality Full}]"
	p = getJSON[map[string]any](t, client, base+"/api/settings/naming-preview?kind=tv&format="+url.QueryEscape(sonarr))
	if p["filename"] != "Example Show (2023) - S01E02 - The Second One [WEBDL-1080p].mkv" || p["folder"] != "Example Show (2023)/Season 01" {
		t.Fatalf("episode preview = %v", p)
	}
	saved := putJSONStatus(t, client, base+"/api/settings", map[string]any{"namingPreset": "custom", "movieNameFormat": radarr, "episodeNameFormat": sonarr}, http.StatusOK)
	if saved["movieNameFormat"] != radarr || saved["episodeNameFormat"] != sonarr {
		t.Fatalf("saved = %v", saved)
	}

	// An episode format that would give two episodes the same name is refused.
	bad := putJSONStatus(t, client, base+"/api/settings", map[string]any{"namingPreset": "custom", "episodeNameFormat": "{Series Title} - {Episode Title}"}, http.StatusBadRequest)
	if msg, _ := bad["error"].(string); !strings.Contains(msg, "{Season} and {Episode}") {
		t.Fatalf("bad episode format = %v", bad)
	}
	p = getJSON[map[string]any](t, client, base+"/api/settings/naming-preview?format="+url.QueryEscape("{Movie Title} {Made Up}"))
	if msg, _ := p["problem"].(string); !strings.Contains(msg, "{Made Up}") {
		t.Fatalf("problem = %v", p)
	}

	// A draft saved while another style was picked is checked once Custom is
	// picked, even when the draft itself isn't sent again.
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"namingPreset": "plex", "movieNameFormat": "{Release Year} {Quality}"}, http.StatusOK)
	bad = putJSONStatus(t, client, base+"/api/settings", map[string]any{"namingPreset": "custom"}, http.StatusBadRequest)
	if msg, _ := bad["error"].(string); !strings.Contains(msg, "movie's title") {
		t.Fatalf("switching to a bad movie draft = %v", bad)
	}
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"namingPreset": "plex", "movieNameFormat": radarr, "episodeNameFormat": "{Series Title} {Air-Date}"}, http.StatusOK)
	bad = putJSONStatus(t, client, base+"/api/settings", map[string]any{"namingPreset": "custom"}, http.StatusBadRequest)
	if msg, _ := bad["error"].(string); !strings.Contains(msg, "{Season} and {Episode}") {
		t.Fatalf("switching to a bad episode draft = %v", bad)
	}
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"namingPreset": "custom", "episodeNameFormat": sonarr}, http.StatusOK)

	list := getJSON[map[string]any](t, client, base+"/api/settings/naming-tokens")
	tokens, _ := list["tokens"].([]any)
	schemes, _ := list["schemes"].([]any)
	if len(tokens) < 30 || len(schemes) < 4 {
		t.Fatalf("token list: %d tokens, %d schemes", len(tokens), len(schemes))
	}
}
