package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecret(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFileVariablesFollowTheDockerSecretsConvention(t *testing.T) {
	tests := []struct {
		name    string
		content string
		plain   string // value of the plain variable, "" for not set
		useFile bool
		want    string
	}{
		{"file contents are used", "/data/from-file", "", true, "/data/from-file"},
		{"surrounding whitespace and the final newline are trimmed", "  /data/from-file \r\n\n", "", true, "/data/from-file"},
		{"an empty file counts as not set", "\n", "", true, "/downloads"},
		{"the plain variable wins when both are given", "/data/from-file", "/data/plain", true, "/data/plain"},
		{"no file variable keeps the plain value", "", "/data/plain", false, "/data/plain"},
		{"neither gives the default", "", "", false, "/downloads"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DOWNLOADS_DIR", tc.plain)
			t.Setenv("DOWNLOADS_DIR_FILE", "")
			if tc.useFile {
				t.Setenv("DOWNLOADS_DIR_FILE", writeSecret(t, tc.content))
			}
			if got := Load().DownloadsDir; got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if p := Problems(); len(p) != 0 {
				t.Fatalf("unexpected problems: %v", p)
			}
		})
	}
}

func TestFileVariablesWorkForNumbersAndFlags(t *testing.T) {
	t.Setenv("APP_PORT", "")
	t.Setenv("APP_PORT_FILE", writeSecret(t, "9123\n"))
	t.Setenv("MEDIARIUM_PAUSE_AUTOMATION", "")
	t.Setenv("MEDIARIUM_PAUSE_AUTOMATION_FILE", writeSecret(t, "yes"))
	cfg := Load()
	if cfg.Port != 9123 || !cfg.PauseAutomation {
		t.Fatalf("got port %d, pause %v", cfg.Port, cfg.PauseAutomation)
	}
}

func TestUnreadableFileIsReportedWithoutItsContents(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	t.Setenv("DOWNLOADS_DIR", "")
	t.Setenv("DOWNLOADS_DIR_FILE", missing)
	cfg := Load()
	if cfg.DownloadsDir != "/downloads" {
		t.Fatalf("an unreadable file should leave the default, got %q", cfg.DownloadsDir)
	}
	problems := Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "DOWNLOADS_DIR_FILE") || !strings.Contains(problems[0], "cannot be read") {
		t.Fatalf("expected one clear problem, got %v", problems)
	}
	// A new Load starts clean.
	t.Setenv("DOWNLOADS_DIR_FILE", "")
	Load()
	if p := Problems(); len(p) != 0 {
		t.Fatalf("problems should be cleared by the next Load: %v", p)
	}
}

func TestURLBaseFromTheEnvironmentOrAFile(t *testing.T) {
	t.Setenv("URL_BASE", "")
	t.Setenv("URL_BASE_FILE", "")
	if cfg := Load(); cfg.URLBase != "" || cfg.Validate() != nil {
		t.Fatalf("default should be the root, got %q (%v)", cfg.URLBase, cfg.Validate())
	}
	t.Setenv("URL_BASE", "mediarium/")
	if cfg := Load(); cfg.URLBase != "/mediarium" || cfg.Validate() != nil {
		t.Fatalf("got %q (%v)", cfg.URLBase, cfg.Validate())
	}
	t.Setenv("URL_BASE", "")
	t.Setenv("URL_BASE_FILE", writeSecret(t, "/apps/media\n"))
	if cfg := Load(); cfg.URLBase != "/apps/media" {
		t.Fatalf("got %q", cfg.URLBase)
	}
	t.Setenv("URL_BASE_FILE", "")
	t.Setenv("URL_BASE", "/api")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "URL_BASE") {
		t.Fatalf("expected a URL_BASE error, got %v", err)
	}
}

func TestNormalizeURLBase(t *testing.T) {
	tests := []struct {
		in, want string
		bad      bool
	}{
		{"", "", false},
		{"/", "", false},
		{"  ", "", false},
		{"///", "", false},
		{"/mediarium", "/mediarium", false},
		{"mediarium", "/mediarium", false},
		{"/mediarium/", "/mediarium", false},
		{" /Media_Rium-2.0 ", "/Media_Rium-2.0", false},
		{"/apps/mediarium", "/apps/mediarium", false},
		{"/a//b", "", true},
		{"/a/../b", "", true},
		{"/a/./b", "", true},
		{"/my media", "", true},
		{"/media?x=1", "", true},
		{"/media#top", "", true},
		{"/café", "", true},
		{"https://example.com/media", "", true},
		{"/api", "", true},
		{"/API/v2", "", true},
		{"/settings", "", true},
		{"/radarr", "", true},
		{"/" + strings.Repeat("a", 120), "", true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := NormalizeURLBase(tc.in)
			if tc.bad {
				if err == nil {
					t.Fatalf("%q should be refused, got %q", tc.in, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("NormalizeURLBase(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}
