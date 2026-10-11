// Package totp makes and checks the six-digit codes of authenticator apps
// (time-based one-time passwords, RFC 6238 with HMAC-SHA1 and 30-second
// steps: what Google Authenticator, Microsoft Authenticator, 1Password,
// Bitwarden and the rest use by default).
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	period = 30 // seconds per code
	digits = 6
	// skew is how many steps either side of now a code is still accepted,
	// for a phone clock that is a little off.
	skew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret makes a random 160-bit secret, in the base32 form apps accept.
func NewSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make a secret: %w", err)
	}
	return b32.EncodeToString(b), nil
}

// URI is the otpauth:// link an authenticator app scans (as a QR code) to
// add the account.
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(digits))
	q.Set("period", fmt.Sprint(period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func decodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "=", "").Replace(secret))
	key, err := b32.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("read the secret: %w", err)
	}
	return key, nil
}

// codeAt is the code for one time step.
func codeAt(key []byte, step uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], step)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", digits, n%1_000_000)
}

// Code is the code for secret at time t.
func Code(secret string, t time.Time) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	return codeAt(key, uint64(t.Unix())/period), nil
}

// Verify checks code against secret at time t, allowing one step of clock
// difference either way. A code is good only once: notAfter is the last step
// already used (0 for none), and steps up to it are refused. It answers the
// step that matched, to be remembered as the new notAfter.
func Verify(secret, code string, t time.Time, notAfter uint64) (bool, uint64) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != digits {
		return false, 0
	}
	key, err := decodeSecret(secret)
	if err != nil {
		return false, 0
	}
	now := uint64(t.Unix()) / period
	for d := -skew; d <= skew; d++ {
		step := uint64(int64(now) + int64(d))
		if step <= notAfter {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(codeAt(key, step)), []byte(code)) == 1 {
			return true, step
		}
	}
	return false, 0
}
