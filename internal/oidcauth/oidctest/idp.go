// Package oidctest is a small fake OpenID Connect provider for tests: discovery,
// a key set, a token endpoint that checks the code and the PKCE verifier, and a
// user info endpoint. Tests change what it signs through the exported fields.
// Nothing outside tests imports it.
package oidctest

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// IdP is a small OpenID Connect provider: discovery, keys, a token endpoint
// that checks the code and the PKCE verifier, and a user info endpoint. Tests
// change what it signs through the fields.
type IdP struct {
	t   *testing.T
	srv *httptest.Server
	// URL is the provider's address.
	URL string
	// TrailingSlash makes the provider report its issuer with a closing slash,
	// as authentik does.
	TrailingSlash bool
	key           *rsa.PrivateKey
	kid           string

	mu    sync.Mutex
	codes map[string]codeInfo
	// ClientID and Secret are what the provider accepts.
	ClientID string
	Secret   string

	// What the next ID token looks like. Tests set these before Finish.
	Mutate      func(claims map[string]any) // change the claims
	SignWith    *rsa.PrivateKey             // sign with another key
	Alg         string                      // "RS256" by default; "HS256" or "none" to test confusion
	OmitIDToken bool                        // answer without an id_token
	UserInfo    map[string]any              // what the user info endpoint says
	Issuer      string                      // the issuer to put in the token (the server's by default)
	tokenCalls  int
}

type codeInfo struct {
	challenge string
	nonce     string
	redirect  string
}

func New(t *testing.T) *IdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &IdP{t: t, key: key, kid: "k1", codes: map[string]codeInfo{}, ClientID: "mediarium", Secret: "s3cret"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.issuerValue(),
			"authorization_endpoint":                f.URL + "/authorize",
			"token_endpoint":                        f.URL + "/token",
			"userinfo_endpoint":                     f.URL + "/userinfo",
			"jwks_uri":                              f.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256", "HS256", "none"},
			"code_challenge_methods_supported":      []string{"S256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		pub := f.key.PublicKey
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": f.kid,
			"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "no token", 401)
			return
		}
		json.NewEncoder(w).Encode(f.UserInfo)
	})
	f.srv = httptest.NewServer(mux)
	f.URL = f.srv.URL
	t.Cleanup(f.srv.Close)
	return f
}

func (f *IdP) issuerValue() string {
	if f.TrailingSlash {
		return f.URL + "/"
	}
	return f.URL
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (f *IdP) token(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenCalls++
	_ = r.ParseForm()
	id, secret, _ := r.BasicAuth()
	if id == "" {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != f.ClientID || secret != f.Secret {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
		return
	}
	info, ok := f.codes[r.PostForm.Get("code")]
	delete(f.codes, r.PostForm.Get("code")) // a code works once
	if !ok || r.PostForm.Get("redirect_uri") != info.redirect {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if b64(sum[:]) != info.challenge {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "PKCE"})
		return
	}
	now := time.Now()
	issuer := f.Issuer
	if issuer == "" {
		issuer = f.issuerValue()
	}
	claims := map[string]any{
		"iss": issuer, "sub": "user-123", "aud": f.ClientID, "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
		"nonce": info.nonce, "preferred_username": "sam", "email": "sam@example.com", "email_verified": true,
		"name": "Sam Example", "groups": []string{"family", "media-admins"},
	}
	if f.Mutate != nil {
		f.Mutate(claims)
	}
	resp := map[string]any{"access_token": "at-123", "token_type": "Bearer", "expires_in": 3600}
	if !f.OmitIDToken {
		resp["id_token"] = f.sign(claims)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (f *IdP) sign(claims map[string]any) string {
	alg := f.Alg
	if alg == "" {
		alg = "RS256"
	}
	hdr, _ := json.Marshal(map[string]string{"alg": alg, "kid": f.kid, "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signing := b64(hdr) + "." + b64(body)
	switch alg {
	case "none":
		return signing + "."
	case "HS256":
		// A confusion attack: the "key" is the public key's bytes.
		mac := hmac.New(sha256.New, f.key.PublicKey.N.Bytes())
		mac.Write([]byte(signing))
		return signing + "." + b64(mac.Sum(nil))
	}
	key := f.key
	if f.SignWith != nil {
		key = f.SignWith
	}
	h := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return signing + "." + b64(sig)
}

// Authorize plays the browser: it reads the sign-in address and gets a code from the provider for it.
func (f *IdP) Authorize(t *testing.T, authURL string) (state, code string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if !strings.HasPrefix(authURL, f.URL+"/authorize") {
		t.Fatalf("sign-in address %q does not point at the provider", authURL)
	}
	for k, want := range map[string]string{"response_type": "code", "client_id": f.ClientID, "code_challenge_method": "S256"} {
		if q.Get(k) != want {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("sign-in address is missing something: %v", q)
	}
	code = "code-" + q.Get("state")
	f.mu.Lock()
	f.codes[code] = codeInfo{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirect: q.Get("redirect_uri")}
	f.mu.Unlock()
	return q.Get("state"), code
}

// Forge registers a code the provider will honour for a given PKCE challenge,
// nonce and redirect address, as if a browser had been sent there.
func (f *IdP) Forge(code, challenge, nonce, redirect string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codes[code] = codeInfo{challenge: challenge, nonce: nonce, redirect: redirect}
}

// B64 is base64url without padding, the encoding PKCE challenges use.
func B64(b []byte) string { return b64(b) }
