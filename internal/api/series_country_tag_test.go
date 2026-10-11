package api

import (
	"testing"

	"github.com/rdborg/mediarium/internal/library"
)

func TestMatchesShowWithCountryTag(t *testing.T) {
	hk := library.Series{Title: "Hell's Kitchen", Year: 2005}
	for _, tc := range []struct {
		release string
		series  library.Series
		want    bool
	}{
		{"Hells.Kitchen.US.S24E01.1080p.WEB.h264-GRP", hk, true},
		{"Hells.Kitchen.S24E01.Making.A.State-ment.1080p.CRAV.WEB-DL.DD5.1.H.264-STC", hk, true},
		{"Hells.Kitchen.UK.S01E01.720p-GRP", hk, false},
		{"Hells.Kitchen.Italy.S01E01.720p-GRP", hk, false},
		{"Marvels.Daredevil.S02E13.A.Cold.Day.in.Hells.Kitchen.1080p-GRP", hk, false},
		{"The.Office.UK.2001.S01E01.720p-GRP", library.Series{Title: "The Office", Year: 2001}, true},
		{"The.Office.UK.S01E01.720p-GRP", library.Series{Title: "The Office", Year: 2005}, false},
	} {
		if got := matchesShow(tc.release, tc.series); got != tc.want {
			t.Errorf("matchesShow(%q, %q) = %v, want %v", tc.release, tc.series.Title, got, tc.want)
		}
	}
}
