package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/library"
)

// flaggedOpenSubtitles answers every English search with the same four
// subtitles whatever the filter parameters say, so the tests show that the
// choice is made on what comes back too, not only by the service.
type flaggedOpenSubtitles struct {
	srv *httptest.Server
	mu  sync.Mutex
	// queries holds the parameters of each search.
	queries []map[string]string
}

const (
	fakePlainID  = 11
	fakeSDHID    = 12
	fakeForcedID = 13
	fakeBothID   = 14
)

func newFlaggedOpenSubtitles(t *testing.T, only ...int) *flaggedOpenSubtitles {
	t.Helper()
	f := &flaggedOpenSubtitles{}
	all := map[int]map[string]any{
		fakePlainID:  {"hearing_impaired": false, "foreign_parts_only": false, "release": "Some.Movie.2001.1080p.BluRay.x264-GRP"},
		fakeSDHID:    {"hearing_impaired": true, "foreign_parts_only": false, "release": "Some.Movie.2001.1080p.BluRay.x264-GRP"},
		fakeForcedID: {"hearing_impaired": false, "foreign_parts_only": true, "release": "Some.Movie.2001.1080p.BluRay.x264-GRP"},
		fakeBothID:   {"hearing_impaired": true, "foreign_parts_only": true, "release": "Some.Movie.2001.1080p.BluRay.x264-GRP"},
	}
	ids := only
	if len(ids) == 0 {
		ids = []int{fakePlainID, fakeSDHID, fakeForcedID, fakeBothID}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/subtitles", func(w http.ResponseWriter, r *http.Request) {
		q := map[string]string{}
		for k := range r.URL.Query() {
			q[k] = r.URL.Query().Get(k)
		}
		f.mu.Lock()
		f.queries = append(f.queries, q)
		f.mu.Unlock()
		var data []map[string]any
		if q["languages"] == "en" {
			for _, id := range ids {
				attrs := map[string]any{"language": "en", "download_count": 100, "ratings": 8.0, "files": []map[string]any{{"file_id": id}}}
				for k, v := range all[id] {
					attrs[k] = v
				}
				data = append(data, map[string]any{"attributes": attrs})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			FileID int `json:"file_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]any{"link": "http://" + r.Host + "/file/" + strconv.Itoa(body.FileID) + ".srt"})
	})
	mux.HandleFunc("/file/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/file/"), ".srt")
		fmt.Fprintf(w, "1\n00:00:01,000 --> 00:00:02,000\nsubtitle %s\n", id)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *flaggedOpenSubtitles) lastQuery() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queries) == 0 {
		return nil
	}
	return f.queries[len(f.queries)-1]
}

// profileMovie adds a downloaded movie with a video file and returns its
// folder and the video's base name.
func profileMovie(t *testing.T, env *tvAutoEnv) (dir, base string, id int64) {
	t.Helper()
	dir = t.TempDir()
	base = "Some Movie (2001)"
	video := filepath.Join(dir, base+".mkv")
	if err := os.WriteFile(video, []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := env.server.MovieRepo.Add(library.Movie{TMDBID: 9, Title: "Some Movie", Year: 2001, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.server.MovieRepo.SetStatus(m.ID, library.StatusDownloaded, "Bluray-1080p", video); err != nil {
		t.Fatal(err)
	}
	return dir, base, m.ID
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(b)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestSubtitleProfileSettingsRoundTripAndValidation(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	get := func() map[string]any {
		s := getJSON[map[string]any](t, env.client, env.baseURL+"/api/settings")
		p, _ := s["subtitleProfiles"].(map[string]any)
		return p
	}
	if p := get(); len(p) != 0 {
		t.Fatalf("a fresh install has no profile choices: %v", p)
	}
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{
		"subtitleProfiles": map[string]any{"en": map[string]any{"full": "normal", "forced": true}, "FR": map[string]any{"full": "none"}, "de": map[string]any{"full": "either"}},
	}, http.StatusOK)
	p := get()
	if len(p) != 2 {
		t.Fatalf("the default for German is not stored: %v", p)
	}
	if en := p["en"].(map[string]any); en["full"] != "normal" || en["forced"] != true {
		t.Fatalf("en: %v", en)
	}
	if fr := p["fr"].(map[string]any); fr["full"] != "none" || fr["forced"] != true {
		t.Fatalf("forced only always wants the forced track: %v", fr)
	}
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleProfiles": map[string]any{"en": map[string]any{"full": "loud"}}}, http.StatusBadRequest)
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleProfiles": map[string]any{"../x": map[string]any{"full": "normal"}}}, http.StatusBadRequest)
	// Other settings saves leave the choices alone, and {} puts them back.
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleAutoDownload": true}, http.StatusOK)
	if len(get()) != 2 {
		t.Fatalf("a save that doesn't mention profiles changed them: %v", get())
	}
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleProfiles": map[string]any{}}, http.StatusOK)
	if len(get()) != 0 {
		t.Fatalf("{} should clear the choices: %v", get())
	}
}

func TestSubtitleDefaultProfileStillAsksForNothingExtraAndSkipsForcedResults(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	fake := newFlaggedOpenSubtitles(t, fakeForcedID, fakePlainID)
	env.server.TestSetSubtitlesBaseURL("fixture-key", fake.srv.URL)
	dir, base, _ := profileMovie(t, env)

	res := postJSON[map[string]any](t, env.client, env.baseURL+"/api/subtitles/sweep", nil, http.StatusOK)
	if res["downloaded"] != float64(1) {
		t.Fatalf("one subtitle: %+v", res)
	}
	q := fake.lastQuery()
	if q["hearing_impaired"] != "" || q["foreign_parts_only"] != "" {
		t.Fatalf("the default asks the service for no filter, as before: %v", q)
	}
	if got := readFile(t, filepath.Join(dir, base+".en.srt")); !strings.Contains(got, fmt.Sprint("subtitle ", fakePlainID)) {
		t.Fatalf("the plain subtitle should be saved, not the forced one: %q", got)
	}
	if fileExists(filepath.Join(dir, base+".en.forced.srt")) {
		t.Fatal("a forced track must not be saved when nobody asked for one")
	}
}

func TestSubtitleDefaultProfileNamesAnSDHResultSdh(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	fake := newFlaggedOpenSubtitles(t, fakeSDHID)
	env.server.TestSetSubtitlesBaseURL("fixture-key", fake.srv.URL)
	dir, base, movie := profileMovie(t, env)

	postJSON[map[string]any](t, env.client, env.baseURL+"/api/subtitles/sweep", nil, http.StatusOK)
	if !fileExists(filepath.Join(dir, base+".en.sdh.srt")) || fileExists(filepath.Join(dir, base+".en.srt")) {
		entries, _ := os.ReadDir(dir)
		t.Fatalf("the SDH subtitle should be saved as .en.sdh.srt: %v", entries)
	}
	// An SDH subtitle counts as done under the default, so it is not fetched again.
	st := getJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/movies/%d/subtitles/status", env.baseURL, movie))
	if present := st["present"].([]any); len(present) != 1 || present[0] != "en" {
		t.Fatalf("status: %+v", st)
	}
	wanted := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/subtitles/wanted")
	if len(wanted) != 0 {
		t.Fatalf("nothing should be wanted: %+v", wanted)
	}
}

func TestSubtitleProfilePicksTheKindAskedFor(t *testing.T) {
	tests := []struct {
		name    string
		profile map[string]any
		want    map[string]int // file name suffix -> the file id its content must name
		query   map[string]string
	}{
		{
			name:    "normal only",
			profile: map[string]any{"full": "normal"},
			want:    map[string]int{".en.srt": fakePlainID},
			query:   map[string]string{"hearing_impaired": "exclude", "foreign_parts_only": "exclude"},
		},
		{
			name:    "SDH only",
			profile: map[string]any{"full": "sdh"},
			want:    map[string]int{".en.sdh.srt": fakeSDHID},
			query:   map[string]string{"hearing_impaired": "only"},
		},
		{
			name:    "forced only",
			profile: map[string]any{"full": "none"},
			want:    map[string]int{".en.forced.srt": fakeForcedID},
			query:   map[string]string{"foreign_parts_only": "only"},
		},
		{
			name:    "normal plus a forced track",
			profile: map[string]any{"full": "normal", "forced": true},
			want:    map[string]int{".en.srt": fakePlainID, ".en.forced.srt": fakeForcedID},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTVAutoEnv(t, nil, nil)
			fake := newFlaggedOpenSubtitles(t)
			env.server.TestSetSubtitlesBaseURL("fixture-key", fake.srv.URL)
			putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleProfiles": map[string]any{"en": tt.profile}}, http.StatusOK)
			dir, base, _ := profileMovie(t, env)

			wanted := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/subtitles/wanted")
			if len(wanted) != 1 || len(wanted[0]["missing"].([]any)) != len(tt.want) {
				t.Fatalf("wanted: %+v", wanted)
			}
			res := postJSON[map[string]any](t, env.client, env.baseURL+"/api/subtitles/sweep", nil, http.StatusOK)
			if res["downloaded"] != float64(len(tt.want)) {
				t.Fatalf("sweep: %+v", res)
			}
			for suffix, id := range tt.want {
				if got := readFile(t, filepath.Join(dir, base+suffix)); !strings.Contains(got, fmt.Sprint("subtitle ", id)) {
					t.Errorf("%s holds %q, want subtitle %d", suffix, got, id)
				}
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != len(tt.want)+1 {
				t.Errorf("unexpected files: %v", entries)
			}
			if tt.query != nil {
				// Each search is for one kind; the last holds the filter of the last slot.
				q := fake.lastQuery()
				for k, v := range tt.query {
					if q[k] != v {
						t.Errorf("%s = %q, want %q", k, q[k], v)
					}
				}
			}
			// Everything is there now.
			if wanted := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/subtitles/wanted"); len(wanted) != 0 {
				t.Errorf("still wanted: %+v", wanted)
			}
		})
	}
}

func TestSubtitleForcedFileDoesNotFillTheFullSlotButFillsItsOwn(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	fake := newFlaggedOpenSubtitles(t)
	env.server.TestSetSubtitlesBaseURL("fixture-key", fake.srv.URL)
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleProfiles": map[string]any{"en": map[string]any{"full": "normal", "forced": true}}}, http.StatusOK)
	dir, base, movie := profileMovie(t, env)
	if err := os.WriteFile(filepath.Join(dir, base+".en.forced.srt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	wanted := getJSON[[]map[string]any](t, env.client, env.baseURL+"/api/subtitles/wanted")
	if len(wanted) != 1 {
		t.Fatalf("wanted: %+v", wanted)
	}
	if missing := wanted[0]["missing"].([]any); len(missing) != 1 || missing[0] != "en" {
		t.Fatalf("only the full subtitle is missing: %v", missing)
	}
	st := getJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/movies/%d/subtitles/status", env.baseURL, movie))
	slots := st["slots"].([]any)
	if len(slots) != 2 {
		t.Fatalf("slots: %+v", st)
	}
	byKind := map[string]bool{}
	for _, s := range slots {
		m := s.(map[string]any)
		byKind[m["kind"].(string)] = m["present"].(bool)
	}
	if byKind["full"] || !byKind["forced"] {
		t.Fatalf("slots: %v", byKind)
	}
	if present := st["present"].([]any); len(present) != 0 {
		t.Fatalf("English is not done until both are there: %v", present)
	}
}

func TestManualSubtitleDownloadNamesTheKind(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	fake := newFlaggedOpenSubtitles(t)
	env.server.TestSetSubtitlesBaseURL("fixture-key", fake.srv.URL)
	dir, base, movie := profileMovie(t, env)
	url := fmt.Sprintf("%s/api/movies/%d/subtitles", env.baseURL, movie)

	results := getJSON[[]map[string]any](t, env.client, url+"?lang=en")
	kinds := map[float64]string{}
	for _, r := range results {
		v, _ := r["variant"].(string)
		kinds[r["fileId"].(float64)] = v
	}
	if kinds[fakePlainID] != "" || kinds[fakeSDHID] != "sdh" || kinds[fakeForcedID] != "forced" || kinds[fakeBothID] != "sdh" {
		t.Fatalf("variants in the list: %v", kinds)
	}
	// Only the forced ones when asked.
	forced := getJSON[[]map[string]any](t, env.client, url+"?lang=en&variant=forced")
	if len(forced) != 1 || forced[0]["fileId"] != float64(fakeForcedID) {
		t.Fatalf("forced list: %+v", forced)
	}
	if q := fake.lastQuery(); q["foreign_parts_only"] != "only" {
		t.Fatalf("a forced search asks for foreign parts only: %v", q)
	}

	postJSON[map[string]any](t, env.client, url+"/download", map[string]any{"fileId": fakeForcedID, "language": "en", "variant": "forced"}, http.StatusOK)
	postJSON[map[string]any](t, env.client, url+"/download", map[string]any{"fileId": fakeSDHID, "language": "en", "variant": "sdh"}, http.StatusOK)
	postJSON[map[string]any](t, env.client, url+"/download", map[string]any{"fileId": fakePlainID, "language": "en"}, http.StatusOK)
	for _, suffix := range []string{".en.forced.srt", ".en.sdh.srt", ".en.srt"} {
		if !fileExists(filepath.Join(dir, base+suffix)) {
			t.Errorf("%s should have been saved", suffix)
		}
	}
	postJSON[map[string]any](t, env.client, url+"/download", map[string]any{"fileId": fakePlainID, "language": "en", "variant": "../evil"}, http.StatusBadRequest)
}

// A subtitle made for the exact video file replaces the one picked by name,
// and where the two are different kinds under the default profile (SDH against
// plain) the old file goes, so the title doesn't end up with both.
func TestSubtitleUpgradeReplacesAnSDHPickWithAPlainExactMatch(t *testing.T) {
	env := newTVAutoEnv(t, nil, nil)
	var ready atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/subtitles", func(w http.ResponseWriter, r *http.Request) {
		data := []map[string]any{
			{"attributes": map[string]any{"language": "en", "release": "Some.Movie.2001.1080p.BluRay.x264-GRP", "download_count": 100, "hearing_impaired": true, "files": []map[string]any{{"file_id": 7}}}},
			{"attributes": map[string]any{"language": "en", "release": "Some.Movie.2001.1080p.BluRay.x264-GRP", "download_count": 100, "foreign_parts_only": true, "files": []map[string]any{{"file_id": 8}}}},
		}
		if ready.Load() && r.URL.Query().Get("moviehash") != "" {
			data = append(data, map[string]any{"attributes": map[string]any{"language": "en", "release": "other", "moviehash_match": true, "files": []map[string]any{{"file_id": 9}}}})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			FileID int `json:"file_id"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(map[string]any{"link": fmt.Sprintf("http://%s/file/%d", r.Host, req.FileID)})
	})
	mux.HandleFunc("/file/{id}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "1\n00:00:01,000 --> 00:00:02,000\nfile %s\n", r.PathValue("id"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	env.server.TestSetSubtitlesBaseURL("fixture-key", srv.URL)
	putJSONStatus(t, env.client, env.baseURL+"/api/settings", map[string]any{"subtitleLanguages": []string{"en"}, "subtitleAutoDownload": true}, http.StatusOK)

	dir, base, _ := profileMovie(t, env)
	if err := os.WriteFile(filepath.Join(dir, base+".mkv"), make([]byte, 200*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	env.server.TestSubtitleSweepJob(context.Background())
	sdh := filepath.Join(dir, base+".en.sdh.srt")
	if !strings.Contains(readFile(t, sdh), "file 7") {
		t.Fatal("the SDH subtitle should be saved first")
	}

	ready.Store(true)
	env.server.TestAgeSubtitleFiles(4 * 24 * time.Hour)
	env.server.TestSubtitleSweepJob(context.Background())
	if got := readFile(t, filepath.Join(dir, base+".en.srt")); !strings.Contains(got, "file 9") {
		t.Fatalf("the exact-file subtitle should replace it: %q", got)
	}
	if fileExists(sdh) {
		t.Fatal("the replaced SDH file should be gone")
	}
}
