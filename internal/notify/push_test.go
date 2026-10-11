package notify

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// browser is the receiving side of a push subscription, as a browser keeps
// it: its key pair and auth secret.
type browser struct {
	key  *ecdh.PrivateKey
	auth []byte
}

func newBrowser(t *testing.T) browser {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)
	return browser{key: k, auth: auth}
}

func (b browser) subscription(endpoint string) PushSubscription {
	var s PushSubscription
	s.Endpoint = endpoint
	s.Keys.P256dh = base64.RawURLEncoding.EncodeToString(b.key.PublicKey().Bytes())
	s.Keys.Auth = base64.RawURLEncoding.EncodeToString(b.auth)
	return s
}

// decrypt undoes RFC 8291 the way a browser does.
func (b browser) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	asPubBytes := body[21 : 21+idlen]
	ct := body[21+idlen:]
	if rs != 4096 || idlen != 65 {
		t.Fatalf("header: rs %d idlen %d", rs, idlen)
	}
	asPub, err := ecdh.P256().NewPublicKey(asPubBytes)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := b.key.ECDH(asPub)
	if err != nil {
		t.Fatal(err)
	}
	prkKey := hmacSHA256(b.auth, shared)
	info := append(append([]byte("WebPush: info\x00"), b.key.PublicKey().Bytes()...), asPubBytes...)
	ikm := hmacSHA256(prkKey, info, []byte{1})
	prk := hmacSHA256(salt, ikm)
	cek := hmacSHA256(prk, []byte("Content-Encoding: aes128gcm\x00"), []byte{1})[:16]
	nonce := hmacSHA256(prk, []byte("Content-Encoding: nonce\x00"), []byte{1})[:12]
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain[len(plain)-1] != 2 {
		t.Fatalf("record delimiter %d", plain[len(plain)-1])
	}
	return plain[:len(plain)-1]
}

// RFC 8291 section 5 example: fixed keys and salt give a known message.
func TestPushEncryptionMatchesRFC8291(t *testing.T) {
	d := func(s string) []byte { b, _ := base64.RawURLEncoding.DecodeString(s); return b }
	uaPriv, err := ecdh.P256().NewPrivateKey(d("q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"))
	if err != nil {
		t.Fatal(err)
	}
	asPriv, err := ecdh.P256().NewPrivateKey(d("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := sealPush([]byte("When I grow up, I want to be a watermelon"), uaPriv.PublicKey(), asPriv, d("BTBZMqHH6r4Tts7J_aSIgg"), d("DGv6ra1nlYgDCS1FRnbzlw"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if base64.RawURLEncoding.EncodeToString(got) != want {
		t.Fatalf("encrypted message differs from the RFC example:\n got %s\nwant %s", base64.RawURLEncoding.EncodeToString(got), want)
	}
}

func TestPushSenderDeliversAnEncryptedSignedMessage(t *testing.T) {
	b := newBrowser(t)
	key, err := NewVAPID()
	if err != nil {
		t.Fatal(err)
	}
	vapid := &VAPID{Private: key, Subject: "https://github.com/rdborg/Mediarium"}
	var got pushPayload
	var auth, encoding, urgency string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, encoding, urgency = r.Header.Get("Authorization"), r.Header.Get("Content-Encoding"), r.Header.Get("Urgency")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b.decrypt(t, body), &got)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	s := &PushSender{Sub: b.subscription(srv.URL + "/push/abc"), VAPID: vapid,
		Client: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
	ev := Event{Type: "failed", Title: "Download failed: Night Harbour (2021)", Message: "plain", Lead: "The release was incomplete.",
		Details: []Detail{{Label: "Quality", Value: "WEBDL-1080p"}}, LinkURL: "https://mediarium.example.com/title/701"}
	if err := s.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if got.Title != ev.Title || !strings.Contains(got.Body, "Quality: WEBDL-1080p") || got.URL != "/title/701" {
		t.Fatalf("payload: %+v", got)
	}
	if encoding != "aes128gcm" || urgency != "high" {
		t.Fatalf("headers: %q %q", encoding, urgency)
	}

	// The VAPID token: signed by the server's key, for the push service's origin.
	parts := strings.SplitN(strings.TrimPrefix(auth, "vapid t="), ", k=", 2)
	if len(parts) != 2 || parts[1] != vapid.PublicKey() {
		t.Fatalf("authorization: %q", auth)
	}
	jwt := strings.Split(parts[0], ".")
	claims, _ := base64.RawURLEncoding.DecodeString(jwt[1])
	var c map[string]any
	_ = json.Unmarshal(claims, &c)
	if c["aud"] != srv.URL || c["sub"] != vapid.Subject || c["exp"].(float64) < float64(time.Now().Unix()) {
		t.Fatalf("claims: %v", c)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(jwt[2])
	sum := sha256.Sum256([]byte(jwt[0] + "." + jwt[1]))
	r, sv := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(&key.PublicKey, sum[:], r, sv) {
		t.Fatal("the VAPID signature doesn't verify")
	}
}

func TestPushSenderGoneSubscription(t *testing.T) {
	b := newBrowser(t)
	key, _ := NewVAPID()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusGone) }))
	defer srv.Close()
	s := &PushSender{Sub: b.subscription(srv.URL), VAPID: &VAPID{Private: key, Subject: "mailto:a@b.c"},
		Client: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
	err := s.Send(context.Background(), Event{Type: "test", Title: "t", Message: "m"})
	if err == nil || !strings.Contains(err.Error(), "isn't subscribed any more") {
		t.Fatalf("gone: %v", err)
	}
}

func TestParsePushSubscriptionAndKeys(t *testing.T) {
	b := newBrowser(t)
	good, _ := json.Marshal(b.subscription("https://fcm.googleapis.com/fcm/send/abc"))
	if _, err := ParsePushSubscription(string(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "{}", `{"endpoint":"http://x/y","keys":{"p256dh":"a","auth":"b"}}`} {
		if _, err := ParsePushSubscription(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if err := Validate(string(TargetPush), map[string]string{"subscription": string(good)}); err != nil {
		t.Fatalf("validate: %v", err)
	}
	k, _ := NewVAPID()
	text, err := EncodeVAPID(k)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeVAPID(text)
	if err != nil || !back.Equal(k) {
		t.Fatalf("round trip: %v", err)
	}
	if pk, _ := base64.RawURLEncoding.DecodeString((VAPID{Private: k}).PublicKey()); len(pk) != 65 || pk[0] != 4 {
		t.Fatal("the public key must be an uncompressed P-256 point")
	}
}
