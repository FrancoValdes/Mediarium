package cloudbackup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func init() { scryptN = 1 << 10 } // fast in tests

func TestLockRoundTrip(t *testing.T) {
	sizes := []int{0, 1, 1000, chunkSize - 1, chunkSize, chunkSize + 1, 3 * chunkSize, 3*chunkSize + 17}
	for _, n := range sizes {
		plain := make([]byte, n)
		rand.Read(plain)
		var locked bytes.Buffer
		if err := Lock(&locked, bytes.NewReader(plain), "correct horse"); err != nil {
			t.Fatalf("%d bytes: lock: %v", n, err)
		}
		if !IsLocked(locked.Bytes()) {
			t.Fatalf("%d bytes: not marked as locked", n)
		}
		if n > 16 && bytes.Contains(locked.Bytes(), plain[:16]) {
			t.Fatalf("%d bytes: the plain data shows through", n)
		}
		var out bytes.Buffer
		if err := Unlock(&out, bytes.NewReader(locked.Bytes()), "correct horse"); err != nil {
			t.Fatalf("%d bytes: unlock: %v", n, err)
		}
		if !bytes.Equal(out.Bytes(), plain) {
			t.Fatalf("%d bytes: came back different", n)
		}
	}
}

func TestUnlockRefusesWhatIsWrong(t *testing.T) {
	plain := make([]byte, 2*chunkSize+100)
	rand.Read(plain)
	var locked bytes.Buffer
	if err := Lock(&locked, bytes.NewReader(plain), "secret"); err != nil {
		t.Fatal(err)
	}
	data := locked.Bytes()
	flipped := append([]byte(nil), data...)
	flipped[len(flipped)/2] ^= 1
	// Dropping the last chunk entirely (4-byte size + 100 + 16 bytes of tag).
	cut := data[:len(data)-(4+100+16)]

	tests := []struct {
		name       string
		data       []byte
		passphrase string
		want       error
	}{
		{"wrong passphrase", data, "Secret", ErrWrongPassphrase},
		{"a changed byte", flipped, "secret", ErrWrongPassphrase},
		{"cut short at a chunk boundary", cut, "secret", ErrWrongPassphrase},
		{"cut in the middle", data[:len(data)-50], "secret", ErrWrongPassphrase},
		{"extra data at the end", append(append([]byte(nil), data...), 0), "secret", ErrWrongPassphrase},
		{"not locked at all", []byte("PK\x03\x04 a plain zip"), "secret", ErrNotLocked},
	}
	for _, tt := range tests {
		if err := Unlock(&bytes.Buffer{}, bytes.NewReader(tt.data), tt.passphrase); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.want)
		}
	}
	if err := Lock(&bytes.Buffer{}, bytes.NewReader(plain), ""); err == nil {
		t.Error("locked with an empty passphrase")
	}
}
