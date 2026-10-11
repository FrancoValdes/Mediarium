package api_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/api"
)

// metaImagesSlow makes the fake picture server hold every download until the
// request is given up on.
var metaImagesSlow atomic.Bool

var (
	testJPEG = append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("j"), 64)...)
	testPNG  = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte("p"), 64)...)
)

// newMetaTMDB serves one movie (The Matrix), one show (Fixture Show), their
// pictures and logos, the way TMDB documents them.
func newMetaTMDB(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	json := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		}
	}
	image := func(data []byte, typ string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if metaImagesSlow.Load() {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(15 * time.Second):
				}
			}
			w.Header().Set("Content-Type", typ)
			w.Write(data)
		}
	}
	mux.HandleFunc("/genre/movie/list", json(`{"genres":[{"id":28,"name":"Action"}]}`))
	mux.HandleFunc("/genre/tv/list", json(`{"genres":[{"id":18,"name":"Drama"}]}`))
	mux.HandleFunc("/movie/603", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":603,"title":"The Matrix","original_title":"The Matrix","release_date":"1999-03-31","overview":"A hacker learns the truth.",
			"vote_average":8.2654,"vote_count":25000,"runtime":136,"tagline":"Welcome to the Real World","status":"Released","imdb_id":"tt0133093",
			"poster_path":"/poster603.jpg","backdrop_path":"/back603.jpg",
			"genres":[{"id":28,"name":"Action"}],
			"belongs_to_collection":{"id":2344,"name":"The Matrix Collection","poster_path":"/col.jpg","backdrop_path":"/colb.jpg"},
			"production_companies":[{"name":"Village Roadshow Pictures"}],"production_countries":[{"name":"United States of America"}],
			"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"R"}]}]},
			"videos":{"results":[{"name":"Trailer","site":"YouTube","key":"vKQi3bBA1y8","type":"Trailer","official":true}]},
			"credits":{"cast":[{"name":"Keanu Reeves","character":"Neo","order":0}],"crew":[{"name":"Lana Wachowski","job":"Director"},{"name":"Lilly Wachowski","job":"Screenplay"}]}}`))
	})
	mux.HandleFunc("/movie/603/images", json(`{"id":603,"logos":[{"file_path":"/logo603.png","iso_639_1":"en","vote_average":5.1,"vote_count":3}],"posters":[],"backdrops":[]}`))
	mux.HandleFunc("/tv/1399", json(`{"id":1399,"name":"Fixture Show","original_name":"Fixture Show","first_air_date":"2011-04-17","overview":"A show.",
		"poster_path":"/showposter.jpg","backdrop_path":"/showback.jpg","vote_average":8.4,"vote_count":900,"status":"Ended",
		"genres":[{"id":18,"name":"Drama"}],"networks":[{"name":"HBO"}],
		"seasons":[{"season_number":0,"name":"Specials","episode_count":1},{"season_number":1,"episode_count":2,"poster_path":"/s1.jpg"},{"season_number":2,"episode_count":1,"poster_path":"/s2.jpg"}],
		"external_ids":{"imdb_id":"tt0944947","tvdb_id":121361},
		"content_ratings":{"results":[{"iso_3166_1":"US","rating":"TV-MA"}]},
		"credits":{"cast":[{"name":"Sean Bean","character":"Ned","order":0}]},"videos":{"results":[]}}`))
	mux.HandleFunc("/tv/1399/images", json(`{"id":1399,"logos":[],"posters":[],"backdrops":[]}`))
	mux.HandleFunc("/tv/1399/season/1", json(`{"episodes":[{"season_number":1,"episode_number":1,"name":"Winter","air_date":"2011-04-17","overview":"It begins."},{"season_number":1,"episode_number":2,"name":"The Road","air_date":"2011-04-24"}]}`))
	mux.HandleFunc("/tv/1399/season/2", json(`{"episodes":[{"season_number":2,"episode_number":1,"name":"The North","air_date":"2012-04-01"}]}`))
	mux.HandleFunc("/tv/1399/season/0", json(`{"episodes":[{"season_number":0,"episode_number":1,"name":"Recap","air_date":"2011-01-01"}]}`))
	// The picture server (the real one is image.tmdb.org).
	mux.HandleFunc("/t/p/original/poster603.jpg", image(testJPEG, "image/jpeg"))
	mux.HandleFunc("/t/p/original/back603.jpg", image(testJPEG, "image/jpeg"))
	mux.HandleFunc("/t/p/original/logo603.png", image(testPNG, "image/png"))
	mux.HandleFunc("/t/p/original/showposter.jpg", image(testJPEG, "image/jpeg"))
	mux.HandleFunc("/t/p/original/showback.jpg", image(testJPEG, "image/jpeg"))
	mux.HandleFunc("/t/p/original/s1.jpg", image(testJPEG, "image/jpeg"))
	mux.HandleFunc("/t/p/original/s2.jpg", image(testJPEG, "image/jpeg"))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// metaLibrary makes a server with one movie and one show on disk.
type metaLibrary struct {
	server  *api.Server
	base    string
	client  *http.Client
	movie   string // the movie's video file
	ep1     string // holds S01E01 and S01E02
	ep2     string // S02E01
	movies  string
	tv      string
	movieID float64
}

func newMetaLibrary(t *testing.T) *metaLibrary {
	t.Helper()
	server, base, client := loginNewServer(t)
	tmdb := newMetaTMDB(t)
	server.TestSetTMDBBaseURL("k", tmdb.URL)
	t.Cleanup(api.SetTestImageBase(tmdb.URL))

	dir := t.TempDir()
	l := &metaLibrary{server: server, base: base, client: client, movies: filepath.Join(dir, "movies"), tv: filepath.Join(dir, "tv")}
	for _, d := range []string{l.movies, l.tv} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	putJSONStatus(t, client, base+"/api/settings", map[string]any{"moviesPath": l.movies, "tvPath": l.tv}, http.StatusOK)

	mv := postJSON[map[string]any](t, client, base+"/api/movies", map[string]any{"tmdbId": 603}, http.StatusCreated)
	l.movieID = mv["id"].(float64)
	l.movie = filepath.Join(l.movies, "The Matrix (1999)", "The Matrix (1999).mkv")
	putFile(t, l.movie, "video")
	if err := server.TestExec(`UPDATE movies SET status = 'downloaded', file_path = ? WHERE id = ?`, l.movie, int64(l.movieID)); err != nil {
		t.Fatal(err)
	}

	sh := postJSON[map[string]any](t, client, base+"/api/series", map[string]any{"tmdbId": 1399}, http.StatusCreated)
	seriesID := int64(sh["id"].(float64))
	if err := server.TestExec(`UPDATE series SET tvdb_id = 121361 WHERE id = ?`, seriesID); err != nil {
		t.Fatal(err)
	}
	l.ep1 = filepath.Join(l.tv, "Fixture Show", "Season 01", "Fixture Show - S01E01-E02.mkv")
	l.ep2 = filepath.Join(l.tv, "Fixture Show", "Season 02", "Fixture Show - S02E01.mkv")
	putFile(t, l.ep1, "video")
	putFile(t, l.ep2, "video")
	for _, e := range []struct {
		file            string
		season, episode int
	}{{l.ep1, 1, 1}, {l.ep1, 1, 2}, {l.ep2, 2, 1}} {
		if err := server.TestExec(`UPDATE episodes SET status = 'downloaded', file_path = ? WHERE series_id = ? AND season = ? AND episode = ?`, e.file, seriesID, e.season, e.episode); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func slurp(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (l *metaLibrary) settings(t *testing.T, body map[string]any) {
	t.Helper()
	putJSONStatus(t, l.client, l.base+"/api/settings/metadata-files", body, http.StatusOK)
}

// runJob starts the whole-library run and waits for it to finish.
func (l *metaLibrary) runJob(t *testing.T, kind string) map[string]any {
	t.Helper()
	postJSON[map[string]any](t, l.client, l.base+"/api/library/metadata-files/run", map[string]any{"kind": kind}, http.StatusAccepted)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st := getJSON[map[string]any](t, l.client, l.base+"/api/library/metadata-files/status")
		if st["running"] == false {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the run did not finish")
	return nil
}

func TestMetadataFilesAreOffByDefault(t *testing.T) {
	l := newMetaLibrary(t)
	got := getJSON[map[string]any](t, l.client, l.base+"/api/settings/metadata-files")
	for _, kind := range []string{"movies", "tv"} {
		k, _ := got[kind].(map[string]any)
		if k["nfo"] != false || k["artwork"] != false {
			t.Fatalf("%s default = %v", kind, k)
		}
	}
	if got["replace"] != false || got["hasTmdbKey"] != true {
		t.Fatalf("defaults = %v", got)
	}
	// Importing with everything off writes nothing.
	l.server.TestMovieImported(l.movie)
	l.server.TestEpisodesImported(l.ep1, l.ep2)
	entries, _ := os.ReadDir(filepath.Dir(l.movie))
	if len(entries) != 1 {
		t.Fatalf("files appeared with the feature off: %v", entries)
	}
	// And there is nothing to run.
	postJSON[map[string]any](t, l.client, l.base+"/api/library/metadata-files/run", map[string]any{"dryRun": true}, http.StatusConflict)
}

func TestMetadataFilesForTheExistingLibrary(t *testing.T) {
	l := newMetaLibrary(t)
	l.settings(t, map[string]any{"movies": map[string]any{"nfo": true, "artwork": true}, "tv": map[string]any{"nfo": true, "artwork": true}})

	// A person's own poster is already next to the movie.
	own := filepath.Join(filepath.Dir(l.movie), "folder.png")
	putFile(t, own, "my poster")

	// The dry run counts and writes nothing.
	dry := postJSON[map[string]any](t, l.client, l.base+"/api/library/metadata-files/run", map[string]any{"dryRun": true}, http.StatusOK)
	need, _ := dry["needed"].(map[string]any)
	want := map[string]float64{"nfo": 2, "posters": 1, "fanart": 2, "logos": 2, "seasonPosters": 2, "episodeNfos": 2}
	for k, v := range want {
		if need[k] != v {
			t.Errorf("dry run needs %s = %v, want %v (all: %v)", k, need[k], v, need)
		}
	}
	if dry["titles"] != float64(2) || dry["message"] == "" {
		t.Errorf("dry run = %v", dry)
	}
	if exists(filepath.Join(filepath.Dir(l.movie), "movie.nfo")) {
		t.Fatal("a dry run wrote a file")
	}

	st := l.runJob(t, "all")
	if st["failedCount"] != float64(0) || st["canceled"] != false || st["done"] != float64(2) {
		t.Fatalf("run = %v", st)
	}

	// The movie.
	dir := filepath.Dir(l.movie)
	nfo := slurp(t, filepath.Join(dir, "movie.nfo"))
	for _, want := range []string{"<title>The Matrix</title>", `<uniqueid type="tmdb" default="true">603</uniqueid>`, `<uniqueid type="imdb">tt0133093</uniqueid>`,
		"<id>tt0133093</id>", "<mpaa>R</mpaa>", "<name>The Matrix Collection</name>", "<director>Lana Wachowski</director>", "<credits>Lilly Wachowski</credits>",
		"<name>Keanu Reeves</name>", "<studio>Village Roadshow Pictures</studio>", "<genre>Action</genre>", "videoid=vKQi3bBA1y8"} {
		if !strings.Contains(nfo, want) {
			t.Errorf("movie.nfo lacks %s:\n%s", want, nfo)
		}
	}
	if slurp(t, own) != "my poster" {
		t.Error("the person's own poster was changed")
	}
	if exists(filepath.Join(dir, "poster.jpg")) {
		t.Error("a poster was written next to the person's own folder.png")
	}
	if slurp(t, filepath.Join(dir, "fanart.jpg")) != string(testJPEG) || slurp(t, filepath.Join(dir, "clearlogo.png")) != string(testPNG) {
		t.Error("fanart or clearlogo is not what TMDB sent")
	}

	// The show: tvshow.nfo, pictures, season posters and an NFO for each video.
	show := filepath.Join(l.tv, "Fixture Show")
	tvnfo := slurp(t, filepath.Join(show, "tvshow.nfo"))
	for _, want := range []string{"<title>Fixture Show</title>", `<uniqueid type="tvdb" default="true">121361</uniqueid>`, `<uniqueid type="tmdb">1399</uniqueid>`,
		`<uniqueid type="imdb">tt0944947</uniqueid>`, "<id>121361</id>", "<status>Ended</status>", "<studio>HBO</studio>", "<mpaa>TV-MA</mpaa>"} {
		if !strings.Contains(tvnfo, want) {
			t.Errorf("tvshow.nfo lacks %s:\n%s", want, tvnfo)
		}
	}
	for _, f := range []string{"poster.jpg", "fanart.jpg", "season01-poster.jpg", "season02-poster.jpg"} {
		if !exists(filepath.Join(show, f)) {
			t.Errorf("%s was not written", f)
		}
	}
	if exists(filepath.Join(show, "season-specials-poster.jpg")) || exists(filepath.Join(show, "clearlogo.png")) {
		t.Error("a file was written for something the show doesn't have")
	}
	two := slurp(t, strings.TrimSuffix(l.ep1, ".mkv")+".nfo")
	if strings.Count(two, "<episodedetails>") != 2 || !strings.Contains(two, "<title>The Road</title>") || !strings.Contains(two, "<aired>2011-04-17</aired>") {
		t.Errorf("the two-episode file's NFO:\n%s", two)
	}
	if !strings.Contains(slurp(t, strings.TrimSuffix(l.ep2, ".mkv")+".nfo"), "<season>2</season>") {
		t.Error("the second episode's NFO is wrong")
	}

	// Running it again finds nothing to do and changes nothing.
	again := postJSON[map[string]any](t, l.client, l.base+"/api/library/metadata-files/run", map[string]any{"dryRun": true}, http.StatusOK)
	if n, _ := again["needed"].(map[string]any); n["nfo"] != float64(0) || n["fanart"] != float64(0) {
		t.Errorf("a second dry run still wants to write: %v", again)
	}

	// A file that was changed by hand stays that way...
	putFile(t, filepath.Join(dir, "movie.nfo"), "<movie><title>Mine</title></movie>")
	l.runJob(t, "movies")
	if slurp(t, filepath.Join(dir, "movie.nfo")) != "<movie><title>Mine</title></movie>" {
		t.Error("an NFO that was there got overwritten without asking")
	}
	// ...unless the person chooses to replace.
	l.settings(t, map[string]any{"movies": map[string]any{"nfo": true, "artwork": true}, "tv": map[string]any{"nfo": true, "artwork": true}, "replace": true})
	l.runJob(t, "movies")
	if !strings.Contains(slurp(t, filepath.Join(dir, "movie.nfo")), "<title>The Matrix</title>") {
		t.Error("replace did not replace Mediarium's own file")
	}
	if slurp(t, own) != "my poster" {
		t.Error("replace touched the person's own folder.png")
	}
}

func TestMetadataFilesAfterAnImport(t *testing.T) {
	l := newMetaLibrary(t)
	// Only movies, only the NFO.
	l.settings(t, map[string]any{"movies": map[string]any{"nfo": true}})
	l.server.TestMovieImported(l.movie)
	l.server.TestEpisodesImported(l.ep1, l.ep2)
	dir := filepath.Dir(l.movie)
	if !exists(filepath.Join(dir, "movie.nfo")) {
		t.Fatal("the movie's NFO was not written after the import")
	}
	if exists(filepath.Join(dir, "poster.jpg")) {
		t.Error("artwork was written although only the NFO is on")
	}
	if exists(filepath.Join(l.tv, "Fixture Show", "tvshow.nfo")) {
		t.Error("shows are off, yet their files were written")
	}

	// Shows on, artwork only: the pictures but no NFO.
	l.settings(t, map[string]any{"movies": map[string]any{"nfo": true}, "tv": map[string]any{"artwork": true}})
	l.server.TestEpisodesImported(l.ep1, l.ep2)
	if !exists(filepath.Join(l.tv, "Fixture Show", "poster.jpg")) || !exists(filepath.Join(l.tv, "Fixture Show", "season01-poster.jpg")) {
		t.Error("the show's pictures were not written")
	}
	if exists(filepath.Join(l.tv, "Fixture Show", "tvshow.nfo")) || exists(strings.TrimSuffix(l.ep1, ".mkv")+".nfo") {
		t.Error("NFO files were written although only artwork is on for shows")
	}
}

func TestMetadataFilesRunReportsItsProgress(t *testing.T) {
	l := newMetaLibrary(t)
	l.settings(t, map[string]any{"movies": map[string]any{"nfo": true}})
	// Stopping when nothing runs is harmless.
	postJSON[map[string]any](t, l.client, l.base+"/api/library/metadata-files/cancel", map[string]any{}, http.StatusOK)
	st := l.runJob(t, "movies")
	if st["running"] != false || st["done"] != float64(1) {
		t.Fatalf("status = %v", st)
	}
	if got := fmt.Sprint(st["message"]); !strings.Contains(got, "1 file written") {
		t.Errorf("message = %q", got)
	}
}

func TestMetadataFilesRunCanBeStopped(t *testing.T) {
	l := newMetaLibrary(t)
	l.settings(t, map[string]any{"movies": map[string]any{"artwork": true}})
	metaImagesSlow.Store(true)
	t.Cleanup(func() { metaImagesSlow.Store(false) })

	postJSON[map[string]any](t, l.client, l.base+"/api/library/metadata-files/run", map[string]any{"kind": "movies"}, http.StatusAccepted)
	// A second run is refused while this one goes on.
	postJSON[map[string]any](t, l.client, l.base+"/api/library/metadata-files/run", map[string]any{"kind": "movies"}, http.StatusConflict)
	time.Sleep(200 * time.Millisecond)
	postJSON[map[string]any](t, l.client, l.base+"/api/library/metadata-files/cancel", map[string]any{}, http.StatusOK)

	deadline := time.Now().Add(8 * time.Second)
	var st map[string]any
	for time.Now().Before(deadline) {
		st = getJSON[map[string]any](t, l.client, l.base+"/api/library/metadata-files/status")
		if st["running"] == false {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st["running"] != false || st["canceled"] != true {
		t.Fatalf("the run was not stopped: %v", st)
	}
	if !strings.HasPrefix(fmt.Sprint(st["message"]), "Stopped.") {
		t.Errorf("message = %q", st["message"])
	}
	dir := filepath.Dir(l.movie)
	if exists(filepath.Join(dir, "poster.jpg")) {
		t.Error("a picture was written by a run that was stopped")
	}
}
