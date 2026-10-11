package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A kind of media can have several folders, and each title says which one it
// is kept in.
func TestMoviesCanBeKeptInMoreThanOneFolder(t *testing.T) {
	server, base, client := loginNewServer(t)
	server.TestSetTMDBBaseURL("k", newTitleTMDB(t).URL)
	main, usb, kids := t.TempDir(), t.TempDir(), t.TempDir()
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"moviesPath": main}, http.StatusOK)

	// Nothing extra to start with: only the main folder is on offer.
	folders := getJSON[map[string][]string](t, client, base+"/api/library/folders")
	if len(folders["movies"]) != 1 || filepath.Clean(folders["movies"][0]) != filepath.Clean(main) {
		t.Fatalf("one folder at first: %v", folders)
	}

	putJSONStatus(t, client, base+"/api/settings", map[string]any{"moviesExtraPaths": []string{usb, kids, usb}}, http.StatusOK)
	settings := getJSON[map[string]any](t, client, base+"/api/settings")
	extra, _ := settings["moviesExtraPaths"].([]any)
	if len(extra) != 2 {
		t.Fatalf("a repeat is dropped: %v", extra)
	}
	folders = getJSON[map[string][]string](t, client, base+"/api/library/folders")
	if len(folders["movies"]) != 3 || folders["movies"][0] != main {
		t.Fatalf("the main folder comes first: %v", folders["movies"])
	}

	// A movie asks for one of them; anything else is refused.
	postJSON[map[string]any](t, client, base+"/api/movies", map[string]any{"tmdbId": 603, "rootPath": filepath.Join(main, "elsewhere")}, http.StatusBadRequest)
	added := postJSON[map[string]any](t, client, base+"/api/movies", map[string]any{"tmdbId": 603, "rootPath": usb}, http.StatusCreated)
	if filepath.Clean(added["rootPath"].(string)) != filepath.Clean(usb) {
		t.Fatalf("the movie should say where it lives: %v", added)
	}
	// Choosing the main folder stores nothing: it follows the default.
	other := postJSON[map[string]any](t, client, base+"/api/movies", map[string]any{"tmdbId": 700, "rootPath": main}, http.StatusCreated)
	if other["rootPath"] != nil && other["rootPath"] != "" {
		t.Fatalf("the main folder is the default and stores as empty: %v", other)
	}

	// It can be changed later (the files already there stay).
	id := int64(added["id"].(float64))
	putJSONStatus(t, client, base+"/api/movies/"+fmt.Sprint(id)+"/folder", map[string]any{"rootPath": kids}, http.StatusOK)
	putJSONStatus(t, client, base+"/api/movies/"+fmt.Sprint(id)+"/folder", map[string]any{"rootPath": "/nowhere"}, http.StatusBadRequest)
	list := getJSON[[]map[string]any](t, client, base+"/api/movies")
	var found bool
	for _, m := range list {
		if m["id"] == added["id"] {
			found = true
			if filepath.Clean(m["rootPath"].(string)) != filepath.Clean(kids) {
				t.Fatalf("changed folder: %v", m)
			}
		}
	}
	if !found {
		t.Fatal("the movie is missing from the list")
	}

	// Folders that overlap are refused: one inside another, or the same as the main one.
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"moviesExtraPaths": []string{main}}, http.StatusBadRequest)
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"moviesExtraPaths": []string{filepath.Join(main, "sub")}}, http.StatusBadRequest)
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"moviesExtraPaths": []string{"relative/path"}}, http.StatusBadRequest)
	// An empty list clears them again.
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"moviesExtraPaths": []string{}}, http.StatusOK)
	if folders = getJSON[map[string][]string](t, client, base+"/api/library/folders"); len(folders["movies"]) != 1 {
		t.Fatalf("cleared: %v", folders)
	}
}

// An episode of a show kept in another folder is imported into that folder.
func TestAShowKeptInAnotherFolderImportsThere(t *testing.T) {
	content := []byte("episode two bytes " + strings.Repeat("x", 500))
	env := newTVEnv(t, map[string]nntpArticle{"ep-two@example": {fileName: "release-file.mkv", content: content}})
	usb := filepath.Join(t.TempDir(), "usb-tv")
	if err := os.MkdirAll(usb, 0o755); err != nil {
		t.Fatal(err)
	}
	base := env.httpSrv.URL
	putJSONStatus(t, env.client, base+"/api/settings", map[string]any{"tvExtraPaths": []string{usb}}, http.StatusOK)
	putJSONStatus(t, env.client, fmt.Sprintf("%s/api/series/%d/folder", base, env.seriesID), map[string]any{"rootPath": usb}, http.StatusOK)

	env.grab(t, "Fixture.Show.S01E02.1080p.WEB-DL.x264-GRP")
	if status, errMsg := env.waitForTerminal(t); status != "completed" {
		t.Fatalf("expected the grab to complete, got status=%q error=%q", status, errMsg)
	}
	want := filepath.Join(usb, "Fixture Show (2011)", "Season 01", "Fixture Show - S01E02 - Second.mkv")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("the episode should be in the chosen folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.tvRoot, "Fixture Show (2011)")); err == nil {
		t.Fatal("nothing should have gone to the main TV folder")
	}
}
