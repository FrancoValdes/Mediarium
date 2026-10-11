package api_test

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

// The built-in player gets a downloaded track from the server with ranges, so
// it can seek, and a track that hasn't been downloaded yet answers 404.
func TestPlayTrackStreamsWithRanges(t *testing.T) {
	e := newMusicEnv(t)
	albumID, _, flacBytes := albumOnDisk(t, e)
	tracks, err := e.server.MusicRepo.ListTracks(albumID)
	if err != nil || len(tracks) != 3 {
		t.Fatalf("tracks: %v %v", tracks, err)
	}
	url := fmt.Sprintf("%s/api/music/tracks/%d/play", e.base, tracks[1].ID)

	resp, err := e.client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "audio/flac" || len(body) != len(flacBytes) || resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("whole track: %d %q %d bytes, ranges %q", resp.StatusCode, resp.Header.Get("Content-Type"), len(body), resp.Header.Get("Accept-Ranges"))
	}

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Range", "bytes=10-19")
	part, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	chunk, _ := io.ReadAll(part.Body)
	part.Body.Close()
	if part.StatusCode != http.StatusPartialContent || len(chunk) != 10 || string(chunk) != string(flacBytes[10:20]) {
		t.Fatalf("a range for seeking: %d, %d bytes", part.StatusCode, len(chunk))
	}

	// A track without a file, an unknown track and a bad id.
	if err := e.server.MusicRepo.SetTrackFile(tracks[2].ID, ""); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		fmt.Sprintf("/api/music/tracks/%d/play", tracks[2].ID): http.StatusNotFound,
		"/api/music/tracks/999999/play":                        http.StatusNotFound,
		"/api/music/tracks/abc/play":                           http.StatusBadRequest,
	} {
		r, err := e.client.Get(e.base + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != want {
			t.Errorf("GET %s: %d, want %d", path, r.StatusCode, want)
		}
	}
}
