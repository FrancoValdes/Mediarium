package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Shortening a push message never splits a character.
func TestCutText(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly", 7, "exactly"},
		{"abcdef", 3, "abc"},
		{"café", 4, "caf"},  // é is two bytes: cutting at 4 would split it
		{"café", 5, "café"}, // all of it fits
		{"日本語", 4, "日"},     // three bytes each
		{"日本語", 2, ""},      // not even one fits
		{"🎬 Film", 3, ""},   // a four-byte emoji
		{"🎬 Film", 5, "🎬 "}, // emoji and the space
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := cutText(tt.in, tt.max)
		if got != tt.want || !utf8.ValidString(got) || len(got) > tt.max {
			t.Errorf("cutText(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
		}
	}
	long := strings.Repeat("é", 500)
	if got := cutText(long, maxPushTitle); len(got) > maxPushTitle || !utf8.ValidString(got) {
		t.Errorf("a long title was not cut cleanly: %d bytes", len(got))
	}
}
