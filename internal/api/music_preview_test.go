package api_test

import (
	"net/http"
	"testing"
)

const previewAlbum = "aaaaaaaa-0000-4000-8000-000000000001"

// An album that is not in the library has a page of its own: its artist,
// genres and tracklist, so Discover is more than an Add button.
func TestAnAlbumNotInTheLibraryHasAPreviewPage(t *testing.T) {
	e := newMusicEnv(t)
	got := getJSON[map[string]any](t, e.client, e.base+"/api/music/preview/"+previewAlbum)
	if got["title"] != "First Album" || got["artistName"] != "Fixture Band" || got["type"] != "album" || got["releaseDate"] != "1999-03-01" {
		t.Fatalf("preview: %v", got)
	}
	if got["inLibrary"] != false || got["coverUrl"] != "/api/music/covers/release-group/"+previewAlbum {
		t.Fatalf("library and cover: %v", got)
	}
	genres := got["genres"].([]any)
	if len(genres) != 2 || genres[0] != "rock" {
		t.Fatalf("genres should be the most agreed first: %v", genres)
	}
	tracks := got["tracks"].([]any)
	if len(tracks) != 3 || tracks[0].(map[string]any)["title"] != "Opening" || tracks[1].(map[string]any)["lengthMs"] != float64(200000) {
		t.Fatalf("tracks: %v", tracks)
	}
}

func TestThePreviewPageSaysWhenTheAlbumIsAlreadyInTheLibrary(t *testing.T) {
	e := newMusicEnv(t)
	e.addArtist(t, "all")
	got := getJSON[map[string]any](t, e.client, e.base+"/api/music/preview/"+previewAlbum)
	if got["inLibrary"] != true || got["artistId"] == nil || got["albumId"] == nil {
		t.Fatalf("an album in the library should say so: %v", got)
	}
}

func TestThePreviewPageRefusesWhatIsNotAnId(t *testing.T) {
	e := newMusicEnv(t)
	for _, id := range []string{"nope", "..%2F..%2Fetc", "aaaaaaaa-0000-4000-8000-00000000000z"} {
		if status, _ := doStatus(t, e.client, http.MethodGet, e.base+"/api/music/preview/"+id); status != http.StatusNotFound {
			t.Errorf("preview of %q: %d, want 404", id, status)
		}
	}
}
