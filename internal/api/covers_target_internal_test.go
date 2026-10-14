package api

import (
	"testing"

	"github.com/rdborg/mediarium/internal/parser"
)

// TestCoversTarget checks which releases a season or episode search takes.
func TestCoversTarget(t *testing.T) {
	cases := []struct {
		name            string
		release         string
		season, episode int
		want            bool
	}{
		{"episode for its episode", "Show.S01E05.1080p", 1, 5, true},
		{"episode for another episode", "Show.S01E05.1080p", 1, 6, false},
		{"episode for its season", "Show.S01E05.1080p", 1, 0, false},
		{"spaced episode for its season", "Show S01 E01 1080p", 1, 0, false},
		{"dotted episode for its season", "Show.S1.E1.1080p", 1, 0, false},
		{"spaced episode for its episode", "Show S01 E01 1080p", 1, 1, true},
		{"pack for its season", "Show.S01.1080p", 1, 0, true},
		{"pack for an episode", "Show.S01.1080p", 1, 3, true},
		{"pack for another season", "Show.S01.1080p", 2, 0, false},
		{"multi-season pack for its first season", "Show.S01-S03.1080p", 1, 0, false},
		{"multi-season pack for a later season", "Show.S01-03.1080p", 2, 0, false},
		{"multi-season pack for an episode", "Show.S01-S03.1080p", 1, 4, false},
		{"multi-episode for a listed episode", "Show.1x01-1x03.720p", 1, 2, true},
		{"joined episodes for the second", "Show.S01E01.E02.720p", 1, 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := coversTarget(parser.Parse(tc.release), tc.season, tc.episode); got != tc.want {
				t.Errorf("coversTarget(%q, %d, %d) = %v, want %v", tc.release, tc.season, tc.episode, got, tc.want)
			}
		})
	}
}
