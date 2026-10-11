package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// get sends a GET with the given headers and no cookie.
func metricsGet(t *testing.T, url string, headers map[string]string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
}

func TestMetricsAreProtectedAndAcceptBearerKeys(t *testing.T) {
	_, base, admin := loginNewServer(t)
	key := postJSON[map[string]any](t, admin, base+"/api/auth/api-keys", map[string]any{"name": "prometheus"}, http.StatusCreated)["key"].(string)

	for _, path := range []string{"/api/metrics", "/metrics"} {
		// Nobody signed in: refused, with no numbers in the answer.
		if code, _, body := metricsGet(t, base+path, nil); code != http.StatusUnauthorized || strings.Contains(body, "mediarium_") {
			t.Fatalf("%s without a key: %d %q", path, code, body)
		}
		// A wrong key, either way of sending it.
		if code, _, _ := metricsGet(t, base+path, map[string]string{"Authorization": "Bearer nope"}); code != http.StatusUnauthorized {
			t.Fatalf("%s with a wrong bearer key: %d", path, code)
		}
		if code, _, _ := metricsGet(t, base+path, map[string]string{"X-API-Key": "nope"}); code != http.StatusUnauthorized {
			t.Fatalf("%s with a wrong X-API-Key: %d", path, code)
		}
		// Basic auth or other schemes are not taken for a key.
		if code, _, _ := metricsGet(t, base+path, map[string]string{"Authorization": "Basic " + key}); code != http.StatusUnauthorized {
			t.Fatalf("%s with a Basic header: %d", path, code)
		}
		// The right key, both ways.
		for name, h := range map[string]map[string]string{
			"bearer":    {"Authorization": "Bearer " + key},
			"x-api-key": {"X-API-Key": key},
		} {
			code, ctype, body := metricsGet(t, base+path, h)
			if code != http.StatusOK || !strings.HasPrefix(ctype, "text/plain; version=0.0.4") {
				t.Fatalf("%s with %s: %d %q", path, name, code, ctype)
			}
			if !strings.Contains(body, "mediarium_up 1\n") {
				t.Fatalf("%s with %s: no mediarium_up in %q", path, name, body)
			}
		}
	}

	// A bearer key does not sign in anywhere else.
	if code, _, _ := metricsGet(t, base+"/api/dashboard", map[string]string{"Authorization": "Bearer " + key}); code != http.StatusUnauthorized {
		t.Fatalf("a bearer key must only work for the metrics: got %d", code)
	}
	// Prometheus scrapes with GET; /metrics is not a way to run anything else.
	req, _ := http.NewRequest(http.MethodPost, base+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), "mediarium_up") {
		t.Fatalf("POST /metrics should not give the numbers")
	}
}

func TestMetricsMembersAreRefused(t *testing.T) {
	_, base, _, member, _ := familyServer(t)
	key := postJSON[map[string]any](t, member, base+"/api/auth/api-keys", map[string]any{"name": "mine"}, http.StatusCreated)["key"].(string)
	if code, _, _ := metricsGet(t, base+"/metrics", map[string]string{"Authorization": "Bearer " + key}); code != http.StatusForbidden {
		t.Fatalf("a member's key must not read the metrics, got %d", code)
	}
}

func TestMetricsContent(t *testing.T) {
	_, base, admin := loginNewServer(t)
	key := postJSON[map[string]any](t, admin, base+"/api/auth/api-keys", map[string]any{"name": "p"}, http.StatusCreated)["key"].(string)
	_, _, body := metricsGet(t, base+"/metrics", map[string]string{"Authorization": "Bearer " + key})
	for _, want := range []string{
		"# TYPE mediarium_up gauge",
		`mediarium_build_info{version="`,
		`mediarium_library_movies_total{status="missing"} 0`,
		`mediarium_queue_items_total{status="downloading"} 0`,
		"mediarium_downloads_active 0",
		"mediarium_library_series 0",
		`mediarium_library_episodes{status="missing"} 0`,
		`mediarium_wanted_missing{kind="movie"} 0`,
		`mediarium_wanted_missing{kind="episode"} 0`,
		`mediarium_health_warnings{level="error"}`,
		`mediarium_library_books{format="ebook",status="missing"} 0`,
		`mediarium_indexers{state="enabled"} 0`,
		`mediarium_service_limit_hits{service="tmdb"} 0`,
		"mediarium_start_time_seconds ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics are missing %q", want)
		}
	}
	// Every sample belongs to a declared metric, and a TYPE line comes first.
	declared := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.HasPrefix(line, "# TYPE ") {
			declared[strings.Fields(line)[2]] = true
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		name := line
		if i := strings.IndexAny(line, "{ "); i > 0 {
			name = line[:i]
		}
		if !declared[name] {
			t.Errorf("sample %q has no TYPE line before it", line)
		}
		if !strings.HasPrefix(name, "mediarium_") {
			t.Errorf("metric %q should start with mediarium_", name)
		}
	}
}
