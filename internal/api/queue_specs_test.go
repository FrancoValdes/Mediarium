package api_test

import (
	"testing"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/queue"
)

// Activity shows what a release is (picture size, source, codec, language),
// read from its name, so a person can tell the downloads apart at a glance.
func TestQueueItemsSayWhatTheReleaseIs(t *testing.T) {
	server, base, client := loginNewServer(t)
	movie, err := server.MovieRepo.Add(library.Movie{TMDBID: 811, Title: "Night Harbour", Year: 2021, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{
		"Night.Harbour.2021.1080p.BluRay.x265-GRP",
		"Night.Harbour.2021.GERMAN.2160p.WEB-DL.H.265-GRP",
		"Night.Harbour.2021.MULTi.720p.HDTV.x264-GRP",
	}
	for _, rt := range titles {
		_, err := server.QueueRepo.Enqueue(queue.Item{MovieID: movie.ID, ReleaseTitle: rt, NZBURL: "http://127.0.0.1:1/x.nzb"})
		if err != nil {
			t.Fatal(err)
		}
	}
	items := getJSON[[]map[string]any](t, client, base+"/api/queue")
	byTitle := map[string]map[string]any{}
	for _, it := range items {
		byTitle[it["releaseTitle"].(string)] = it
	}
	first := byTitle[titles[0]]
	if first["resolution"] != "1080p" || first["source"] == nil || first["source"] == "" || first["codec"] == nil || first["codec"] == "" {
		t.Fatalf("a 1080p BluRay x265 release says so: %v", first)
	}
	second := byTitle[titles[1]]
	if second["resolution"] != "2160p" {
		t.Fatalf("a 2160p release says so: %v", second)
	}
	if lang, _ := second["language"].(string); lang == "" {
		t.Fatalf("a release that names German says its language: %v", second)
	}
	if lang, _ := first["language"].(string); lang != "" && lang == "German" {
		t.Fatalf("a release that names no language isn't given one: %v", first)
	}
}
