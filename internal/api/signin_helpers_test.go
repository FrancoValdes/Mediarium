package api_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/api"
	"github.com/rdborg/mediarium/internal/config"
	"github.com/rdborg/mediarium/internal/crypto"
	"github.com/rdborg/mediarium/internal/store"
)

// signInEnv is a running server for the sign-in tests, with an administrator
// ("ryan") already signed in on admin.
type signInEnv struct {
	t      *testing.T
	server *api.Server
	db     *sql.DB
	base   string
	admin  *http.Client
}

const adminPassword = "correct-horse-battery-staple"

func newSignInEnv(t *testing.T, mutate func(*config.Config)) *signInEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		ConfigDir:           filepath.Join(dir, "config"),
		DownloadsDir:        filepath.Join(dir, "downloads"),
		DownloadsIncomplete: filepath.Join(dir, "downloads", "incomplete"),
		DownloadsComplete:   filepath.Join(dir, "downloads", "complete"),
		MoviesDir:           filepath.Join(dir, "movies"),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(cfg.ConfigDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := crypto.LoadOrCreateKey(filepath.Join(cfg.ConfigDir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := api.New(db, cfg, box, "", "1.2.3-test")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.Routes())
	t.Cleanup(srv.Close)
	e := &signInEnv{t: t, server: server, db: db, base: srv.URL}
	e.admin = e.client()
	e.must(e.admin, "POST", "/api/onboarding/admin", map[string]string{"username": "ryan", "password": adminPassword, "name": "Ryan"}, http.StatusCreated)
	return e
}

// restart builds another server on the same database, as if the app had been
// restarted with a different environment.
func (e *signInEnv) withConfig(mutate func(*config.Config)) *signInEnv {
	e.t.Helper()
	var dir string
	_ = e.db.QueryRow(`PRAGMA database_list`).Scan(new(int), new(string), &dir)
	cfg := config.Config{ConfigDir: filepath.Dir(dir)}
	mutate(&cfg)
	box, err := crypto.LoadOrCreateKey(filepath.Join(cfg.ConfigDir, "secret.key"))
	if err != nil {
		e.t.Fatal(err)
	}
	server, err := api.New(e.db, cfg, box, "", "1.2.3-test")
	if err != nil {
		e.t.Fatal(err)
	}
	srv := httptest.NewServer(server.Routes())
	e.t.Cleanup(srv.Close)
	return &signInEnv{t: e.t, server: server, db: e.db, base: srv.URL, admin: e.admin}
}

// client is a browser: it keeps cookies and does not follow redirects, so a
// test can look at each step.
func (e *signInEnv) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// call sends a JSON request and returns the status and the decoded answer.
func (e *signInEnv) call(c *http.Client, method, path string, body any, headers ...string) (int, map[string]any) {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.base+path, rdr)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	out["_raw"] = string(raw)
	return resp.StatusCode, out
}

// must is call that fails the test unless the status is the wanted one.
func (e *signInEnv) must(c *http.Client, method, path string, body any, want int, headers ...string) map[string]any {
	e.t.Helper()
	got, out := e.call(c, method, path, body, headers...)
	if got != want {
		e.t.Fatalf("%s %s: status %d, want %d: %v", method, path, got, want, out["_raw"])
	}
	return out
}

// signedInAs reports the account a client is signed in as ("" when none).
func (e *signInEnv) signedInAs(c *http.Client) (username, role string) {
	status, out := e.call(c, "GET", "/api/auth/me", nil)
	if status != http.StatusOK {
		return "", ""
	}
	u, _ := out["username"].(string)
	r, _ := out["role"].(string)
	return u, r
}

// addAccount makes an account through the administrator and returns its id.
func (e *signInEnv) addAccount(username, role string) int64 {
	e.t.Helper()
	out := e.must(e.admin, "POST", "/api/users", map[string]any{"username": username, "password": "long-enough-password", "role": role, "email": username + "@example.com"}, http.StatusCreated)
	return int64(out["id"].(float64))
}

// login signs a fresh client in with a password.
func (e *signInEnv) login(username, password string) (*http.Client, int, map[string]any) {
	c := e.client()
	status, out := e.call(c, "POST", "/api/auth/login", map[string]string{"username": username, "password": password})
	return c, status, out
}

func (e *signInEnv) setPublicURL(u string) {
	e.t.Helper()
	e.must(e.admin, "PUT", "/api/settings", map[string]any{"publicUrl": u}, http.StatusOK)
}

func (e *signInEnv) scalar(query string, args ...any) string {
	e.t.Helper()
	var v sql.NullString
	if err := e.db.QueryRow(query, args...).Scan(&v); err != nil {
		e.t.Fatal(err)
	}
	return v.String
}
