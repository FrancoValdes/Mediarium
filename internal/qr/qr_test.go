package qr

import (
	"strings"
	"testing"
)

func TestEncodeSizes(t *testing.T) {
	tests := []struct {
		text string
		size int
	}{
		{"hi", 21},                     // version 1
		{strings.Repeat("a", 14), 21},  // the most version 1-M holds
		{strings.Repeat("a", 15), 25},  // one more: version 2
		{strings.Repeat("a", 106), 41}, // version 6
		{strings.Repeat("a", 107), 45}, // version 7, with version information
		{strings.Repeat("a", 213), 57}, // version 10
	}
	for _, tt := range tests {
		c, err := Encode([]byte(tt.text))
		if err != nil {
			t.Fatalf("%d bytes: %v", len(tt.text), err)
		}
		if c.Size != tt.size || len(c.Modules) != tt.size || len(c.Rows()[0]) != tt.size {
			t.Errorf("%d bytes: size %d, want %d", len(tt.text), c.Size, tt.size)
		}
	}
	if _, err := Encode([]byte(strings.Repeat("a", 214))); err != ErrTooLong {
		t.Fatalf("214 bytes: %v, want ErrTooLong", err)
	}
}

func TestFinderPatterns(t *testing.T) {
	c, err := Encode([]byte("otpauth://totp/Mediarium:ryan?secret=JBSWY3DPEHPK3PXP&issuer=Mediarium"))
	if err != nil {
		t.Fatal(err)
	}
	// The three corners hold a 7x7 square: dark ring, light ring, dark 3x3 middle.
	want := []string{"1111111", "1000001", "1011101", "1011101", "1011101", "1000001", "1111111"}
	rows := c.Rows()
	for _, corner := range [][2]int{{0, 0}, {c.Size - 7, 0}, {0, c.Size - 7}} {
		for dy, line := range want {
			if got := rows[corner[1]+dy][corner[0] : corner[0]+7]; got != line {
				t.Fatalf("finder at %v row %d: %s, want %s", corner, dy, got, line)
			}
		}
	}
	if !c.Modules[c.Size-8][8] {
		t.Fatal("the dark module is missing")
	}
}

func TestReedSolomon(t *testing.T) {
	// Version 1-M "01234567" in numeric mode is the standard's worked example;
	// here the generator for 10 error codewords is checked against its
	// published coefficients (log form 251, 67, 46, 61, 118, 70, 64, 94, 32, 45).
	div := rsDivisor(10)
	exp := make([]byte, 256)
	x := byte(1)
	for i := range 255 {
		exp[i] = x
		x = gfMul(x, 2)
	}
	for i, e := range []int{251, 67, 46, 61, 118, 70, 64, 94, 32, 45} {
		if div[i] != exp[e] {
			t.Fatalf("coefficient %d = %d, want a^%d = %d", i, div[i], e, exp[e])
		}
	}
}
