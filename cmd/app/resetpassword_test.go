package main

import (
	"bufio"
	"strings"
	"testing"
)

// Both prompts read from one reader, so a password and its confirmation piped
// in together are both seen.
func TestPromptPasswordPiped(t *testing.T) {
	in := bufio.NewReader(strings.NewReader("first-password\r\nfirst-password\n"))
	for i := range 2 {
		got, err := promptPassword(in, "")
		if err != nil {
			t.Fatalf("prompt %d: %v", i+1, err)
		}
		if got != "first-password" {
			t.Fatalf("prompt %d read %q", i+1, got)
		}
	}
	if _, err := promptPassword(in, ""); err == nil {
		t.Fatal("a third prompt with nothing left should fail")
	}
}
