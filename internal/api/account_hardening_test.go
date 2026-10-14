package api_test

import (
	"net/http"
	"testing"
)

// Deleting an account and switching off someone's two-step sign-in need a
// signed-in administrator, as making and changing accounts do: a leaked
// administrator key can't do either.
func TestAccountChangesNeedASession(t *testing.T) {
	e := newSignInEnv(t, nil)
	id := e.addAccount("kim", "member")
	key := e.must(e.admin, "POST", "/api/auth/api-keys", map[string]string{"name": "script"}, http.StatusCreated)["key"].(string)
	script := e.client()
	for _, path := range []string{"/api/users/" + itoa(id) + "/two-step", "/api/users/" + itoa(id)} {
		if status, _ := e.call(script, "DELETE", path, nil, "X-API-Key", key); status != http.StatusForbidden {
			t.Errorf("DELETE %s with an API key: %d", path, status)
		}
	}
	if got := e.scalar(`SELECT COUNT(*) FROM users WHERE id = ?`, id); got != "1" {
		t.Fatalf("the account was deleted with an API key")
	}
	// An e-reader password, a Kobo address or a calendar feed would outlive the key.
	for _, path := range []string{"/api/calendar/feed", "/api/auth/ereader/opds", "/api/auth/ereader/kobo"} {
		if status, _ := e.call(script, "POST", path, nil, "X-API-Key", key); status != http.StatusForbidden {
			t.Errorf("POST %s with an API key: %d", path, status)
		}
	}
	e.must(e.admin, "DELETE", "/api/users/"+itoa(id)+"/two-step", nil, http.StatusOK)
	e.must(e.admin, "DELETE", "/api/users/"+itoa(id), nil, http.StatusOK)
}

// A new password set by an administrator also stops the person's API keys and
// calendar feed, which work without the password.
func TestAdminPasswordResetRevokesKeys(t *testing.T) {
	e := newSignInEnv(t, nil)
	id := e.addAccount("kim", "member")
	kim, status, _ := e.login("kim", "long-enough-password")
	if status != http.StatusOK {
		t.Fatalf("sign in: %d", status)
	}
	key := e.must(kim, "POST", "/api/auth/api-keys", map[string]string{"name": "phone"}, http.StatusCreated)["key"].(string)
	e.must(kim, "POST", "/api/calendar/feed", nil, http.StatusOK)
	if status, _ := e.call(e.client(), "GET", "/api/auth/me", nil, "X-API-Key", key); status != http.StatusOK {
		t.Fatalf("the key doesn't work to start with: %d", status)
	}
	e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]string{"password": "a-fresh-password-1"}, http.StatusOK)
	if status, _ := e.call(e.client(), "GET", "/api/auth/me", nil, "X-API-Key", key); status != http.StatusUnauthorized {
		t.Errorf("the key still works after an administrator set a new password: %d", status)
	}
	if got := e.scalar(`SELECT COUNT(*) FROM calendar_feeds WHERE user_id = ?`, id); got != "0" {
		t.Errorf("the calendar feed is still there")
	}
	if status, _ := e.call(kim, "GET", "/api/auth/me", nil); status != http.StatusUnauthorized {
		t.Errorf("the old session still works: %d", status)
	}
}

// Wrong tries at your own password before a change to two-step sign-in, a
// passkey or a single sign-on link count per account as well as per address.
func TestPasswordConfirmationIsLimitedPerAccount(t *testing.T) {
	e := newSignInEnv(t, nil)
	e.addAccount("kim", "member")
	kim, _, _ := e.login("kim", "long-enough-password")
	for i := 0; i < 25; i++ {
		e.call(kim, "POST", "/api/auth/two-step/recovery", map[string]string{"password": "wrong"}, "X-Forwarded-For", "203.0.113."+itoa(int64(i+1)))
	}
	if status, _ := e.call(kim, "POST", "/api/auth/two-step/recovery", map[string]string{"password": "long-enough-password"}, "X-Forwarded-For", "203.0.113.200"); status != http.StatusTooManyRequests {
		t.Errorf("after 25 wrong tries from many addresses the right password got %d", status)
	}
}

// The Not interested list is shared and decides what import lists add, so
// only someone who may add that kind of title themselves can change it.
func TestNotInterestedNeedsTheRightToAdd(t *testing.T) {
	e := newSignInEnv(t, nil)
	id := e.addAccount("kim", "member")
	perms := map[string]any{"addDirect": false, "movies": true, "tv": true, "music": true, "books": true, "releases": true, "manage": true, "retry": true, "subtitles": true, "play": true}
	e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]any{"permissions": perms}, http.StatusOK)
	kim, _, _ := e.login("kim", "long-enough-password")
	movie := map[string]any{"kind": "movie", "tmdbId": 603, "title": "Night Harbour"}
	e.must(kim, "POST", "/api/exclusions", movie, http.StatusForbidden)
	e.must(e.admin, "POST", "/api/exclusions", movie, http.StatusOK)
	e.must(kim, "DELETE", "/api/exclusions/movie/603", nil, http.StatusForbidden)

	perms["addDirect"], perms["tv"] = true, false
	e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]any{"permissions": perms}, http.StatusOK)
	e.must(kim, "POST", "/api/exclusions", map[string]any{"kind": "tv", "tmdbId": 1399, "title": "A Show"}, http.StatusForbidden)
	e.must(kim, "DELETE", "/api/exclusions/movie/603", nil, http.StatusOK)
}
