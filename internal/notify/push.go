package notify

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/rdborg/mediarium/internal/netguard"
)

// Push notifications to a browser or phone that added Mediarium (Web Push:
// RFC 8030 delivery, RFC 8291 message encryption, RFC 8292 VAPID). The
// browser hands over a subscription (an address at its push service and two
// keys); each message is encrypted for that browser alone and signed with
// this server's VAPID key, so the push service can't read it.

// TargetPush is a browser or phone that subscribed to push notifications.
const TargetPush TargetType = "push"

// PushSubscription is what the browser's PushManager.subscribe() returns.
type PushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// ParsePushSubscription reads and checks a subscription.
func ParsePushSubscription(raw string) (PushSubscription, error) {
	var sub PushSubscription
	if err := json.Unmarshal([]byte(raw), &sub); err != nil {
		return sub, errors.New("Allow notifications on this device first.")
	}
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return sub, errors.New("The browser gave a push address that isn't https. Try again in another browser.")
	}
	if k, err := b64(sub.Keys.P256dh); err != nil || len(k) != 65 {
		return sub, errors.New("The browser gave an unusable key. Allow notifications again.")
	}
	if a, err := b64(sub.Keys.Auth); err != nil || len(a) < 16 {
		return sub, errors.New("The browser gave an unusable key. Allow notifications again.")
	}
	return sub, nil
}

// b64 reads base64url with or without padding (browsers vary).
func b64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	return base64.RawURLEncoding.DecodeString(s)
}

// VAPID is this server's key pair for signing push requests.
type VAPID struct {
	Private *ecdsa.PrivateKey
	// Subject tells push services who sends (a mailto: or https: address).
	Subject string
}

// PublicKey is the public key in the form browsers want (base64url,
// uncompressed point), for PushManager.subscribe's applicationServerKey.
func (v VAPID) PublicKey() string {
	pub, err := v.Private.PublicKey.ECDH()
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(pub.Bytes())
}

// NewVAPID makes a new key pair.
func NewVAPID() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// EncodeVAPID and DecodeVAPID store a private key as text.
func EncodeVAPID(k *ecdsa.PrivateKey) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

func DecodeVAPID(s string) (*ecdsa.PrivateKey, error) {
	der, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok || ek.Curve != elliptic.P256() {
		return nil, errors.New("not a P-256 key")
	}
	return ek, nil
}

var currentVAPID atomic.Pointer[VAPID]

// SetVAPID gives push targets the server's key; until it's set they can't send.
func SetVAPID(v VAPID) { currentVAPID.Store(&v) }

// PushSender sends to one subscribed browser or phone.
type PushSender struct {
	Sub    PushSubscription
	VAPID  *VAPID
	Client *http.Client
}

func newPushSender(raw string) (*PushSender, error) {
	sub, err := ParsePushSubscription(raw)
	if err != nil {
		return nil, err
	}
	return &PushSender{Sub: sub, VAPID: currentVAPID.Load(), Client: pushHTTPClient()}, nil
}

// pushPayload is what the service worker (push-sw.js) shows.
type pushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

// maxPushPayload keeps the encrypted message under the 4 KB push services
// accept.
const maxPushPayload = 3000

// maxPushTitle is the most bytes of a title sent in a push message; phones
// show only one line of it anyway.
const maxPushTitle = 200

// cutText shortens s to at most max bytes without splitting a character.
func cutText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

func (s *PushSender) Send(ctx context.Context, ev Event) error {
	if s.VAPID == nil || s.VAPID.Private == nil {
		return errors.New("push notifications aren't set up on this server yet")
	}
	body := ev.Message
	if ev.rich() {
		var b strings.Builder
		b.WriteString(ev.Lead)
		for _, d := range ev.Details {
			b.WriteString("\n" + d.Label + ": " + d.Value)
		}
		body = b.String()
	}
	// With no link the browser's worker opens Mediarium's own start page; "/"
	// would be the domain root, another app when Mediarium runs under a URL base.
	link := ""
	if u, err := url.Parse(ev.LinkURL); err == nil && strings.HasPrefix(u.Path, "/") {
		link = u.Path
	}
	p := pushPayload{Title: cutText(ev.Title, maxPushTitle), Body: body, URL: link, Tag: ev.Type}
	raw, _ := json.Marshal(p)
	for len(raw) > maxPushPayload && len(p.Body) > 0 {
		p.Body = cutText(p.Body, len(p.Body)*3/4)
		raw, _ = json.Marshal(p)
	}
	enc, err := encryptPush(raw, s.Sub)
	if err != nil {
		return fmt.Errorf("encrypt the push message: %w", err)
	}
	jwt, err := vapidJWT(s.VAPID, s.Sub.Endpoint, time.Now())
	if err != nil {
		return fmt.Errorf("sign the push message: %w", err)
	}
	urgency := "normal"
	if priorityFor(ev) >= 4 {
		urgency = "high"
	}
	headers := map[string]string{
		"Content-Type":     "application/octet-stream",
		"Content-Encoding": "aes128gcm",
		"TTL":              "86400",
		"Urgency":          urgency,
		"Authorization":    "vapid t=" + jwt + ", k=" + s.VAPID.PublicKey(),
	}
	err = doJSONPostHeaders(ctx, s.Client, s.Sub.Endpoint, enc, headers)
	if err != nil && (strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "410")) {
		return errors.New("this device isn't subscribed any more (notifications were switched off there, or the app was removed). Remove it here and allow notifications on the device again")
	}
	return err
}

// vapidJWT is the signed token that says this server sends the message.
func vapidJWT(v *VAPID, endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(12 * time.Hour).Unix(),
		"sub": v.Subject,
	})
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, sgn, err := ecdsa.Sign(rand.Reader, v.Private, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sgn.FillBytes(sig[32:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// encryptPush encrypts plaintext for one subscription (RFC 8291, a single
// aes128gcm record).
func encryptPush(plaintext []byte, sub PushSubscription) ([]byte, error) {
	uaPub, err := b64(sub.Keys.P256dh)
	if err != nil {
		return nil, err
	}
	authSecret, err := b64(sub.Keys.Auth)
	if err != nil {
		return nil, err
	}
	curve := ecdh.P256()
	ua, err := curve.NewPublicKey(uaPub)
	if err != nil {
		return nil, fmt.Errorf("the browser's key: %w", err)
	}
	as, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return sealPush(plaintext, ua, as, authSecret, salt)
}

func hmacSHA256(key []byte, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

// sealPush does the encryption with given keys and salt (split out so tests
// can use fixed ones).
func sealPush(plaintext []byte, ua *ecdh.PublicKey, as *ecdh.PrivateKey, authSecret, salt []byte) ([]byte, error) {
	shared, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()
	// IKM = HKDF(auth secret, ECDH secret, "WebPush: info" || ua || as, 32)
	prkKey := hmacSHA256(authSecret, shared)
	info := append(append([]byte("WebPush: info\x00"), ua.Bytes()...), asPub...)
	ikm := hmacSHA256(prkKey, info, []byte{1})
	// CEK and nonce = HKDF(salt, IKM, ...)
	prk := hmacSHA256(salt, ikm)
	cek := hmacSHA256(prk, []byte("Content-Encoding: aes128gcm\x00"), []byte{1})[:16]
	nonce := hmacSHA256(prk, []byte("Content-Encoding: nonce\x00"), []byte{1})[:12]

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	record := append(append([]byte{}, plaintext...), 2) // 2: the last (only) record
	ct := gcm.Seal(nil, nonce, record, nil)

	header := make([]byte, 0, 16+4+1+len(asPub))
	header = append(header, salt...)
	rs := make([]byte, 4)
	binary.BigEndian.PutUint32(rs, 4096)
	header = append(header, rs...)
	header = append(header, byte(len(asPub)))
	header = append(header, asPub...)
	return append(header, ct...), nil
}

// pushHTTPClient is the client for a browser's push service. Push services
// are on the public internet, so private and loopback addresses are refused
// (a person can type any address in as a push endpoint), and a redirect is
// never followed.
func pushHTTPClient() *http.Client {
	c := netguard.PublicClient(10 * time.Second)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}
