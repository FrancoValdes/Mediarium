package music_test

import (
	"testing"

	"github.com/rdborg/mediarium/internal/music"
)

func TestListMonitoredReleasing(t *testing.T) {
	r := newRepo(t)
	if _, _, err := r.AddArtist(music.Artist{MBID: "mb-a", Name: "Band A", Monitored: true}, []music.Album{
		{MBID: "rg-1", Title: "In window", ReleaseDate: "2026-10-20", Monitored: true},
		{MBID: "rg-2", Title: "Not monitored", ReleaseDate: "2026-10-21", Monitored: false},
		{MBID: "rg-3", Title: "Only a year", ReleaseDate: "2026", Monitored: true},
		{MBID: "rg-4", Title: "Too late", ReleaseDate: "2028-01-01", Monitored: true},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := r.ListMonitoredReleasing("2026-09-09", "2027-02-06")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "In window" || got[0].ArtistName != "Band A" || got[0].ArtistID == 0 {
		t.Fatalf("releasing = %+v", got)
	}
}
