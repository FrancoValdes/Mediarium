package api

import (
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

func TestCeremonyStore(t *testing.T) {
	now := time.Now()
	c := newCeremonyStore()
	sd := webauthn.SessionData{Challenge: "abc"}

	id, err := c.put(kindLogin, 0, sd, now)
	if err != nil || len(id) < 32 {
		t.Fatalf("put: %q, %v", id, err)
	}
	// The wrong kind of ceremony is refused, and that uses it up.
	if _, ok := c.take(id, kindRegister, now); ok {
		t.Error("a login challenge was taken as a registration")
	}
	if _, ok := c.take(id, kindLogin, now); ok {
		t.Error("a challenge survived a wrong attempt")
	}
	// A challenge works once.
	id, _ = c.put(kindRegister, 7, sd, now)
	got, ok := c.take(id, kindRegister, now)
	if !ok || got.userID != 7 || got.session.Challenge != "abc" {
		t.Errorf("take = %+v, %v", got, ok)
	}
	if _, ok := c.take(id, kindRegister, now); ok {
		t.Error("a challenge was taken twice")
	}
	// It expires.
	id, _ = c.put(kindLogin, 0, sd, now)
	if _, ok := c.take(id, kindLogin, now.Add(ceremonyTTL+time.Second)); ok {
		t.Error("an old challenge was accepted")
	}
	// Unknown and empty ids.
	for _, bad := range []string{"", "nope", id} {
		if _, ok := c.take(bad, kindLogin, now); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
	// A flood stops at a limit, and old ones are swept to make room.
	for i := 0; i < maxCeremony; i++ {
		if _, err := c.put(kindLogin, 0, sd, now); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	if _, err := c.put(kindLogin, 0, sd, now); err == nil {
		t.Error("no limit on waiting challenges")
	}
	if _, err := c.put(kindLogin, 0, sd, now.Add(ceremonyTTL+time.Second)); err != nil {
		t.Errorf("expired challenges were not swept: %v", err)
	}
}

func TestReadOnlyAllowsTable(t *testing.T) {
	// readOnlyAllows is the one rule; the route walk in viewer_test.go proves it on every route.
	cases := []struct {
		name            string
		viewer, roKey   bool
		admin           bool
		method, pattern string
		want            bool
	}{
		{"viewer reads", true, false, false, "GET", "GET /api/movies", true},
		{"viewer HEAD", true, false, false, "HEAD", "GET /api/movies", true},
		{"viewer writes", true, false, false, "POST", "POST /api/movies", false},
		{"viewer deletes", true, false, false, "DELETE", "DELETE /api/queue/{id}", false},
		{"viewer own profile", true, false, false, "PUT", "PUT /api/auth/profile", true},
		{"viewer reads the keys", true, false, false, "GET", "GET /api/auth/api-keys", false},
		{"viewer makes a key", true, false, false, "POST", "POST /api/auth/api-keys", false},
		{"read-only key reads", false, true, false, "GET", "GET /api/movies", true},
		{"read-only key writes", false, true, false, "POST", "POST /api/movies", false},
		{"read-only key own profile", false, true, false, "PUT", "PUT /api/auth/profile", false},
		{"read-only admin key writes", false, true, true, "PUT", "PUT /api/settings", false},
		{"read-only key on a viewer's own profile", true, true, false, "PUT", "PUT /api/auth/profile", false},
	}
	for _, c := range cases {
		u := newTestUser(c.viewer, c.roKey, c.admin)
		got, msg := readOnlyAllows(u, c.method, c.pattern)
		if got != c.want || (!got && msg == "") {
			t.Errorf("%s: allowed=%v (%q), want %v", c.name, got, msg, c.want)
		}
	}
}
