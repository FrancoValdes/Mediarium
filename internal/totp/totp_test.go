package totp

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// The SHA1 test values of RFC 6238 (secret "12345678901234567890"), cut to
// the last six digits as six-digit apps show them.
func TestRFC6238Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	tests := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, tt := range tests {
		got, err := Code(secret, time.Unix(tt.unix, 0))
		if err != nil || got != tt.want {
			t.Errorf("at %d: %q, %v; want %q", tt.unix, got, err, tt.want)
		}
	}
}

func TestVerify(t *testing.T) {
	secret, err := NewSecret()
	if err != nil || len(secret) != 32 {
		t.Fatalf("secret %q, %v", secret, err)
	}
	now := time.Unix(1_800_000_000, 0)
	code, _ := Code(secret, now)
	before, _ := Code(secret, now.Add(-30*time.Second))
	after, _ := Code(secret, now.Add(30*time.Second))
	old, _ := Code(secret, now.Add(-90*time.Second))
	step := uint64(now.Unix()) / 30

	tests := []struct {
		name     string
		code     string
		notAfter uint64
		ok       bool
	}{
		{"now", code, 0, true},
		{"with spaces", code[:3] + " " + code[3:], 0, true},
		{"a step behind (phone clock slow)", before, 0, true},
		{"a step ahead", after, 0, true},
		{"three steps old", old, 0, false},
		{"used already", code, step, false},
		{"wrong", "000000", 0, code == "000000"},
		{"too short", code[:5], 0, false},
		{"letters", "abcdef", 0, false},
	}
	for _, tt := range tests {
		ok, got := Verify(secret, tt.code, now, tt.notAfter)
		if ok != tt.ok {
			t.Errorf("%s: %v, want %v", tt.name, ok, tt.ok)
		}
		if ok && got == 0 {
			t.Errorf("%s: no step answered", tt.name)
		}
	}
	// Lower case and spaces in the secret, as people type it, still work.
	low := strings.ToLower(secret[:4] + " " + secret[4:])
	if ok, _ := Verify(low, code, now, 0); !ok {
		t.Error("a secret typed in lower case with spaces was refused")
	}
}

func TestURI(t *testing.T) {
	got := URI("Mediarium", "ryan smith", "JBSWY3DPEHPK3PXP")
	want := "otpauth://totp/Mediarium:ryan%20smith?algorithm=SHA1&digits=6&issuer=Mediarium&period=30&secret=JBSWY3DPEHPK3PXP"
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}
