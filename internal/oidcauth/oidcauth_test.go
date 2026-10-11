package oidcauth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/oidcauth"
	"github.com/rdborg/mediarium/internal/oidcauth/oidctest"
)

const redirect = "https://mediarium.example.com/api/auth/oidc/callback"

func newManager() *oidcauth.Manager { return oidcauth.New(&http.Client{Timeout: 10 * time.Second}) }

func config(f *oidctest.IdP) oidcauth.Config {
	return oidcauth.Config{Enabled: true, Name: "Test", Issuer: f.URL, ClientID: f.ClientID, ClientSecret: f.Secret}
}

func codeOf(err error) string {
	var e *oidcauth.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestSuccessfulSignIn(t *testing.T) {
	idp := oidctest.New(t)
	m := newManager()
	cfg := config(idp)
	cfg.AdminGroup = "media-admins"
	flow, err := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, "/movies")
	if err != nil {
		t.Fatal(err)
	}
	state, code := idp.Authorize(t, flow.URL)
	res, err := m.Finish(context.Background(), cfg, redirect, url.Values{"state": {state}, "code": {code}}, flow.Binding)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if res.Subject != "user-123" || res.Username != "sam" || res.Email != "sam@example.com" || !res.EmailVerified || res.Name != "Sam Example" {
		t.Errorf("result = %+v", res)
	}
	if !oidcauth.InGroup(res.Groups, "media-admins") || oidcauth.InGroup(res.Groups, "other") {
		t.Errorf("groups = %v", res.Groups)
	}
	if res.Next != "/movies" || res.Mode != oidcauth.ModeSignIn {
		t.Errorf("next/mode = %q %q", res.Next, res.Mode)
	}
	if m.Pending() != 0 {
		t.Errorf("a finished sign-in is still waiting")
	}
}

// Every way the callback can be wrong must be refused, and refused with the
// right short code.
func TestSignInFailures(t *testing.T) {
	type fix func(t *testing.T, idp *oidctest.IdP, q url.Values, binding *string, cfg *oidcauth.Config)
	cases := []struct {
		name string
		fix  fix
		want string
	}{
		{"wrong state", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			q.Set("state", "not-the-state")
		}, oidcauth.CodeState},
		{"no state", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) { q.Del("state") }, oidcauth.CodeState},
		{"another browser", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			*b = "someone-elses-cookie"
		}, oidcauth.CodeState},
		{"no cookie", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) { *b = "" }, oidcauth.CodeState},
		{"setup changed", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			c.ClientID = "other"
		}, oidcauth.CodeState},
		{"provider error", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			q.Set("error", "access_denied")
			q.Del("code")
		}, oidcauth.CodeProvider},
		{"no code", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) { q.Del("code") }, oidcauth.CodeProvider},
		{"wrong code", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			q.Set("code", "guess")
		}, oidcauth.CodeToken},
		{"wrong client secret", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			c.ClientSecret = "nope"
		}, oidcauth.CodeToken},
		{"no id token", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			idp.OmitIDToken = true
		}, oidcauth.CodeIDToken},
		{"wrong nonce", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			idp.Mutate = func(cl map[string]any) { cl["nonce"] = "replayed-from-another-sign-in" }
		}, oidcauth.CodeNonce},
		{"no nonce", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			idp.Mutate = func(cl map[string]any) { delete(cl, "nonce") }
		}, oidcauth.CodeNonce},
		{"wrong audience", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			idp.Mutate = func(cl map[string]any) { cl["aud"] = "some-other-app" }
		}, oidcauth.CodeIDToken},
		{"several audiences without azp", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			idp.Mutate = func(cl map[string]any) { cl["aud"] = []string{"mediarium", "other"} }
		}, oidcauth.CodeIDToken},
		{"authorized party is someone else", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			idp.Mutate = func(cl map[string]any) { cl["aud"] = []string{"mediarium", "other"}; cl["azp"] = "other" }
		}, oidcauth.CodeIDToken},
		{"expired", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			idp.Mutate = func(cl map[string]any) { cl["exp"] = time.Now().Add(-time.Minute).Unix() }
		}, oidcauth.CodeIDToken},
		{"not valid yet", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			idp.Mutate = func(cl map[string]any) { cl["nbf"] = time.Now().Add(time.Hour).Unix() }
		}, oidcauth.CodeIDToken},
		{"wrong issuer", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			idp.Issuer = "https://evil.example.com"
		}, oidcauth.CodeIDToken},
		{"signed with another key", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			other, _ := rsa.GenerateKey(rand.Reader, 2048)
			idp.SignWith = other
		}, oidcauth.CodeIDToken},
		{"alg none", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) { idp.Alg = "none" }, oidcauth.CodeIDToken},
		{"alg HS256 with the public key as secret", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) { idp.Alg = "HS256" }, oidcauth.CodeIDToken},
		{"no subject", func(t *testing.T, idp *oidctest.IdP, q url.Values, b *string, c *oidcauth.Config) {
			idp.Mutate = func(cl map[string]any) { delete(cl, "sub") }
		}, oidcauth.CodeClaims},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idp := oidctest.New(t)
			m := newManager()
			cfg := config(idp)
			flow, err := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			state, code := idp.Authorize(t, flow.URL)
			q := url.Values{"state": {state}, "code": {code}}
			binding := flow.Binding
			tc.fix(t, idp, q, &binding, &cfg)
			res, err := m.Finish(context.Background(), cfg, redirect, q, binding)
			if err == nil {
				t.Fatalf("signed in anyway: %+v", res)
			}
			if got := codeOf(err); got != tc.want {
				t.Errorf("code = %q (%v), want %q", got, err, tc.want)
			}
			if res != nil {
				t.Errorf("a result came back with the error: %+v", res)
			}
		})
	}
}

func TestStateWorksOnce(t *testing.T) {
	idp := oidctest.New(t)
	m := newManager()
	cfg := config(idp)
	flow, _ := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, "")
	state, code := idp.Authorize(t, flow.URL)
	q := url.Values{"state": {state}, "code": {code}}
	if _, err := m.Finish(context.Background(), cfg, redirect, q, flow.Binding); err != nil {
		t.Fatal(err)
	}
	// Played again, with everything else right, the same answer is refused.
	_, code2 := idp.Authorize(t, flow.URL)
	q.Set("code", code2)
	if _, err := m.Finish(context.Background(), cfg, redirect, q, flow.Binding); codeOf(err) != oidcauth.CodeState {
		t.Errorf("replayed state: %v", err)
	}
}

func TestFailedAttemptStillUsesUpTheState(t *testing.T) {
	idp := oidctest.New(t)
	m := newManager()
	cfg := config(idp)
	flow, _ := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, "")
	state, code := idp.Authorize(t, flow.URL)
	// Someone tries the right state from the wrong browser; the real one is too late.
	if _, err := m.Finish(context.Background(), cfg, redirect, url.Values{"state": {state}, "code": {code}}, "stolen"); err == nil {
		t.Fatal("accepted another browser")
	}
	if _, err := m.Finish(context.Background(), cfg, redirect, url.Values{"state": {state}, "code": {code}}, flow.Binding); codeOf(err) != oidcauth.CodeState {
		t.Errorf("state survived a failed attempt: %v", err)
	}
}

func TestStateExpires(t *testing.T) {
	idp := oidctest.New(t)
	m := newManager()
	cfg := config(idp)
	flow, _ := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, "")
	state, code := idp.Authorize(t, flow.URL)
	oidcauth.SetClockForTest(m, func() time.Time { return time.Now().Add(oidcauth.StateTTL + time.Second) })
	if _, err := m.Finish(context.Background(), cfg, redirect, url.Values{"state": {state}, "code": {code}}, flow.Binding); codeOf(err) != oidcauth.CodeState {
		t.Errorf("an old sign-in was accepted: %v", err)
	}
}

func TestPKCEIsSent(t *testing.T) {
	// The provider refuses a code whose verifier does not match the challenge;
	// asking for a code with a different challenge than the verifier we hold
	// must fail at the token step.
	idp := oidctest.New(t)
	m := newManager()
	cfg := config(idp)
	flow, _ := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, "")
	state, _ := idp.Authorize(t, flow.URL)
	idp.Forge("forged", oidctest.B64([]byte("not-our-challenge")), "x", redirect)
	if _, err := m.Finish(context.Background(), cfg, redirect, url.Values{"state": {state}, "code": {"forged"}}, flow.Binding); codeOf(err) != oidcauth.CodeToken {
		t.Errorf("a code bound to another challenge worked: %v", err)
	}
}

func TestClaimsFromUserInfo(t *testing.T) {
	idp := oidctest.New(t)
	idp.Mutate = func(cl map[string]any) { delete(cl, "groups"); delete(cl, "preferred_username") }
	idp.UserInfo = map[string]any{"sub": "user-123", "groups": []string{"media-admins"}, "preferred_username": "sam-from-userinfo"}
	m := newManager()
	cfg := config(idp)
	cfg.AdminGroup = "media-admins"
	flow, _ := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, "")
	state, code := idp.Authorize(t, flow.URL)
	res, err := m.Finish(context.Background(), cfg, redirect, url.Values{"state": {state}, "code": {code}}, flow.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if !oidcauth.InGroup(res.Groups, "media-admins") || res.Username != "sam-from-userinfo" {
		t.Errorf("claims from user info missing: %+v", res)
	}
}

func TestUserInfoAboutSomeoneElseIsIgnored(t *testing.T) {
	idp := oidctest.New(t)
	idp.Mutate = func(cl map[string]any) { delete(cl, "groups") }
	idp.UserInfo = map[string]any{"sub": "another-person", "groups": []string{"media-admins"}}
	m := newManager()
	cfg := config(idp)
	cfg.AdminGroup = "media-admins"
	flow, _ := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, "")
	state, code := idp.Authorize(t, flow.URL)
	res, err := m.Finish(context.Background(), cfg, redirect, url.Values{"state": {state}, "code": {code}}, flow.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if oidcauth.InGroup(res.Groups, "media-admins") {
		t.Errorf("believed user info about another person: %+v", res)
	}
}

func TestEmailVerifiedAsText(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want bool
	}{{true, true}, {false, false}, {"true", true}, {"True", true}, {"false", false}, {"yes", false}, {1, false}, {nil, false}} {
		idp := oidctest.New(t)
		idp.Mutate = func(cl map[string]any) { cl["email_verified"] = tc.v }
		m := newManager()
		cfg := config(idp)
		flow, _ := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, "")
		state, code := idp.Authorize(t, flow.URL)
		res, err := m.Finish(context.Background(), cfg, redirect, url.Values{"state": {state}, "code": {code}}, flow.Binding)
		if err != nil {
			t.Fatal(err)
		}
		if res.EmailVerified != tc.want {
			t.Errorf("email_verified %#v -> %v, want %v", tc.v, res.EmailVerified, tc.want)
		}
	}
}

func TestBeginRefusesBadSetupAndUnreachableProvider(t *testing.T) {
	m := newManager()
	if _, err := m.Begin(context.Background(), oidcauth.Config{Issuer: "https://auth.example.com"}, redirect, oidcauth.ModeSignIn, 0, ""); err == nil {
		t.Error("began without a client id")
	}
	cfg := oidcauth.Config{Issuer: "http://127.0.0.1:1", ClientID: "a", ClientSecret: "b"}
	if _, err := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, ""); codeOf(err) != oidcauth.CodeProvider {
		t.Errorf("unreachable provider: %v", err)
	}
}

func TestDiscoveryWithAnotherIssuerIsRefused(t *testing.T) {
	// The provider answers for itself, but we were told to trust a different issuer address.
	idp := oidctest.New(t)
	m := newManager()
	cfg := config(idp)
	cfg.Issuer = idp.URL + "/" // a slash makes it a different issuer string than the one it reports
	if _, err := m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, ""); err != nil {
		// go-oidc trims the trailing slash for the well-known address but
		// compares the issuer exactly, so this must fail.
		if codeOf(err) != oidcauth.CodeProvider {
			t.Errorf("code = %q", codeOf(err))
		}
		return
	}
	t.Error("accepted a provider whose reported issuer differs from the configured one")
}

func TestTooManyStartedSignIns(t *testing.T) {
	idp := oidctest.New(t)
	m := newManager()
	cfg := config(idp)
	var err error
	for i := 0; i < 1100 && err == nil; i++ {
		_, err = m.Begin(context.Background(), cfg, redirect, oidcauth.ModeSignIn, 0, "")
	}
	if err == nil {
		t.Fatal("no limit on waiting sign-ins")
	}
	if m.Pending() > 1000 {
		t.Errorf("pending = %d", m.Pending())
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                             "/",
		"/":                            "/",
		"/movies":                      "/movies",
		"/movies?x=1#a":                "/movies?x=1#a",
		"//evil.example.com":           "/",
		"/\\evil.example.com":          "/",
		"https://evil.example.com":     "/",
		"javascript:alert(1)":          "/",
		"evil.example.com":             "/",
		"/ok\r\nSet-Cookie: a=b":       "/",
		"/a\\b":                        "/",
		"/" + strings.Repeat("a", 600): "/",
	}
	for in, want := range cases {
		if got := oidcauth.SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmailAllowed(t *testing.T) {
	cases := []struct {
		domains, email string
		want           bool
	}{
		{"", "a@anything.org", true},
		{"example.com", "a@example.com", true},
		{"example.com", "a@EXAMPLE.com", true},
		{"example.com, @other.org", "a@other.org", true},
		{"example.com", "a@evil.example.com.attacker.net", false},
		{"example.com", "a@notexample.com", false},
		{"example.com", "a@example.com@evil.net", false},
		{"example.com", "", false},
		{"example.com", "no-at-sign", false},
	}
	for _, c := range cases {
		if got := (oidcauth.Config{AllowedDomains: c.domains}).EmailAllowed(c.email); got != c.want {
			t.Errorf("EmailAllowed(%q in %q) = %v, want %v", c.email, c.domains, got, c.want)
		}
	}
}

func TestRedirectURL(t *testing.T) {
	good := map[string]string{
		"https://mediarium.example.com":      "https://mediarium.example.com/api/auth/oidc/callback",
		"https://mediarium.example.com/":     "https://mediarium.example.com/api/auth/oidc/callback",
		"http://localhost:8264":              "http://localhost:8264/api/auth/oidc/callback",
		"https://example.com/media?x=1#frag": "https://example.com/media/api/auth/oidc/callback",
	}
	for in, want := range good {
		if got, err := oidcauth.RedirectURL(in); err != nil || got != want {
			t.Errorf("RedirectURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "mediarium.example.com", "ftp://x.example.com", "javascript:alert(1)", "https://"} {
		if _, err := oidcauth.RedirectURL(in); err == nil {
			t.Errorf("RedirectURL(%q) was accepted", in)
		}
	}
}

func TestConfigCheck(t *testing.T) {
	ok := oidcauth.Config{Issuer: "https://auth.example.com", ClientID: "a", ClientSecret: "b"}
	if err := ok.Check(); err != nil {
		t.Fatal(err)
	}
	bad := []oidcauth.Config{
		{ClientID: "a", ClientSecret: "b"},
		{Issuer: "auth.example.com", ClientID: "a", ClientSecret: "b"},
		{Issuer: "https://user:pw@auth.example.com", ClientID: "a", ClientSecret: "b"},
		{Issuer: "https://auth.example.com?x=1", ClientID: "a", ClientSecret: "b"},
		{Issuer: "https://auth.example.com", ClientSecret: "b"},
		{Issuer: "https://auth.example.com", ClientID: "a"},
	}
	for i, c := range bad {
		if c.Check() == nil {
			t.Errorf("case %d accepted: %+v", i, c)
		}
	}
	scopes := (oidcauth.Config{Scopes: "profile email profile groups"}).ScopeList()
	if strings.Join(scopes, " ") != "openid profile email groups" {
		t.Errorf("scopes = %v", scopes)
	}
}

func TestDiscover(t *testing.T) {
	idp := oidctest.New(t)
	ep, err := newManager().Discover(context.Background(), idp.URL)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Authorization != idp.URL+"/authorize" || ep.Token != idp.URL+"/token" || len(ep.PKCE) != 1 {
		t.Errorf("endpoints = %+v", ep)
	}
}
