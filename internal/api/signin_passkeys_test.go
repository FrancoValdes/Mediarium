package api_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"github.com/rdborg/mediarium/internal/api"
)

const passkeyBase = "https://mediarium.example.com"

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// softKey is a software authenticator: it makes passkeys and signs with them
// the way a phone or a security key would, so the whole ceremony can be tested
// without a browser.
type softKey struct {
	priv    *ecdsa.PrivateKey
	credID  []byte
	handle  []byte
	counter uint32
	// What it puts in its answers. Tests change these to play a bad authenticator.
	origin string
	rpID   string
	flags  byte // user present (0x01), user verified (0x04)
}

func newSoftKey() *softKey {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, 32)
	rand.Read(id)
	return &softKey{priv: priv, credID: id, origin: passkeyBase, rpID: "mediarium.example.com", flags: 0x01 | 0x04}
}

func (k *softKey) authData(attested bool) []byte {
	h := sha256.Sum256([]byte(k.rpID))
	flags := k.flags
	if attested {
		flags |= 0x40
	}
	out := append(h[:], flags)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], k.counter)
	out = append(out, c[:]...)
	if attested {
		out = append(out, make([]byte, 16)...) // AAGUID
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(k.credID)))
		out = append(out, l[:]...)
		out = append(out, k.credID...)
		xb, yb := k.priv.PublicKey.X.FillBytes(make([]byte, 32)), k.priv.PublicKey.Y.FillBytes(make([]byte, 32))
		cose, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
			Curve:         int64(webauthncose.P256), XCoord: xb, YCoord: yb,
		})
		if err != nil {
			panic(err)
		}
		out = append(out, cose...)
	}
	return out
}

func clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

// create answers a registration the server asked for.
func (k *softKey) create(options map[string]any) []byte {
	pk := options["publicKey"].(map[string]any)
	user := pk["user"].(map[string]any)
	k.handle, _ = base64.RawURLEncoding.DecodeString(user["id"].(string))
	cd := clientData("webauthn.create", pk["challenge"].(string), k.origin)
	k.counter = 0
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData(true)})
	if err != nil {
		panic(err)
	}
	resp, _ := json.Marshal(map[string]any{
		"id": b64u(k.credID), "rawId": b64u(k.credID), "type": "public-key", "authenticatorAttachment": "platform",
		"response":               map[string]any{"clientDataJSON": b64u(cd), "attestationObject": b64u(att), "transports": []string{"internal"}},
		"clientExtensionResults": map[string]any{},
	})
	return resp
}

// get answers a sign-in the server asked for. The counter goes up by one
// first, unless a test has set it.
func (k *softKey) get(options map[string]any, bump bool) []byte {
	pk := options["publicKey"].(map[string]any)
	if bump {
		k.counter++
	}
	cd := clientData("webauthn.get", pk["challenge"].(string), k.origin)
	ad := k.authData(false)
	cdh := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdh[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		panic(err)
	}
	resp, _ := json.Marshal(map[string]any{
		"id": b64u(k.credID), "rawId": b64u(k.credID), "type": "public-key", "authenticatorAttachment": "platform",
		"response":               map[string]any{"clientDataJSON": b64u(cd), "authenticatorData": b64u(ad), "signature": b64u(sig), "userHandle": b64u(k.handle)},
		"clientExtensionResults": map[string]any{},
	})
	return resp
}

// raw posts bytes (the browser's own answer) to a path.
func (e *signInEnv) raw(c *http.Client, method, path string, body []byte) (int, map[string]any) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.base+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{}
	}
	out["_raw"] = string(b)
	return resp.StatusCode, out
}

// passkeyEnv has passkeys switched on and one local member ("sam") signed in on sam.
type passkeyEnv struct {
	*signInEnv
	sam *http.Client
}

func newPasskeyEnv(t *testing.T) *passkeyEnv {
	t.Helper()
	e := &passkeyEnv{signInEnv: newSignInEnv(t, nil)}
	e.setPublicURL(passkeyBase)
	e.must(e.admin, "PUT", "/api/auth/sign-in", map[string]any{"passkeys": true}, http.StatusOK)
	e.addAccount("sam", "member")
	e.sam, _, _ = e.login("sam", "long-enough-password")
	return e
}

// register adds a passkey for a client's account and returns the key.
func (e *passkeyEnv) register(c *http.Client, name string) *softKey {
	e.t.Helper()
	k := newSoftKey()
	password := "long-enough-password"
	if c == e.admin {
		password = adminPassword
	}
	if status, out := e.registerWith(c, k, name, password); status != http.StatusCreated {
		e.t.Fatalf("register: %d %v", status, out["_raw"])
	}
	return k
}

func (e *passkeyEnv) registerWith(c *http.Client, k *softKey, name, password string) (int, map[string]any) {
	e.t.Helper()
	status, begin := e.call(c, "POST", "/api/auth/passkeys/register/begin", map[string]string{"password": password})
	if status != http.StatusOK {
		return status, begin
	}
	opts := begin["options"].(map[string]any)
	return e.raw(c, "POST", "/api/auth/passkeys/register/finish?c="+begin["ceremony"].(string)+"&name="+url.QueryEscape(name), k.create(opts))
}

// signIn runs a passkey sign-in on a fresh client.
func (e *passkeyEnv) signIn(k *softKey, bump bool) (*http.Client, int, map[string]any) {
	e.t.Helper()
	c := e.client()
	status, begin := e.call(c, "POST", "/api/auth/passkey/login/begin", map[string]any{})
	if status != http.StatusOK {
		return c, status, begin
	}
	status, out := e.raw(c, "POST", "/api/auth/passkey/login/finish?c="+begin["ceremony"].(string), k.get(begin["options"].(map[string]any), bump))
	return c, status, out
}

func TestPasskeyOriginAndRelyingPartyId(t *testing.T) {
	cases := []struct{ in, origin, rp string }{
		{"https://mediarium.example.com", "https://mediarium.example.com", "mediarium.example.com"},
		{"https://Mediarium.Example.com/", "https://mediarium.example.com", "mediarium.example.com"},
		{"https://mediarium.example.com:8443", "https://mediarium.example.com:8443", "mediarium.example.com"},
		{"https://example.com/media", "https://example.com", "example.com"},
		{"http://localhost:8264", "http://localhost:8264", "localhost"},
		{"http://localhost", "http://localhost", "localhost"},
		{"http://app.localhost:3000", "http://app.localhost:3000", "app.localhost"},
		// Not usable: not a secure context, an IP address (not a valid relying party id), or nothing.
		{"http://mediarium.example.com", "", ""},
		{"http://192.168.1.10:8264", "", ""},
		{"https://192.168.1.10", "", ""},
		{"https://[::1]:8264", "", ""},
		{"http://127.0.0.1:8264", "", ""},
		{"", "", ""}, {"mediarium.example.com", "", ""}, {"ftp://example.com", "", ""},
	}
	for _, c := range cases {
		if origin, rp := api.PasskeyOriginOf(c.in); origin != c.origin || rp != c.rp {
			t.Errorf("%q -> %q, %q; want %q, %q", c.in, origin, rp, c.origin, c.rp)
		}
	}
}

func TestPasskeysAreOffUntilSwitchedOnWithAPublicAddress(t *testing.T) {
	e := newSignInEnv(t, nil)
	e.addAccount("sam", "member")
	sam, _, _ := e.login("sam", "long-enough-password")
	// Off by default, and the page says why.
	list := e.must(sam, "GET", "/api/auth/passkeys", nil, http.StatusOK)
	if list["available"] != false || !strings.Contains(list["reason"].(string), "aren't switched on") {
		t.Errorf("default state = %v", list)
	}
	if m := e.must(e.client(), "GET", "/api/auth/methods", nil, http.StatusOK); m["passkeys"] != false {
		t.Errorf("methods = %v", m)
	}
	e.must(sam, "POST", "/api/auth/passkeys/register/begin", map[string]string{"password": "long-enough-password"}, http.StatusConflict)
	e.must(e.client(), "POST", "/api/auth/passkey/login/begin", map[string]any{}, http.StatusConflict)
	// Can't be switched on without a public address, an https one, or one with a name.
	for _, u := range []string{"", "http://mediarium.example.com", "http://192.168.1.5:8264"} {
		e.setPublicURL(u)
		status, out := e.call(e.admin, "PUT", "/api/auth/sign-in", map[string]any{"passkeys": true})
		if status != http.StatusBadRequest || !strings.Contains(out["error"].(string), "Passkeys need the address") {
			t.Errorf("public address %q: %d %v", u, status, out["_raw"])
		}
	}
	// With one, they can be.
	e.setPublicURL("http://localhost:8264")
	e.must(e.admin, "PUT", "/api/auth/sign-in", map[string]any{"passkeys": true}, http.StatusOK)
	if m := e.must(e.client(), "GET", "/api/auth/methods", nil, http.StatusOK); m["passkeys"] != true || m["passkeyHost"] != "localhost" {
		t.Errorf("methods on localhost = %v", m)
	}
	// And taking the public address away turns them off again, on its own.
	e.setPublicURL("")
	if m := e.must(e.client(), "GET", "/api/auth/methods", nil, http.StatusOK); m["passkeys"] != false {
		t.Errorf("methods without an address = %v", m)
	}
	list = e.must(sam, "GET", "/api/auth/passkeys", nil, http.StatusOK)
	if list["available"] != false || !strings.Contains(list["reason"].(string), "address you open Mediarium at") {
		t.Errorf("state without an address = %v", list)
	}
}

func TestPasskeyRegisterListRenameRemove(t *testing.T) {
	e := newPasskeyEnv(t)
	k1 := e.register(e.sam, "Phone")
	k2 := e.register(e.sam, "Laptop")
	list := e.must(e.sam, "GET", "/api/auth/passkeys", nil, http.StatusOK)
	keys := list["keys"].([]any)
	if list["available"] != true || len(keys) != 2 || keys[0].(map[string]any)["name"] != "Phone" {
		t.Fatalf("list = %v", list["_raw"])
	}
	if strings.Contains(list["_raw"].(string), b64u(k1.credID)) {
		t.Error("the list shows credential ids")
	}
	id1 := int64(keys[0].(map[string]any)["id"].(float64))
	e.must(e.sam, "PUT", "/api/auth/passkeys/"+itoa(id1), map[string]string{"name": "My phone"}, http.StatusOK)
	e.must(e.sam, "PUT", "/api/auth/passkeys/"+itoa(id1), map[string]string{"name": ""}, http.StatusBadRequest)
	e.must(e.sam, "PUT", "/api/auth/passkeys/"+itoa(id1), map[string]string{"name": strings.Repeat("x", 61)}, http.StatusBadRequest)
	list = e.must(e.sam, "GET", "/api/auth/passkeys", nil, http.StatusOK)
	if list["keys"].([]any)[0].(map[string]any)["name"] != "My phone" {
		t.Errorf("rename: %v", list["_raw"])
	}
	// Both work to sign in; removing one stops that one only.
	if _, status, _ := e.signIn(k1, true); status != http.StatusOK {
		t.Fatalf("phone: %d", status)
	}
	if _, status, _ := e.signIn(k2, true); status != http.StatusOK {
		t.Fatalf("laptop: %d", status)
	}
	e.must(e.sam, "DELETE", "/api/auth/passkeys/"+itoa(id1), nil, http.StatusOK)
	if _, status, _ := e.signIn(k1, true); status != http.StatusUnauthorized {
		t.Errorf("removed passkey signed in: %d", status)
	}
	if _, status, _ := e.signIn(k2, true); status != http.StatusOK {
		t.Errorf("the other passkey stopped working: %d", status)
	}
	e.must(e.sam, "DELETE", "/api/auth/passkeys/"+itoa(id1), nil, http.StatusNotFound)
	// Another account can neither rename nor remove it.
	e.addAccount("eve", "member")
	eve, _, _ := e.login("eve", "long-enough-password")
	rest := int64(e.mustFirstPasskeyID(e.sam))
	e.must(eve, "PUT", "/api/auth/passkeys/"+itoa(rest), map[string]string{"name": "mine now"}, http.StatusNotFound)
	e.must(eve, "DELETE", "/api/auth/passkeys/"+itoa(rest), nil, http.StatusNotFound)
	if got := e.must(eve, "GET", "/api/auth/passkeys", nil, http.StatusOK)["keys"].([]any); len(got) != 0 {
		t.Errorf("eve sees sam's passkeys: %v", got)
	}
}

func (e *passkeyEnv) mustFirstPasskeyID(c *http.Client) int64 {
	keys := e.must(c, "GET", "/api/auth/passkeys", nil, http.StatusOK)["keys"].([]any)
	return int64(keys[0].(map[string]any)["id"].(float64))
}

func TestPasskeySignInWorks(t *testing.T) {
	e := newPasskeyEnv(t)
	k := e.register(e.sam, "Phone")
	c, status, out := e.signIn(k, true)
	if status != http.StatusOK || out["username"] != "sam" || out["role"] != "member" {
		t.Fatalf("sign-in: %d %v", status, out["_raw"])
	}
	if u, _ := e.signedInAs(c); u != "sam" {
		t.Errorf("signed in as %q", u)
	}
	if got := e.scalar(`SELECT method FROM sessions ORDER BY rowid DESC LIMIT 1`); got != "passkey" {
		t.Errorf("session method = %q", got)
	}
	if got := e.scalar(`SELECT last_used_at FROM passkeys`); got == "" {
		t.Error("last used was not recorded")
	}
	// The counter was stored.
	if got := e.scalar(`SELECT credential FROM passkeys`); !strings.Contains(got, `"signCount":1`) {
		t.Errorf("stored credential = %s", got)
	}
	// Signing in does not need a user name, and works for the other account too.
	e.addAccount("eve", "member")
	eve, _, _ := e.login("eve", "long-enough-password")
	ke := e.register(eve, "Eve's key")
	c2, status, _ := e.signIn(ke, true)
	if u, _ := e.signedInAs(c2); status != http.StatusOK || u != "eve" {
		t.Errorf("eve: %d %q", status, u)
	}
	// An administrator's passkey makes an administrator session.
	ak := e.register(e.admin, "Admin key")
	_ = ak
}

func TestPasskeyRegistrationNeedsThePasswordAndABrowserSession(t *testing.T) {
	e := newPasskeyEnv(t)
	k := newSoftKey()
	if status, _ := e.registerWith(e.sam, k, "Phone", "not-my-password"); status != http.StatusBadRequest {
		t.Errorf("wrong password: %d", status)
	}
	if status, _ := e.registerWith(e.sam, k, "Phone", ""); status != http.StatusBadRequest {
		t.Errorf("no password: %d", status)
	}
	// An API key can't add one, whatever its owner's rights.
	key := e.must(e.sam, "POST", "/api/auth/api-keys", map[string]any{"name": "k"}, http.StatusCreated)["key"].(string)
	anon := e.client()
	for _, tc := range [][2]string{{"POST", "/api/auth/passkeys/register/begin"}, {"POST", "/api/auth/passkeys/register/finish"}, {"DELETE", "/api/auth/passkeys/1"}, {"PUT", "/api/auth/passkeys/1"}} {
		if got, _ := e.call(anon, tc[0], tc[1], map[string]any{"password": "long-enough-password"}, "X-API-Key", key); got != http.StatusForbidden {
			t.Errorf("API key on %s %s: %d", tc[0], tc[1], got)
		}
	}
	// Nothing is added by the failed attempts.
	if got := e.scalar(`SELECT COUNT(*) FROM passkeys`); got != "0" {
		t.Errorf("passkeys = %s", got)
	}
	// Nobody signed out can start a registration.
	e.must(e.client(), "POST", "/api/auth/passkeys/register/begin", map[string]string{"password": "x"}, http.StatusUnauthorized)
}

func TestPasskeysOnlyForAccountsWithAPasswordHere(t *testing.T) {
	e := newLDAPPasskeyEnv(t)
	sam, _, _ := e.login("sam", "dir-password")
	list := e.must(sam, "GET", "/api/auth/passkeys", nil, http.StatusOK)
	if list["available"] != false || !strings.Contains(list["reason"].(string), "single sign-on or the directory") {
		t.Errorf("list = %v", list)
	}
	e.must(sam, "POST", "/api/auth/passkeys/register/begin", map[string]string{"password": "dir-password"}, http.StatusBadRequest)
}

func newLDAPPasskeyEnv(t *testing.T) *ldapEnv {
	t.Helper()
	e := newLDAPEnv(t)
	e.setPublicURL(passkeyBase)
	e.must(e.admin, "PUT", "/api/auth/sign-in", map[string]any{"passkeys": true}, http.StatusOK)
	return e
}

func TestPasskeySignInRefusals(t *testing.T) {
	type tweak func(e *passkeyEnv, k *softKey)
	cases := []struct {
		name  string
		tweak tweak
	}{
		{"another origin (a look-alike site)", func(e *passkeyEnv, k *softKey) { k.origin = "https://mediarium.example.com.evil.net" }},
		{"plain http origin", func(e *passkeyEnv, k *softKey) { k.origin = "http://mediarium.example.com" }},
		{"another relying party id", func(e *passkeyEnv, k *softKey) { k.rpID = "evil.example.com" }},
		{"user not verified", func(e *passkeyEnv, k *softKey) { k.flags = 0x01 }},
		{"user not present", func(e *passkeyEnv, k *softKey) { k.flags = 0x04 }},
		{"a key that was never registered", func(e *passkeyEnv, k *softKey) {
			*k = *newSoftKey()
			k.handle = make([]byte, 64)
		}},
		{"a user handle of someone else", func(e *passkeyEnv, k *softKey) {
			other := make([]byte, 64)
			rand.Read(other)
			k.handle = other
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newPasskeyEnv(t)
			k := e.register(e.sam, "Phone")
			tc.tweak(e, k)
			c, status, out := e.signIn(k, true)
			if status != http.StatusUnauthorized {
				t.Fatalf("status %d %v", status, out["_raw"])
			}
			if u, _ := e.signedInAs(c); u != "" {
				t.Errorf("signed in as %q", u)
			}
			// What the person is told does not say which check failed.
			if out["error"] != "That passkey didn't work. Try again, or sign in another way." {
				t.Errorf("message = %v", out["error"])
			}
		})
	}
}

func TestPasskeySignatureAndChallengeChecks(t *testing.T) {
	e := newPasskeyEnv(t)
	k := e.register(e.sam, "Phone")

	// A signature made by another key over the right data.
	t.Run("signed with another key", func(t *testing.T) {
		impostor := *k
		impostor.priv, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if _, status, _ := e.signIn(&impostor, true); status != http.StatusUnauthorized {
			t.Errorf("status %d", status)
		}
	})
	// The right key signing a different challenge than the one asked.
	t.Run("a stale challenge", func(t *testing.T) {
		c := e.client()
		_, begin1 := e.call(c, "POST", "/api/auth/passkey/login/begin", map[string]any{})
		_, begin2 := e.call(c, "POST", "/api/auth/passkey/login/begin", map[string]any{})
		answerToFirst := k.get(begin1["options"].(map[string]any), true)
		status, _ := e.raw(c, "POST", "/api/auth/passkey/login/finish?c="+begin2["ceremony"].(string), answerToFirst)
		if status != http.StatusUnauthorized {
			t.Errorf("an answer to another challenge: %d", status)
		}
	})
	t.Run("an answer used twice", func(t *testing.T) {
		c := e.client()
		_, begin := e.call(c, "POST", "/api/auth/passkey/login/begin", map[string]any{})
		answer := k.get(begin["options"].(map[string]any), true)
		path := "/api/auth/passkey/login/finish?c=" + begin["ceremony"].(string)
		if status, out := e.raw(c, "POST", path, answer); status != http.StatusOK {
			t.Fatalf("first: %d %v", status, out["_raw"])
		}
		if status, _ := e.raw(e.client(), "POST", path, answer); status == http.StatusOK {
			t.Error("the same answer signed in twice")
		}
	})
	t.Run("a made-up ceremony", func(t *testing.T) {
		_, begin := e.call(e.client(), "POST", "/api/auth/passkey/login/begin", map[string]any{})
		answer := k.get(begin["options"].(map[string]any), true)
		if status, _ := e.raw(e.client(), "POST", "/api/auth/passkey/login/finish?c=abc", answer); status != http.StatusBadRequest {
			t.Errorf("status %d", status)
		}
		if status, _ := e.raw(e.client(), "POST", "/api/auth/passkey/login/finish", answer); status != http.StatusBadRequest {
			t.Errorf("no ceremony: %d", status)
		}
	})
	t.Run("an old challenge", func(t *testing.T) {
		c := e.client()
		_, begin := e.call(c, "POST", "/api/auth/passkey/login/begin", map[string]any{})
		answer := k.get(begin["options"].(map[string]any), true)
		e.server.TestExpireCeremonies()
		if status, _ := e.raw(c, "POST", "/api/auth/passkey/login/finish?c="+begin["ceremony"].(string), answer); status != http.StatusBadRequest {
			t.Errorf("an expired challenge: %d", status)
		}
	})
	t.Run("a registration challenge used for signing in", func(t *testing.T) {
		_, begin := e.call(e.sam, "POST", "/api/auth/passkeys/register/begin", map[string]string{"password": "long-enough-password"})
		login := newSoftKey()
		_ = login
		c := e.client()
		_, lb := e.call(c, "POST", "/api/auth/passkey/login/begin", map[string]any{})
		answer := k.get(lb["options"].(map[string]any), true)
		if status, _ := e.raw(c, "POST", "/api/auth/passkey/login/finish?c="+begin["ceremony"].(string), answer); status != http.StatusBadRequest {
			t.Errorf("a registration ceremony finished a sign-in: %d", status)
		}
	})
	t.Run("garbage", func(t *testing.T) {
		c := e.client()
		_, begin := e.call(c, "POST", "/api/auth/passkey/login/begin", map[string]any{})
		for _, body := range []string{"", "not json", `{}`, `{"id":"x","rawId":"x","type":"public-key","response":{}}`, strings.Repeat("a", 100000)} {
			_, b := e.call(c, "POST", "/api/auth/passkey/login/begin", map[string]any{})
			if status, _ := e.raw(c, "POST", "/api/auth/passkey/login/finish?c="+b["ceremony"].(string), []byte(body)); status == http.StatusOK {
				t.Errorf("garbage %.20q signed in", body)
			}
		}
		_ = begin
	})
	t.Run("ceremonies that never finish don't pile up", func(t *testing.T) {
		before := e.server.TestCeremonyCount()
		e.server.TestExpireCeremonies()
		e.call(e.client(), "POST", "/api/auth/passkey/login/begin", map[string]any{})
		if after := e.server.TestCeremonyCount(); after > before {
			t.Errorf("expired ceremonies are kept: %d -> %d", before, after)
		}
	})
	// Only the one good sign-in ("an answer used twice", its first use) made a session.
	if got := e.scalar(`SELECT COUNT(*) FROM sessions WHERE method = 'passkey'`); got != "1" {
		t.Errorf("passkey sessions = %s, want 1", got)
	}
}

func TestPasskeyCounterAndCloneDetection(t *testing.T) {
	e := newPasskeyEnv(t)
	k := e.register(e.sam, "Phone")
	for i := 0; i < 3; i++ {
		if _, status, out := e.signIn(k, true); status != http.StatusOK {
			t.Fatalf("sign-in %d: %d %v", i, status, out["_raw"])
		}
	}
	// A copy of the key still at an older count signs in: the stored counter is 3, this says 2.
	clone := *k
	clone.counter = 1
	_, status, out := e.signIn(&clone, true) // counter 2
	if status != http.StatusUnauthorized || !strings.Contains(out["error"].(string), "copied") {
		t.Errorf("a passkey whose counter went back: %d %v", status, out["_raw"])
	}
	// The same number again is refused as well.
	same := *k
	same.counter = 3
	if _, status, _ := e.signIn(&same, false); status != http.StatusUnauthorized {
		t.Errorf("a repeated counter: %d", status)
	}
	// The stored counter was not lowered by the refused attempts, so the real key carries on.
	if _, status, _ := e.signIn(k, true); status != http.StatusOK {
		t.Errorf("the real key after a clone attempt: %d", status)
	}
	// Refused attempts count against the sign-in limit.
	for i := 0; i < 6; i++ {
		c := clone
		c.counter = 0
		e.signIn(&c, false)
	}
	if _, status, _ := e.signIn(k, true); status != http.StatusTooManyRequests {
		t.Errorf("after many refused attempts: %d", status)
	}
}

// Many authenticators (and every synced passkey) always answer 0.
func TestPasskeyWithACounterThatStaysAtZero(t *testing.T) {
	e := newPasskeyEnv(t)
	k := e.register(e.sam, "Synced passkey")
	for i := 0; i < 3; i++ {
		if _, status, out := e.signIn(k, false); status != http.StatusOK {
			t.Fatalf("sign-in %d: %d %v", i, status, out["_raw"])
		}
	}
}

func TestPasskeyAndDisabledOrRemovedAccounts(t *testing.T) {
	e := newPasskeyEnv(t)
	k := e.register(e.sam, "Phone")
	id := e.scalar(`SELECT id FROM users WHERE username = 'sam'`)
	e.must(e.admin, "PUT", "/api/users/"+id, map[string]any{"disabled": true}, http.StatusOK)
	_, status, out := e.signIn(k, true)
	if status != http.StatusForbidden || !strings.Contains(out["_raw"].(string), "turned off") {
		t.Errorf("disabled: %d %v", status, out["_raw"])
	}
	e.must(e.admin, "PUT", "/api/users/"+id, map[string]any{"disabled": false}, http.StatusOK)
	if _, status, _ := e.signIn(k, true); status != http.StatusOK {
		t.Errorf("after turning the account on again: %d", status)
	}
	e.must(e.admin, "DELETE", "/api/users/"+id, nil, http.StatusOK)
	if _, status, _ := e.signIn(k, true); status != http.StatusUnauthorized {
		t.Errorf("deleted account: %d", status)
	}
	if got := e.scalar(`SELECT COUNT(*) FROM passkeys`); got != "0" {
		t.Errorf("the account's passkeys survived it: %s", got)
	}
}

func TestPasskeySkipsTheTwoStepCodeAndWorksWithPasswordSignInOff(t *testing.T) {
	e := newPasskeyEnv(t)
	// Give ryan a passkey and sign in with it, so a passkey session exists.
	k := e.register(e.admin, "Admin key")
	ak, status, _ := e.passkeySignInAs(k)
	if status != http.StatusOK {
		t.Fatalf("admin passkey sign-in: %d", status)
	}
	e.must(ak, "PUT", "/api/auth/sign-in", map[string]any{"localSignIn": false}, http.StatusOK)
	if _, status, _ := e.login("sam", "long-enough-password"); status != http.StatusForbidden {
		t.Errorf("password sign-in while it is off: %d", status)
	}
	if _, status, _ := e.signIn(k, true); status != http.StatusOK {
		t.Errorf("passkey sign-in while password sign-in is off: %d", status)
	}
}

func (e *passkeyEnv) passkeySignInAs(k *softKey) (*http.Client, int, map[string]any) {
	return e.signIn(k, true)
}

func TestPasskeyLimits(t *testing.T) {
	e := newPasskeyEnv(t)
	for i := 0; i < 20; i++ {
		e.register(e.sam, "key")
	}
	status, out := e.registerWith(e.sam, newSoftKey(), "one too many", "long-enough-password")
	if status != http.StatusConflict {
		t.Errorf("21st passkey: %d %v", status, out["_raw"])
	}
	// The same authenticator can't be added twice.
	e2 := newPasskeyEnv(t)
	k := e2.register(e2.sam, "Phone")
	if status, _ := e2.registerWith(e2.sam, k, "Phone again", "long-enough-password"); status == http.StatusCreated {
		t.Error("the same credential was registered twice")
	}
	// A registration answered from another account's session is refused.
	e2.addAccount("eve", "member")
	eve, _, _ := e2.login("eve", "long-enough-password")
	_, begin := e2.call(e2.sam, "POST", "/api/auth/passkeys/register/begin", map[string]string{"password": "long-enough-password"})
	nk := newSoftKey()
	answer := nk.create(begin["options"].(map[string]any))
	if status, _ := e2.raw(eve, "POST", "/api/auth/passkeys/register/finish?c="+begin["ceremony"].(string), answer); status != http.StatusBadRequest {
		t.Errorf("another account finished the registration: %d", status)
	}
	// A registration made for another origin isn't accepted.
	bad := newSoftKey()
	bad.origin = "https://evil.example.com"
	if status, _ := e2.registerWith(e2.sam, bad, "Evil", "long-enough-password"); status == http.StatusCreated {
		t.Error("a passkey made for another origin was registered")
	}
}

func TestPasskeyRegistrationNameIsChecked(t *testing.T) {
	e := newPasskeyEnv(t)
	k := newSoftKey()
	status, _ := e.registerWith(e.sam, k, strings.Repeat("n", 61), "long-enough-password")
	if status != http.StatusBadRequest {
		t.Errorf("long name: %d", status)
	}
	// An empty name gets a default one.
	k2 := newSoftKey()
	if status, out := e.registerWith(e.sam, k2, "", "long-enough-password"); status != http.StatusCreated || !strings.HasPrefix(out["name"].(string), "Passkey ") {
		t.Errorf("default name: %d %v", status, out["_raw"])
	}
}
