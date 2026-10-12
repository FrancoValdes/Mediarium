package api

import "testing"

func TestShowStatusOf(t *testing.T) {
	tests := []struct{ tmdb, want string }{
		{"", ""},
		{"  ", ""},
		{"Ended", "ended"},
		{"Canceled", "ended"},
		{"Returning Series", "continuing"},
		{"In Production", "upcoming"},
		{"Planned", "upcoming"},
	}
	for _, tt := range tests {
		t.Run(tt.tmdb, func(t *testing.T) {
			if got := showStatusOf(tt.tmdb); got != tt.want {
				t.Errorf("showStatusOf(%q) = %q, want %q", tt.tmdb, got, tt.want)
			}
		})
	}
}
