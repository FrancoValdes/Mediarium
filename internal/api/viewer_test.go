package api_test

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var placeholder = regexp.MustCompile(`\{[^}]+\}`)

// split turns "PUT /api/movies/{id}/tags" into the method and a path that
// matches the pattern.
func split(pattern string) (method, path string) {
	method, path, _ = strings.Cut(pattern, " ")
	return method, placeholder.ReplaceAllString(path, "1")
}

// A viewer may only look. These are the few things a viewer may do with a
// method that changes something, all of them about their own account.
var viewerMayWrite = map[string]bool{
	"PUT /api/auth/profile": true, "POST /api/auth/change-password": true,
	"POST /api/auth/two-step/setup": true, "POST /api/auth/two-step/enable": true,
	"POST /api/auth/two-step/disable": true, "POST /api/auth/two-step/recovery": true,
	"POST /api/auth/passkeys/register/begin": true, "POST /api/auth/passkeys/register/finish": true,
	"PUT /api/auth/passkeys/{id}": true, "DELETE /api/auth/passkeys/{id}": true,
	"POST /api/auth/oidc/link": true, "DELETE /api/auth/oidc/link": true,
	"PUT /api/books/{id}/progress": true,
}

// Things a viewer may not even read: they belong to whoever manages the account.
var viewerMayNotRead = map[string]bool{
	"GET /api/auth/api-keys": true, "GET /api/calendar/feed": true,
	"GET /api/plex-watchlist": true, "GET /api/plex-watchlist/pin/{pinId}": true, "GET /api/plex-watchlist/items": true,
}

func isRead(method string) bool { return method == http.MethodGet || method == http.MethodHead }

// The central check: walk every route of the server. A viewer account and a
// read-only API key must not get through to anything that changes something
// or to anything meant for administrators, whatever the route's handler does.
func TestViewerAndReadOnlyKeyCannotReachAChangingOrAdminRoute(t *testing.T) {
	e := newSignInEnv(t, nil)
	e.addAccount("vera", "viewer")
	viewer, status, _ := e.login("vera", "long-enough-password")
	if status != http.StatusOK {
		t.Fatalf("viewer sign-in: %d", status)
	}
	roKey := e.must(e.admin, "POST", "/api/auth/api-keys", map[string]any{"name": "dashboard", "readOnly": true}, http.StatusCreated)["key"].(string)
	anon := e.client()

	routes := e.server.TestRoutes()
	if len(routes) < 200 {
		t.Fatalf("only %d routes found, the walk is not seeing the table", len(routes))
	}
	patterns := make([]string, 0, len(routes))
	for p := range routes {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)

	var checkedViewer, checkedKey int
	for _, pattern := range patterns {
		method, path := split(pattern)
		level := routes[pattern]

		// --- the viewer account ---
		mustBlock := level == "admin" || (!isRead(method) && !viewerMayWrite[pattern]) || viewerMayNotRead[pattern]
		if mustBlock {
			status, out := e.call(viewer, method, path, map[string]any{})
			if status != http.StatusForbidden {
				t.Errorf("a viewer got through to %s (%d): %v", pattern, status, out["_raw"])
			}
			checkedViewer++
		}

		// --- an administrator's read-only key: only GET and HEAD ---
		if !isRead(method) {
			status, out := e.call(anon, method, path, map[string]any{}, "X-API-Key", roKey)
			if status != http.StatusForbidden {
				t.Errorf("a read-only key got through to %s (%d): %v", pattern, status, out["_raw"])
			} else if !strings.Contains(out["_raw"].(string), "read-only") && !strings.Contains(out["_raw"].(string), "only be done") {
				// session-only routes answer with their own 403 first, which is just as closed
				t.Errorf("%s: 403 for a read-only key, but the reason is odd: %v", pattern, out["_raw"])
			}
			checkedKey++
		}
	}
	t.Logf("blocked %d viewer calls and %d read-only key calls across %d routes", checkedViewer, checkedKey, len(patterns))
	if checkedViewer < 150 || checkedKey < 150 {
		t.Errorf("the walk checked too little: %d, %d", checkedViewer, checkedKey)
	}
}

func TestViewerCanLookAround(t *testing.T) {
	e := newSignInEnv(t, nil)
	e.addAccount("vera", "viewer")
	viewer, status, out := e.login("vera", "long-enough-password")
	if status != http.StatusOK || out["role"] != "viewer" || out["isAdmin"] != false {
		t.Fatalf("sign-in: %d %v", status, out)
	}
	for _, path := range []string{"/api/auth/me", "/api/movies", "/api/series", "/api/queue", "/api/activity", "/api/dashboard", "/api/wanted", "/api/calendar", "/api/quality-profiles", "/api/modules"} {
		if got, o := e.call(viewer, "GET", path, nil); got != http.StatusOK {
			t.Errorf("a viewer can't GET %s: %d %v", path, got, o["_raw"])
		}
	}
	me := e.must(viewer, "GET", "/api/auth/me", nil, http.StatusOK)
	p := me["permissions"].(map[string]any)
	for k, v := range p {
		if v == true && k != "play" {
			t.Errorf("a viewer has the %q permission", k)
		}
	}
	if p["play"] != true {
		t.Error("a viewer can't play what is already there")
	}
	// Own account things work.
	e.must(viewer, "PUT", "/api/auth/profile", map[string]string{"username": "vera", "name": "Vera V", "email": "vera@example.com"}, http.StatusOK)
	e.must(viewer, "POST", "/api/auth/change-password", map[string]string{"currentPassword": "long-enough-password", "newPassword": "another-long-password"}, http.StatusOK)
	// But not the things that change the library, even on a route a member may use.
	for _, tc := range [][2]string{{"POST", "/api/movies"}, {"PUT", "/api/movies/1/monitored"}, {"POST", "/api/queue/1/retry"}, {"POST", "/api/search/grab"}, {"POST", "/api/exclusions"}} {
		if got, o := e.call(viewer, tc[0], tc[1], map[string]any{}); got != http.StatusForbidden {
			t.Errorf("%s %s: %d %v", tc[0], tc[1], got, o["_raw"])
		}
	}
	// Settings, keys and other accounts stay out of sight.
	for _, path := range []string{"/api/settings", "/api/users", "/api/indexers", "/api/auth/api-keys", "/api/auth/oidc", "/api/auth/ldap"} {
		if got, _ := e.call(viewer, "GET", path, nil); got != http.StatusForbidden {
			t.Errorf("a viewer can GET %s: %d", path, got)
		}
	}
}

func TestReadOnlyKeyReadsButNeverChanges(t *testing.T) {
	e := newSignInEnv(t, nil)
	rw := e.must(e.admin, "POST", "/api/auth/api-keys", map[string]any{"name": "script"}, http.StatusCreated)["key"].(string)
	ro := e.must(e.admin, "POST", "/api/auth/api-keys", map[string]any{"name": "dashboard", "readOnly": true}, http.StatusCreated)
	if ro["readOnly"] != true {
		t.Fatalf("the new key isn't marked read-only: %v", ro)
	}
	roKey := ro["key"].(string)
	anon := e.client()

	// The ordinary key can still change things; the read-only one can only look.
	if got, o := e.call(anon, "POST", "/api/exclusions", map[string]any{}, "X-API-Key", rw); got == http.StatusForbidden {
		t.Errorf("the ordinary key lost its rights: %v", o["_raw"])
	}
	if got, o := e.call(anon, "POST", "/api/exclusions", map[string]any{}, "X-API-Key", roKey); got != http.StatusForbidden || !strings.Contains(o["_raw"].(string), "read-only") {
		t.Errorf("read-only key on a POST: %d %v", got, o["_raw"])
	}
	for _, m := range []string{"PUT", "DELETE", "PATCH", "POST"} {
		// DELETE is a real route there; the others have no handler at all (405), which is just as closed.
		if got, _ := e.call(anon, m, "/api/movies/1", map[string]any{}, "X-API-Key", roKey); got != http.StatusForbidden && !(m != "DELETE" && got == http.StatusMethodNotAllowed) {
			t.Errorf("read-only key on %s: %d", m, got)
		}
	}
	if got, o := e.call(anon, "GET", "/api/movies", nil, "X-API-Key", roKey); got != http.StatusOK {
		t.Errorf("read-only key on GET: %d %v", got, o["_raw"])
	}
	// An administrator's read-only key still reads administrator things, and still can't write them.
	if got, _ := e.call(anon, "GET", "/api/users", nil, "X-API-Key", roKey); got != http.StatusOK {
		t.Errorf("read-only admin key on GET /api/users: %d", got)
	}
	if got, _ := e.call(anon, "PUT", "/api/settings", map[string]any{"huntIntervalHours": 3}, "X-API-Key", roKey); got != http.StatusForbidden {
		t.Errorf("read-only admin key changed settings: %d", got)
	}
	// It can't make itself a stronger key.
	if got, _ := e.call(anon, "POST", "/api/auth/api-keys", map[string]any{"name": "sneaky"}, "X-API-Key", roKey); got != http.StatusForbidden {
		t.Errorf("read-only key made a key: %d", got)
	}
	// The keys list says which is which.
	_, list := e.call(e.admin, "GET", "/api/auth/api-keys", nil)
	if !strings.Contains(list["_raw"].(string), `"readOnly":true`) {
		t.Errorf("the key list doesn't show the read-only key: %v", list["_raw"])
	}
}

// The Radarr/Sonarr-style endpoints take the same keys; a read-only one must
// not be able to add, change or delete there either.
func TestReadOnlyKeyOnTheArrEndpoints(t *testing.T) {
	e := newSignInEnv(t, nil)
	rw := e.must(e.admin, "POST", "/api/auth/api-keys", map[string]any{"name": "overseerr"}, http.StatusCreated)["key"].(string)
	ro := e.must(e.admin, "POST", "/api/auth/api-keys", map[string]any{"name": "homarr", "readOnly": true}, http.StatusCreated)["key"].(string)
	anon := e.client()
	if got, o := e.call(anon, "GET", "/radarr/api/v3/system/status", nil, "X-Api-Key", ro); got != http.StatusOK {
		t.Fatalf("read-only key on a Radarr GET: %d %v", got, o["_raw"])
	}
	cases := []struct{ method, path string }{
		{"POST", "/radarr/api/v3/movie"}, {"PUT", "/radarr/api/v3/movie/1"}, {"DELETE", "/radarr/api/v3/movie/1"}, {"POST", "/radarr/api/v3/command"},
		{"POST", "/sonarr/api/v3/series"}, {"DELETE", "/sonarr/api/v3/queue/1"}, {"POST", "/lidarr/api/v1/artist"}, {"POST", "/radarr/api/v3/tag"},
	}
	for _, c := range cases {
		if got, o := e.call(anon, c.method, c.path, map[string]any{}, "X-Api-Key", ro); got != http.StatusForbidden || !strings.Contains(o["_raw"].(string), "read-only") {
			t.Errorf("%s %s with a read-only key: %d %v", c.method, c.path, got, o["_raw"])
		}
	}
	if got, o := e.call(anon, "POST", "/radarr/api/v3/tag", map[string]any{"label": "x"}, "X-Api-Key", rw); got == http.StatusForbidden {
		t.Errorf("the ordinary key is refused on the Radarr endpoint: %v", o["_raw"])
	}
	// The apikey query parameter takes the same path.
	if got, _ := e.call(anon, "POST", "/radarr/api/v3/tag?apikey="+ro, map[string]any{"label": "x"}); got != http.StatusForbidden {
		t.Errorf("read-only key in the query: %d", got)
	}
}

// A key made before its owner became a viewer follows the new role.
func TestOldKeyOfAnAccountThatBecameAViewer(t *testing.T) {
	e := newSignInEnv(t, nil)
	id := e.addAccount("sam", "member")
	sam, _, _ := e.login("sam", "long-enough-password")
	key := e.must(sam, "POST", "/api/auth/api-keys", map[string]any{"name": "script"}, http.StatusCreated)["key"].(string)
	anon := e.client()
	if got, _ := e.call(anon, "POST", "/api/exclusions", map[string]any{}, "X-API-Key", key); got == http.StatusForbidden {
		t.Fatal("a member's key was refused before the change")
	}
	e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]any{"role": "viewer"}, http.StatusOK)
	if got, _ := e.call(anon, "POST", "/api/exclusions", map[string]any{}, "X-API-Key", key); got != http.StatusForbidden {
		t.Errorf("a viewer's old key can still change things: %d", got)
	}
	if got, _ := e.call(sam, "POST", "/api/exclusions", map[string]any{}); got != http.StatusForbidden {
		t.Errorf("the open session of a viewer can still change things: %d", got)
	}
}

func TestRoleChanges(t *testing.T) {
	e := newSignInEnv(t, nil)
	id := e.addAccount("sam", "member")
	for _, role := range []string{"viewer", "admin", "member", "viewer"} {
		out := e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]any{"role": role}, http.StatusOK)
		if out["role"] != role || out["isAdmin"] != (role == "admin") {
			t.Fatalf("role %s: %v", role, out)
		}
	}
	e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]any{"role": "superuser"}, http.StatusBadRequest)
	// The only administrator can't become a viewer.
	me := e.must(e.admin, "GET", "/api/auth/me", nil, http.StatusOK)
	e.must(e.admin, "PUT", "/api/users/"+itoa(int64(me["id"].(float64))), map[string]any{"role": "viewer"}, http.StatusBadRequest)
	// A viewer can be created directly, and bad roles are refused.
	e.must(e.admin, "POST", "/api/users", map[string]any{"username": "vic", "password": "long-enough-password", "role": "viewer"}, http.StatusCreated)
	e.must(e.admin, "POST", "/api/users", map[string]any{"username": "vlad", "password": "long-enough-password", "role": "root"}, http.StatusBadRequest)
}

func TestDisablingAnAccount(t *testing.T) {
	e := newSignInEnv(t, nil)
	id := e.addAccount("sam", "member")
	sam, status, _ := e.login("sam", "long-enough-password")
	if status != http.StatusOK {
		t.Fatal("sign-in")
	}
	key := e.must(sam, "POST", "/api/auth/api-keys", map[string]any{"name": "script"}, http.StatusCreated)["key"].(string)
	e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]any{"disabled": true}, http.StatusOK)

	// Everything of theirs stops at once.
	if got, _ := e.call(sam, "GET", "/api/auth/me", nil); got != http.StatusUnauthorized {
		t.Errorf("open session of a disabled account: %d", got)
	}
	if got, _ := e.call(e.client(), "GET", "/api/movies", nil, "X-API-Key", key); got != http.StatusUnauthorized {
		t.Errorf("API key of a disabled account: %d", got)
	}
	// The right password is told why; a wrong one is not told anything new.
	_, got, out := e.login("sam", "long-enough-password")
	if got != http.StatusForbidden || !strings.Contains(out["_raw"].(string), "turned off") {
		t.Errorf("disabled sign-in: %d %v", got, out["_raw"])
	}
	if _, got, _ := e.login("sam", "not-the-password"); got != http.StatusUnauthorized {
		t.Errorf("wrong password for a disabled account: %d", got)
	}
	// The list says so, and turning it on again works.
	_, list := e.call(e.admin, "GET", "/api/users", nil)
	if !strings.Contains(list["_raw"].(string), `"disabled":true`) {
		t.Errorf("the account list doesn't show it: %v", list["_raw"])
	}
	e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]any{"disabled": false}, http.StatusOK)
	if _, got, _ := e.login("sam", "long-enough-password"); got != http.StatusOK {
		t.Errorf("sign-in after turning it on again: %d", got)
	}
	// Nobody turns themselves off.
	me := e.must(e.admin, "GET", "/api/auth/me", nil, http.StatusOK)
	e.must(e.admin, "PUT", "/api/users/"+itoa(int64(me["id"].(float64))), map[string]any{"disabled": true}, http.StatusBadRequest)
}
