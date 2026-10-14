package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/config"
	"github.com/rdborg/mediarium/internal/oidcauth/oidctest"
)

const publicURL = "https://mediarium.example.com"

// oidcEnv is a server whose single sign-on points at a fake provider.
type oidcEnv struct {
	*signInEnv
	idp *oidctest.IdP
}

func (e *oidcEnv) config(over map[string]any) map[string]any {
	c := map[string]any{
		"enabled": true, "name": "Authentik", "issuer": e.idp.URL, "clientId": e.idp.ClientID, "clientSecret": e.idp.Secret,
		"autoCreate": true, "defaultRole": "member",
	}
	for k, v := range over {
		c[k] = v
	}
	return c
}

func (e *oidcEnv) saveConfig(over map[string]any) map[string]any {
	e.t.Helper()
	return e.must(e.admin, "PUT", "/api/auth/oidc", e.config(over), http.StatusOK)
}

func newOIDCEnv(t *testing.T) *oidcEnv {
	t.Helper()
	e := &oidcEnv{signInEnv: newSignInEnv(t, nil), idp: oidctest.New(t)}
	e.setPublicURL(publicURL)
	e.saveConfig(nil)
	return e
}

// browse plays a browser doing a whole single sign-on: start, the provider
// hands out a code, the callback. It returns where the callback sent the
// browser.
func (e *oidcEnv) browse(c *http.Client, startQuery string) (location string) {
	e.t.Helper()
	state, code, _ := e.begin(c, startQuery)
	return e.callback(c, state, code)
}

// begin starts a sign-in and answers the provider's state and code.
func (e *oidcEnv) begin(c *http.Client, startQuery string) (state, code, authURL string) {
	e.t.Helper()
	resp, err := c.Get(e.base + "/api/auth/oidc/start" + startQuery)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("start: status %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, e.idp.URL+"/authorize") {
		e.t.Fatalf("start sent the browser to %q", loc)
	}
	state, code = e.idp.Authorize(e.t, loc)
	return state, code, loc
}

func (e *oidcEnv) callback(c *http.Client, state, code string) string {
	e.t.Helper()
	q := url.Values{}
	if state != "" {
		q.Set("state", state)
	}
	if code != "" {
		q.Set("code", code)
	}
	resp, err := c.Get(e.base + "/api/auth/oidc/callback?" + q.Encode())
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("callback: status %d", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func (e *oidcEnv) users() int {
	return atoi(e.scalar(`SELECT COUNT(*) FROM users`))
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestOIDCFirstSignInMakesAnAccount(t *testing.T) {
	e := newOIDCEnv(t)
	browser := e.client()
	if loc := e.browse(browser, ""); loc != "/" {
		t.Fatalf("callback sent the browser to %q", loc)
	}
	user, role := e.signedInAs(browser)
	if user != "sam" || role != "member" {
		t.Fatalf("signed in as %q (%s)", user, role)
	}
	me := e.must(browser, "GET", "/api/auth/me", nil, http.StatusOK)
	if me["source"] != "oidc" || me["email"] != "sam@example.com" || me["name"] != "Sam Example" {
		t.Errorf("profile = %v", me)
	}
	if got := e.scalar(`SELECT method FROM sessions WHERE user_id = (SELECT id FROM users WHERE username = 'sam')`); got != "oidc" {
		t.Errorf("session method = %q", got)
	}
	// Signing in again finds the same account.
	before := e.users()
	other := e.client()
	e.browse(other, "")
	if u, _ := e.signedInAs(other); u != "sam" || e.users() != before {
		t.Errorf("second sign-in: %q, accounts %d -> %d", u, before, e.users())
	}
	// No password exists for this account, here or anywhere.
	if hash := e.scalar(`SELECT password_hash FROM users WHERE username = 'sam'`); hash != "" {
		t.Errorf("a password hash was stored: %q", hash)
	}
	for _, pw := range []string{"", "sam", "password", "sam@example.com", "$2a$12$whatever"} {
		if _, status, _ := e.login("sam", pw); status != http.StatusUnauthorized {
			t.Errorf("signed in with the password %q: %d", pw, status)
		}
	}
	// It can't change a password here, or set one through the account list.
	e.must(browser, "POST", "/api/auth/change-password", map[string]string{"currentPassword": "x", "newPassword": "a-new-long-password"}, http.StatusBadRequest)
	id := e.scalar(`SELECT id FROM users WHERE username = 'sam'`)
	e.must(e.admin, "PUT", "/api/users/"+id, map[string]string{"password": "an-admin-set-password"}, http.StatusBadRequest)
	if hash := e.scalar(`SELECT password_hash FROM users WHERE username = 'sam'`); hash != "" {
		t.Errorf("a password hash appeared: %q", hash)
	}
	_, list := e.call(e.admin, "GET", "/api/users", nil)
	if !strings.Contains(list["_raw"].(string), `"source":"oidc"`) {
		t.Errorf("the account list doesn't say where sam comes from")
	}
}

func TestOIDCAdminGroupAndDemotion(t *testing.T) {
	e := newOIDCEnv(t)
	e.saveConfig(map[string]any{"adminGroup": "media-admins"})
	// A member of the administrator group is an administrator.
	b := e.client()
	e.browse(b, "")
	if u, role := e.signedInAs(b); u != "sam" || role != "admin" {
		t.Fatalf("group member: %q %s", u, role)
	}
	e.must(b, "GET", "/api/users", nil, http.StatusOK)
	// Taken out of the group, the next sign-in makes them a member again.
	e.idp.Mutate = func(c map[string]any) { c["groups"] = []string{"family"} }
	b2 := e.client()
	e.browse(b2, "")
	if _, role := e.signedInAs(b2); role != "member" {
		t.Fatalf("after leaving the group: %s", role)
	}
	e.must(b2, "GET", "/api/users", nil, http.StatusForbidden)

	// A default role of viewer, and no group at all.
	e.saveConfig(map[string]any{"defaultRole": "viewer", "adminGroup": ""})
	e.idp.Mutate = func(c map[string]any) {
		c["sub"] = "someone-else"
		c["preferred_username"] = "vic"
		c["groups"] = []string{"media-admins"}
	}
	b3 := e.client()
	e.browse(b3, "")
	if u, role := e.signedInAs(b3); u != "vic" || role != "viewer" {
		t.Errorf("default viewer: %q %s (the group means nothing when no admin group is set)", u, role)
	}
}

func TestOIDCNeverDemotesTheLastAdministrator(t *testing.T) {
	e := newOIDCEnv(t)
	e.saveConfig(map[string]any{"adminGroup": "media-admins"})
	// Make the SSO account the only administrator: promote it, then demote ryan.
	b := e.client()
	e.browse(b, "")
	me := e.must(b, "GET", "/api/auth/me", nil, http.StatusOK)
	_ = me
	ryan := e.scalar(`SELECT id FROM users WHERE username = 'ryan'`)
	e.must(b, "PUT", "/api/users/"+ryan, map[string]string{"role": "member"}, http.StatusOK)
	// Now sam is the last administrator, and the provider says he isn't one any more.
	e.idp.Mutate = func(c map[string]any) { c["groups"] = []string{"family"} }
	b2 := e.client()
	e.browse(b2, "")
	if _, role := e.signedInAs(b2); role != "admin" {
		t.Errorf("the last administrator was demoted: %s", role)
	}
}

func TestOIDCRefusals(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]any
		claims func(map[string]any)
		setup  func(e *oidcEnv)
		want   string
	}{
		{"no account and none made automatically", map[string]any{"autoCreate": false}, nil, nil, "no_account"},
		{"email domain not allowed", map[string]any{"allowedDomains": "family.example"}, nil, nil, "not_allowed"},
		{"email domain look-alike", map[string]any{"allowedDomains": "example.com"}, func(c map[string]any) { c["email"] = "sam@example.com.evil.net" }, nil, "not_allowed"},
		{"no email while domains are limited", map[string]any{"allowedDomains": "example.com"}, func(c map[string]any) { delete(c, "email") }, nil, "not_allowed"},
		{"not in the allowed group", map[string]any{"allowedGroup": "mediarium"}, nil, nil, "not_allowed"},
		{"unverified email may not link", map[string]any{"linkByEmail": true, "autoCreate": false}, func(c map[string]any) { c["email_verified"] = false }, nil, "email_unverified"},
		{"verified email of an administrator is not linked", map[string]any{"linkByEmail": true, "autoCreate": false}, func(c map[string]any) { c["email"] = "ryan@example.com" },
			func(e *oidcEnv) {
				e.must(e.admin, "PUT", "/api/users/1", map[string]string{"email": "ryan@example.com"}, http.StatusOK)
			}, "link_admin"},
		{"two accounts with that email", map[string]any{"linkByEmail": true, "autoCreate": false}, nil,
			func(e *oidcEnv) {
				for _, n := range []string{"sam1", "sam2"} {
					id := e.addAccount(n, "member")
					e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]string{"email": "sam@example.com"}, http.StatusOK)
				}
			}, "ambiguous"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newOIDCEnv(t)
			e.saveConfig(tc.config)
			if tc.setup != nil {
				tc.setup(e)
			}
			e.idp.Mutate = tc.claims
			b := e.client()
			before := e.users()
			loc := e.browse(b, "")
			if want := "/?signin_error=" + tc.want; loc != want {
				t.Fatalf("sent to %q, want %q", loc, want)
			}
			if u, _ := e.signedInAs(b); u != "" {
				t.Errorf("signed in as %q anyway", u)
			}
			if e.users() != before {
				t.Errorf("an account was made")
			}
		})
	}
}

func TestOIDCLinkByVerifiedEmail(t *testing.T) {
	e := newOIDCEnv(t)
	e.saveConfig(map[string]any{"linkByEmail": true, "autoCreate": false})
	id := e.addAccount("samantha", "member")
	e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]string{"email": "Sam@Example.com"}, http.StatusOK)
	b := e.client()
	if loc := e.browse(b, ""); loc != "/" {
		t.Fatalf("callback sent the browser to %q", loc)
	}
	if u, role := e.signedInAs(b); u != "samantha" || role != "member" {
		t.Fatalf("signed in as %q (%s)", u, role)
	}
	// The password keeps working: it is the same, local account.
	if _, status, _ := e.login("samantha", "long-enough-password"); status != http.StatusOK {
		t.Errorf("the account lost its password: %d", status)
	}
	// And from now on the link is by the provider's id, not the email address.
	e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]string{"email": "other@example.com"}, http.StatusOK)
	b2 := e.client()
	e.browse(b2, "")
	if u, _ := e.signedInAs(b2); u != "samantha" {
		t.Errorf("after the email changed: %q", u)
	}
	// Someone else with the same provider id cannot reuse the link (a new provider account with the same address).
	e.idp.Mutate = func(c map[string]any) { c["sub"] = "intruder"; c["email_verified"] = true }
	b3 := e.client()
	loc := e.browse(b3, "")
	if loc != "/?signin_error=no_account" {
		t.Errorf("a different provider id with an address that no longer matches: %q", loc)
	}
}

// An address a member typed on their own profile could be someone else's, so a
// first single sign-on is never linked by it; one an administrator set is.
func TestOIDCLinkByEmailIgnoresSelfTypedAddress(t *testing.T) {
	e := newOIDCEnv(t)
	e.saveConfig(map[string]any{"linkByEmail": true, "autoCreate": false})
	e.addAccount("mallory", "member")
	mallory, status, _ := e.login("mallory", "long-enough-password")
	if status != http.StatusOK {
		t.Fatalf("sign in: %d", status)
	}
	e.must(mallory, "PUT", "/api/auth/profile", map[string]string{"username": "mallory", "email": "sam@example.com"}, http.StatusOK)
	b := e.client()
	if loc := e.browse(b, ""); loc != "/?signin_error=no_account" {
		t.Fatalf("linked by an address the member typed: sent to %q", loc)
	}
	if u, _ := e.signedInAs(b); u != "" {
		t.Fatalf("signed in as %q", u)
	}
	// Set by an administrator, the same address links.
	id := e.addAccount("samantha", "member")
	e.must(e.admin, "PUT", "/api/users/"+itoa(id), map[string]string{"email": "sam@example.com"}, http.StatusOK)
	b2 := e.client()
	e.browse(b2, "")
	if u, _ := e.signedInAs(b2); u != "samantha" {
		t.Fatalf("signed in as %q, want samantha", u)
	}
}

func TestOIDCCallbackAttacks(t *testing.T) {
	t.Run("a callback with nothing", func(t *testing.T) {
		e := newOIDCEnv(t)
		if loc := e.callback(e.client(), "", ""); loc != "/?signin_error=state" {
			t.Errorf("sent to %q", loc)
		}
	})
	t.Run("a made-up state", func(t *testing.T) {
		e := newOIDCEnv(t)
		b := e.client()
		_, code, _ := e.begin(b, "")
		if loc := e.callback(b, "guessed", code); loc != "/?signin_error=state" {
			t.Errorf("sent to %q", loc)
		}
	})
	t.Run("no cookie: someone else's browser finishing a sign-in", func(t *testing.T) {
		e := newOIDCEnv(t)
		victim := e.client()
		state, code, _ := e.begin(victim, "")
		attacker := e.client() // has no binding cookie
		if loc := e.callback(attacker, state, code); loc != "/?signin_error=state" {
			t.Errorf("sent to %q", loc)
		}
		if u, _ := e.signedInAs(attacker); u != "" {
			t.Errorf("the attacker is signed in as %q", u)
		}
	})
	t.Run("a cookie from another sign-in", func(t *testing.T) {
		e := newOIDCEnv(t)
		a, b := e.client(), e.client()
		stateA, codeA, _ := e.begin(a, "")
		e.begin(b, "") // b now holds the cookie of its own sign-in
		if loc := e.callback(b, stateA, codeA); loc != "/?signin_error=state" {
			t.Errorf("sent to %q", loc)
		}
		if u, _ := e.signedInAs(b); u != "" {
			t.Errorf("signed in as %q", u)
		}
	})
	t.Run("the same answer twice", func(t *testing.T) {
		e := newOIDCEnv(t)
		b := e.client()
		state, code, _ := e.begin(b, "")
		if loc := e.callback(b, state, code); loc != "/" {
			t.Fatalf("first: %q", loc)
		}
		// The state is used up, and the cookie was taken away with it.
		if loc := e.callback(b, state, code); loc != "/?signin_error=state" {
			t.Errorf("replay: %q", loc)
		}
	})
	t.Run("the provider says no", func(t *testing.T) {
		e := newOIDCEnv(t)
		b := e.client()
		state, _, _ := e.begin(b, "")
		resp, err := b.Get(e.base + "/api/auth/oidc/callback?error=access_denied&state=" + url.QueryEscape(state))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if loc := resp.Header.Get("Location"); loc != "/?signin_error=provider" {
			t.Errorf("sent to %q", loc)
		}
	})
	t.Run("a code for something else", func(t *testing.T) {
		e := newOIDCEnv(t)
		b := e.client()
		state, _, _ := e.begin(b, "")
		if loc := e.callback(b, state, "code-from-elsewhere"); loc != "/?signin_error=token" {
			t.Errorf("sent to %q", loc)
		}
	})
	for name, tc := range map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"an ID token for another audience": {func(c map[string]any) { c["aud"] = "another-app" }, "id_token"},
		"a replayed nonce":                 {func(c map[string]any) { c["nonce"] = "from-an-earlier-login" }, "nonce"},
		"an expired ID token":              {func(c map[string]any) { c["exp"] = 1000 }, "id_token"},
		"an ID token from another issuer":  {func(c map[string]any) { c["iss"] = "https://evil.example.com" }, "id_token"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newOIDCEnv(t)
			e.idp.Mutate = tc.mutate
			b := e.client()
			if loc := e.browse(b, ""); loc != "/?signin_error="+tc.want {
				t.Errorf("sent to %q", loc)
			}
			if u, _ := e.signedInAs(b); u != "" {
				t.Errorf("signed in as %q", u)
			}
		})
	}
	t.Run("many wrong answers are slowed down like wrong passwords", func(t *testing.T) {
		e := newOIDCEnv(t)
		b := e.client()
		var last string
		for i := 0; i < 12; i++ {
			last = e.callback(b, "guess", "guess")
		}
		if last != "/?signin_error=limit" {
			t.Errorf("after 12 wrong callbacks: %q", last)
		}
	})
}

func TestOIDCNextAndRedirectAddress(t *testing.T) {
	e := newOIDCEnv(t)
	// Where the browser lands afterwards: only a path on this site.
	for next, want := range map[string]string{
		"/movies?x=1":           "/movies?x=1",
		"//evil.example":        "/",
		"https://evil.example/": "/", "/\\evil.example": "/", "javascript:alert(1)": "/", "": "/",
	} {
		b := e.client()
		q := ""
		if next != "" {
			q = "?next=" + url.QueryEscape(next)
		}
		if loc := e.browse(b, q); loc != want {
			t.Errorf("next=%q landed on %q, want %q", next, loc, want)
		}
	}
	// The address the provider sends the browser back to comes from the saved
	// public address, never from the Host the request arrived with.
	req, _ := http.NewRequest("GET", e.base+"/api/auth/oidc/start", nil)
	req.Host = "evil.example.com"
	req.Header.Set("X-Forwarded-Host", "evil.example.com")
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := e.client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if got := loc.Query().Get("redirect_uri"); got != publicURL+"/api/auth/oidc/callback" {
		t.Errorf("redirect_uri = %q", got)
	}
	// And the sign-in asks for what it should.
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("state") == "" || q.Get("nonce") == "" || !strings.Contains(q.Get("scope"), "openid") {
		t.Errorf("authorize query = %v", q)
	}
}

func TestOIDCBindingCookie(t *testing.T) {
	e := newOIDCEnv(t)
	resp, err := e.client().Get(e.base + "/api/auth/oidc/start")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var found *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "mediarium_oidc" {
			found = c
		}
	}
	if found == nil {
		t.Fatal("no binding cookie")
	}
	// Lax, not Strict: the browser comes back from the provider's site, and a Strict cookie would not be sent.
	if !found.HttpOnly || found.SameSite != http.SameSiteLaxMode || found.Path != "/api/auth/oidc" || found.MaxAge <= 0 || found.MaxAge > 600 || len(found.Value) < 32 {
		t.Errorf("cookie = %+v", found)
	}
	// Over HTTPS (through a proxy that says so) it is Secure.
	req, _ := http.NewRequest("GET", e.base+"/api/auth/oidc/start", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	resp2, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	secure := false
	for _, c := range resp2.Cookies() {
		secure = secure || (c.Name == "mediarium_oidc" && c.Secure)
	}
	if !secure {
		t.Error("the binding cookie isn't Secure over HTTPS")
	}
	// The session cookie a sign-in sets stays HttpOnly and Strict.
	b := e.client()
	state, code, _ := e.begin(b, "")
	cb, _ := http.NewRequest("GET", e.base+"/api/auth/oidc/callback?state="+state+"&code="+code, nil)
	u, _ := url.Parse(e.base)
	for _, c := range b.Jar.Cookies(u) {
		cb.AddCookie(c)
	}
	r3, _ := e.client().Do(cb)
	r3.Body.Close()
	for _, c := range r3.Cookies() {
		if c.Name == "mediarium_session" && (!c.HttpOnly || c.SameSite != http.SameSiteStrictMode) {
			t.Errorf("session cookie = %+v", c)
		}
	}
}

func TestOIDCDisabledAccountAndOffSwitches(t *testing.T) {
	e := newOIDCEnv(t)
	b := e.client()
	e.browse(b, "")
	id := e.scalar(`SELECT id FROM users WHERE username = 'sam'`)
	e.must(e.admin, "PUT", "/api/users/"+id, map[string]any{"disabled": true}, http.StatusOK)
	if got, _ := e.call(b, "GET", "/api/auth/me", nil); got != http.StatusUnauthorized {
		t.Errorf("session of a disabled account: %d", got)
	}
	if loc := e.browse(e.client(), ""); loc != "/?signin_error=disabled" {
		t.Errorf("sign-in of a disabled account sent to %q", loc)
	}
	e.must(e.admin, "PUT", "/api/users/"+id, map[string]any{"disabled": false}, http.StatusOK)

	// Switched off, it is gone from the sign-in page and the start refuses.
	e.saveConfig(map[string]any{"enabled": false})
	if m := e.must(e.client(), "GET", "/api/auth/methods", nil, http.StatusOK); m["oidc"] != nil {
		t.Errorf("methods still list it: %v", m)
	}
	resp, _ := e.client().Get(e.base + "/api/auth/oidc/start")
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/?signin_error=off" {
		t.Errorf("start while off sent to %q", loc)
	}
	// Without the public address it can't be on (and isn't offered).
	e.saveConfig(nil)
	e.setPublicURL("")
	if m := e.must(e.client(), "GET", "/api/auth/methods", nil, http.StatusOK); m["oidc"] != nil {
		t.Errorf("offered without a public address: %v", m)
	}
}

func TestOIDCSettingsKeepTheSecretSecret(t *testing.T) {
	e := newOIDCEnv(t)
	got := e.must(e.admin, "GET", "/api/auth/oidc", nil, http.StatusOK)
	raw := got["_raw"].(string)
	if strings.Contains(raw, e.idp.Secret) || got["clientSecret"] != "" || got["hasClientSecret"] != true {
		t.Errorf("the secret leaks or is not reported as set: %v", raw)
	}
	if got["redirectUrl"] != publicURL+"/api/auth/oidc/callback" || got["publicUrlSet"] != true {
		t.Errorf("redirect = %v", got["redirectUrl"])
	}
	// Left blank, the saved secret is kept (sign-in still works).
	e.saveConfig(map[string]any{"clientSecret": "", "name": "Renamed"})
	e.browse(e.client(), "")
	// Moved to another provider address with the secret left blank: the saved
	// secret does not follow, so it can't be switched on without typing one.
	other := oidctest.New(t)
	status, out := e.call(e.admin, "PUT", "/api/auth/oidc", e.config(map[string]any{"issuer": other.URL, "clientSecret": ""}))
	if status != http.StatusBadRequest || !strings.Contains(out["_raw"].(string), "client secret") {
		t.Errorf("moving the provider kept the secret: %d %v", status, out["_raw"])
	}
	// The same for a draft: it is saved without the old secret.
	e.must(e.admin, "PUT", "/api/auth/oidc", e.config(map[string]any{"enabled": false, "issuer": other.URL, "clientSecret": ""}), http.StatusOK)
	if got := e.must(e.admin, "GET", "/api/auth/oidc", nil, http.StatusOK); got["hasClientSecret"] != false {
		t.Errorf("the old secret followed the address: %v", got["hasClientSecret"])
	}
	// A provider that does not answer can't be switched on.
	status, out = e.call(e.admin, "PUT", "/api/auth/oidc", e.config(map[string]any{"issuer": "http://127.0.0.1:1"}))
	if status != http.StatusBadRequest {
		t.Errorf("unreachable provider: %d %v", status, out["_raw"])
	}
	// Nor without the information it needs.
	status, _ = e.call(e.admin, "PUT", "/api/auth/oidc", e.config(map[string]any{"clientId": ""}))
	if status != http.StatusBadRequest {
		t.Errorf("no client id: %d", status)
	}
}

func TestOIDCSettingsAreForAdministratorsInABrowser(t *testing.T) {
	e := newOIDCEnv(t)
	e.addAccount("sam", "member")
	sam, _, _ := e.login("sam", "long-enough-password")
	key := e.must(e.admin, "POST", "/api/auth/api-keys", map[string]any{"name": "script"}, http.StatusCreated)["key"].(string)
	anon := e.client()
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/auth/oidc"}, {"PUT", "/api/auth/oidc"}, {"POST", "/api/auth/oidc/test"},
		{"GET", "/api/auth/ldap"}, {"PUT", "/api/auth/ldap"}, {"POST", "/api/auth/ldap/test"},
		{"GET", "/api/auth/sign-in"}, {"PUT", "/api/auth/sign-in"},
	} {
		if got, _ := e.call(sam, tc.method, tc.path, e.config(nil)); got != http.StatusForbidden {
			t.Errorf("a member could %s %s: %d", tc.method, tc.path, got)
		}
		if tc.method != "GET" {
			if got, out := e.call(anon, tc.method, tc.path, e.config(nil), "X-API-Key", key); got != http.StatusForbidden || !strings.Contains(out["_raw"].(string), "web page") {
				t.Errorf("an API key could %s %s: %d %v", tc.method, tc.path, got, out["_raw"])
			}
		}
	}
	// None of the answers an administrator gets contain a stored secret.
	for _, path := range []string{"/api/auth/oidc", "/api/auth/ldap", "/api/auth/sign-in"} {
		_, out := e.call(e.admin, "GET", path, nil)
		if strings.Contains(out["_raw"].(string), e.idp.Secret) {
			t.Errorf("%s shows the secret", path)
		}
	}
}

func TestOIDCTestButton(t *testing.T) {
	e := newOIDCEnv(t)
	out := e.must(e.admin, "POST", "/api/auth/oidc/test", map[string]any{"issuer": e.idp.URL}, http.StatusOK)
	if out["ok"] != true || out["redirectUrl"] != publicURL+"/api/auth/oidc/callback" || out["authorizationEndpoint"] != e.idp.URL+"/authorize" {
		t.Errorf("test = %v", out)
	}
	status, out := e.call(e.admin, "POST", "/api/auth/oidc/test", map[string]any{"issuer": "http://127.0.0.1:1"})
	if status != http.StatusBadGateway || out["error"] == "" {
		t.Errorf("unreachable: %d %v", status, out)
	}
	e.must(e.admin, "POST", "/api/auth/oidc/test", map[string]any{}, http.StatusBadRequest)
}

func TestSignInMethodsPageInfo(t *testing.T) {
	e := newOIDCEnv(t)
	m := e.must(e.client(), "GET", "/api/auth/methods", nil, http.StatusOK)
	oidc, _ := m["oidc"].(map[string]any)
	if m["local"] != true || oidc == nil || oidc["name"] != "Authentik" || m["ldap"] != false || m["passkeys"] != false {
		t.Errorf("methods = %v", m)
	}
	if strings.Contains(m["_raw"].(string), e.idp.URL) || strings.Contains(m["_raw"].(string), "clientId") {
		t.Errorf("the public answer says too much: %v", m["_raw"])
	}
}

// Password sign-in can be turned off, but only by an administrator who has
// already signed in another way, and the environment switch always brings it back.
func TestKeepLocalSignInOnSafeguard(t *testing.T) {
	e := newOIDCEnv(t)
	e.saveConfig(map[string]any{"adminGroup": "media-admins"})
	// ryan signed in with his password: that proves nothing about single sign-on.
	st, out := e.call(e.admin, "PUT", "/api/auth/sign-in", map[string]any{"localSignIn": false})
	if st != http.StatusConflict || !strings.Contains(out["error"].(string), "lock yourself out") {
		t.Fatalf("turned password sign-in off from a password session: %d %v", st, out["_raw"])
	}
	cur := e.must(e.admin, "GET", "/api/auth/sign-in", nil, http.StatusOK)
	if cur["localSignIn"] != true || cur["canTurnOffLocal"] != false || cur["sessionMethod"] != "password" {
		t.Errorf("state = %v", cur)
	}
	// An API key can't do it either.
	key := e.must(e.admin, "POST", "/api/auth/api-keys", map[string]any{"name": "k"}, http.StatusCreated)["key"].(string)
	if got, _ := e.call(e.client(), "PUT", "/api/auth/sign-in", map[string]any{"localSignIn": false}, "X-API-Key", key); got != http.StatusForbidden {
		t.Errorf("API key: %d", got)
	}

	// An administrator who came in through single sign-on can.
	sso := e.client()
	e.browse(sso, "")
	if _, role := e.signedInAs(sso); role != "admin" {
		t.Fatal("the SSO account should be an administrator through the group")
	}
	e.must(sso, "PUT", "/api/auth/sign-in", map[string]any{"localSignIn": false}, http.StatusOK)
	_, status, out := e.login("ryan", adminPassword)
	if status != http.StatusForbidden || !strings.Contains(out["_raw"].(string), "turned off") {
		t.Errorf("password sign-in while off: %d %v", status, out["_raw"])
	}
	if m := e.must(e.client(), "GET", "/api/auth/methods", nil, http.StatusOK); m["local"] != false {
		t.Errorf("the page still offers the password form: %v", m)
	}
	// Single sign-on keeps working, and so does the open password session.
	if u, _ := e.signedInAs(e.admin); u != "ryan" {
		t.Errorf("the open session broke: %q", u)
	}
	e.browse(e.client(), "")

	// Switching single sign-on off puts the password form back by itself.
	e.saveConfig(map[string]any{"enabled": false})
	if _, status, _ := e.login("ryan", adminPassword); status != http.StatusOK {
		t.Errorf("password sign-in after single sign-on was switched off: %d", status)
	}
	e.saveConfig(map[string]any{"adminGroup": "media-admins"})
	if _, status, _ := e.login("ryan", adminPassword); status != http.StatusForbidden {
		t.Errorf("password sign-in is on again after SSO was switched back on: %d", status)
	}

	// The emergency switch in the environment: password sign-in is back, SSO is off.
	rescued := e.withConfig(func(c *config.Config) { c.DisableSSO = true })
	if _, status, _ := rescued.login("ryan", adminPassword); status != http.StatusOK {
		t.Errorf("password sign-in with MEDIARIUM_DISABLE_SSO: %d", status)
	}
	m := rescued.must(rescued.client(), "GET", "/api/auth/methods", nil, http.StatusOK)
	if m["local"] != true || m["oidc"] != nil {
		t.Errorf("methods with the switch set: %v", m)
	}
	resp, _ := rescued.client().Get(rescued.base + "/api/auth/oidc/start")
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/?signin_error=off" {
		t.Errorf("start with the switch set: %q", loc)
	}
	// The saved setup is untouched: take the variable away and it applies again.
	if got := e.must(e.admin, "GET", "/api/auth/oidc", nil, http.StatusOK); got["enabled"] != true {
		t.Errorf("the saved setup was changed: %v", got["enabled"])
	}
	// The administrator's view says why.
	if st := rescued.must(rescued.admin, "GET", "/api/auth/sign-in", nil, http.StatusOK); st["ssoDisabledByEnv"] != true || st["localEffective"] != true {
		t.Errorf("sign-in state with the switch set: %v", st)
	}
}

func TestLinkingAnAccountToSingleSignOn(t *testing.T) {
	e := newOIDCEnv(t)
	id := e.addAccount("sam", "member")
	sam, _, _ := e.login("sam", "long-enough-password")
	// A stolen session alone can't start a link: the password is needed.
	e.must(sam, "POST", "/api/auth/oidc/link", nil, http.StatusBadRequest)
	e.must(sam, "POST", "/api/auth/oidc/link", map[string]any{"password": "not-the-password"}, http.StatusBadRequest)
	out := e.must(sam, "POST", "/api/auth/oidc/link", map[string]any{"password": "long-enough-password"}, http.StatusOK)
	authURL := out["url"].(string)
	state, code := e.idp.Authorize(t, authURL)
	// Back from the provider, the browser carries the binding cookie set by the link call.
	if loc := e.callback(sam, state, code); loc != "/settings/profile?linked=ok" {
		t.Fatalf("link ended at %q", loc)
	}
	// Signing in through the provider now lands on the same account.
	b := e.client()
	e.browse(b, "")
	if u, _ := e.signedInAs(b); u != "sam" {
		t.Errorf("signed in as %q", u)
	}
	if e.scalar(`SELECT COUNT(*) FROM user_identities WHERE user_id = `+itoa(id)) != "1" {
		t.Error("expected one link")
	}
	// A second link is refused, and so is the same provider account on another account.
	e.must(sam, "POST", "/api/auth/oidc/link", nil, http.StatusConflict)
	e.addAccount("eve", "member")
	eve, _, _ := e.login("eve", "long-enough-password")
	out = e.must(eve, "POST", "/api/auth/oidc/link", map[string]any{"password": "long-enough-password"}, http.StatusOK)
	state, code = e.idp.Authorize(t, out["url"].(string))
	if loc := e.callback(eve, state, code); loc != "/settings/profile?linked=taken" {
		t.Errorf("linking someone else's provider account ended at %q", loc)
	}
	// Linking is not possible with an API key, and the link can be removed.
	key := e.must(sam, "POST", "/api/auth/api-keys", map[string]any{"name": "k"}, http.StatusCreated)["key"].(string)
	if got, _ := e.call(e.client(), "POST", "/api/auth/oidc/link", nil, "X-API-Key", key); got != http.StatusForbidden {
		t.Errorf("API key: %d", got)
	}
	e.must(sam, "DELETE", "/api/auth/oidc/link", nil, http.StatusOK)
	if e.scalar(`SELECT COUNT(*) FROM user_identities WHERE user_id = `+itoa(id)) != "0" {
		t.Error("the link is still there")
	}
	// An account made through the provider can't remove its only way in.
	b2 := e.client()
	e.idp.Mutate = func(c map[string]any) { c["sub"] = "newcomer"; c["preferred_username"] = "newcomer" }
	e.browse(b2, "")
	e.must(b2, "DELETE", "/api/auth/oidc/link", nil, http.StatusConflict)
}

// authentik reports its issuer with a closing slash. It has to be kept as it is.
func TestOIDCIssuerWithAClosingSlash(t *testing.T) {
	e := newOIDCEnv(t)
	e.idp.TrailingSlash = true
	// The old setup (no slash) no longer matches what the provider reports.
	status, _ := e.call(e.admin, "PUT", "/api/auth/oidc", e.config(map[string]any{"issuer": e.idp.URL}))
	if status != http.StatusBadRequest {
		t.Fatalf("an issuer without the slash was accepted: %d", status)
	}
	e.saveConfig(map[string]any{"issuer": e.idp.URL + "/"})
	got := e.must(e.admin, "GET", "/api/auth/oidc", nil, http.StatusOK)
	if got["issuer"] != e.idp.URL+"/" {
		t.Errorf("the saved issuer = %v", got["issuer"])
	}
	b := e.client()
	if loc := e.browse(b, ""); loc != "/" {
		t.Fatalf("sign-in sent to %q", loc)
	}
	if u, _ := e.signedInAs(b); u != "sam" {
		t.Errorf("signed in as %q", u)
	}
}

func TestSameUserNameFromTheProviderDoesNotTakeOverAnAccount(t *testing.T) {
	e := newOIDCEnv(t)
	// A local account called "sam" exists; the provider calls somebody "sam" too.
	e.addAccount("sam", "member")
	b := e.client()
	e.browse(b, "")
	u, _ := e.signedInAs(b)
	if u == "sam" {
		t.Fatal("the provider's sam was signed in as the local sam")
	}
	if u != "sam-2" {
		t.Errorf("new account name = %q, want sam-2", u)
	}
	// The local one is untouched.
	if _, status, _ := e.login("sam", "long-enough-password"); status != http.StatusOK {
		t.Errorf("the local account broke: %d", status)
	}
}

func TestTwoProvidersWithTheSameSubjectAreDifferentPeople(t *testing.T) {
	e := newOIDCEnv(t)
	b := e.client()
	e.browse(b, "")
	// The administrator moves to another provider whose ids start over: "user-123" is someone else there.
	second := oidctest.New(t)
	e.saveConfig(map[string]any{"issuer": second.URL, "clientId": second.ClientID, "clientSecret": second.Secret})
	e.idp = second
	e.idp.Mutate = func(c map[string]any) { c["preferred_username"] = "mallory"; c["email"] = "mallory@example.com" }
	b2 := e.client()
	e.browse(b2, "")
	if u, _ := e.signedInAs(b2); u != "mallory" {
		t.Errorf("the second provider's person was signed in as %q", u)
	}
}
