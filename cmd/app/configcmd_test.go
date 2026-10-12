package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeSettingsServer answers GET with a few values and records what is PUT.
func fakeSettingsServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var puts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "k" {
			http.Error(w, `{"error":"no"}`, http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet:
			vals := map[string]any{
				"/api/settings":        map[string]any{"huntIntervalHours": 6, "hasTmdbApiKey": false},
				"/api/download-client": map[string]any{"kind": "", "url": "", "hasPassword": false},
				"/api/debrid":          map[string]any{"service": "", "preferCached": true},
				"/api/proxy":           map[string]any{"type": ""},
				"/api/system/options":  map[string]any{"updateCheck": true},
			}
			_ = json.NewEncoder(w).Encode(vals[r.URL.Path])
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			puts = append(puts, r.URL.Path+" "+string(b))
			mu.Unlock()
			_, _ = w.Write([]byte("{}"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &puts
}

func TestConfigCommand(t *testing.T) {
	srv, puts := fakeSettingsServer(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "mediarium.yml")
	if err := os.WriteFile(file, []byte("settings:\n  huntIntervalHours: 12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := runConfigCommand(args, &out, &errb)
		return code, out.String(), errb.String()
	}

	if code, _, errs := run("apply", "--url", srv.URL, file); code != 2 || !strings.Contains(errs, "no API key") {
		t.Fatalf("no key: %d %q", code, errs)
	}
	if code, _, _ := run(); code != 2 {
		t.Fatalf("no command: %d", code)
	}

	code, out, errs := run("diff", "--url", srv.URL, "--key", "k", file)
	if code != 0 || !strings.Contains(out, "settings.huntIntervalHours: 6 -> 12") || !strings.Contains(out, "1 change would be made") {
		t.Fatalf("diff: %d %q %q", code, out, errs)
	}
	if len(*puts) != 0 {
		t.Fatalf("diff must not change anything: %v", *puts)
	}
	if code, _, _ := run("apply", "-n", "--url", srv.URL, "--key", "k", file); code != 0 || len(*puts) != 0 {
		t.Fatalf("a dry run must not change anything: %d %v", code, *puts)
	}

	code, out, errs = run("apply", "--url", srv.URL, "--key", "k", file)
	if code != 0 || !strings.Contains(out, "Applied 1 change") || len(*puts) != 1 || !strings.HasPrefix((*puts)[0], "/api/settings ") || !strings.Contains((*puts)[0], `"huntIntervalHours":12`) {
		t.Fatalf("apply: %d %q %q %v", code, out, errs, *puts)
	}

	code, out, _ = run("export", "--url", srv.URL, "--key", "k")
	if code != 0 || !strings.Contains(out, "huntIntervalHours: 6") || strings.Contains(out, "hasTmdbApiKey") {
		t.Fatalf("export: %d %q", code, out)
	}
	target := filepath.Join(dir, "out.yml")
	if code, _, _ := run("export", "-o", target, "--url", srv.URL, "--key", "k"); code != 0 {
		t.Fatalf("export to a file: %d", code)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("the exported file should be private: %v %v", info, err)
	}

	if code, _, errs := run("diff", "--url", srv.URL, "--key", "wrong", file); code != 1 || !strings.Contains(errs, "administrator") {
		t.Fatalf("a wrong key is explained: %d %q", code, errs)
	}
}
