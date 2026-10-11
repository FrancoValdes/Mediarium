package indexers

import "testing"

func TestSearchTextDropsApostrophes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Hell's Kitchen S24E01", "Hells Kitchen S24E01"},
		{"Hell’s Kitchen", "Hells Kitchen"},
		{"Marvel's Agents of S.H.I.E.L.D. S01", "Marvels Agents of S.H.I.E.L.D. S01"},
		{"  Plain   Title ", "Plain Title"},
		{"", ""},
	} {
		if got := SearchText(tc.in); got != tc.want {
			t.Errorf("SearchText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
