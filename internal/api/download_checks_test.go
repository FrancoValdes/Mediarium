package api_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/api"
	"github.com/rdborg/mediarium/internal/blocklist"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/problems"
	"github.com/rdborg/mediarium/internal/queue"
)

// checksEnv is a server with a Usenet provider that serves the files of the
// releases a test adds, and a movie to download them for.
type checksEnv struct {
	server *api.Server
	base   string
	client *http.Client
	nntp   *gatedNNTP
	movie  library.Movie

	mu     sync.Mutex
	nzbs   map[string]string // path -> NZB text
	bodies map[string][]byte
	srv    *httptest.Server
}

func newChecksEnv(t *testing.T) *checksEnv {
	t.Helper()
	server, base, client := newControlServer(t, false)
	env := &checksEnv{server: server, base: base, client: client, nzbs: map[string]string{}, bodies: map[string][]byte{}}
	env.nntp = newGatedNNTP(t, env.bodies, -1)
	postJSON[map[string]any](t, client, base+"/api/usenet-servers", map[string]any{
		"name": "Fixture Usenet", "host": "127.0.0.1", "port": env.nntp.port, "useSsl": false, "connections": 1,
	}, http.StatusCreated)
	env.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.mu.Lock()
		nzb, ok := env.nzbs[r.URL.Path]
		env.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-nzb")
		fmt.Fprint(w, nzb)
	}))
	t.Cleanup(env.srv.Close)
	m, err := server.MovieRepo.Add(library.Movie{TMDBID: 603, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	env.movie = m
	return env
}

// release publishes an NZB at /<name>.nzb whose files are made of articles
// with the given id prefix, and returns its address. Two releases with the same
// prefix and files are the same post under different names.
func (e *checksEnv) release(name, idPrefix string, files []fixtureFile) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`)
	for i, f := range files {
		id := fmt.Sprintf("%s-%d@example", idPrefix, i+1)
		e.bodies[id] = encodeFixtureYenc(f.name, f.data)
		fmt.Fprintf(&b, `<file subject="[%d/%d] &quot;%s&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="%d" number="1">%s</segment></segments></file>`,
			i+1, len(files), f.name, len(f.data), id)
	}
	b.WriteString(`</nzb>`)
	e.nzbs["/"+name+".nzb"] = b.String()
	return e.srv.URL + "/" + name + ".nzb"
}

// overwriteOnImport lets a second download of the same movie replace the file,
// so a test can tell a skipped download from one the import refused.
func (e *checksEnv) overwriteOnImport(t *testing.T) {
	t.Helper()
	e.put(t, map[string]any{"importConflictPolicy": "overwrite"})
}

func (e *checksEnv) put(t *testing.T, body map[string]any) {
	t.Helper()
	postJSONMethod[map[string]any](t, e.client, http.MethodPut, e.base+"/api/settings", body, http.StatusOK)
}

// grab asks for a release by hand and returns the queue item's id.
func (e *checksEnv) grab(t *testing.T, title, url string, size int64) int64 {
	t.Helper()
	r := postJSON[map[string]any](t, e.client, fmt.Sprintf("%s/api/movies/%d/grab", e.base, e.movie.ID), map[string]any{
		"releaseTitle": title, "downloadUrl": url, "sizeBytes": size,
	}, http.StatusAccepted)
	id, _ := r["queueId"].(float64)
	if id == 0 {
		t.Fatalf("no queue id in %+v", r)
	}
	return int64(id)
}

// finish waits for a queue item to leave the line and returns it.
func (e *checksEnv) finish(t *testing.T, id int64) queue.Item {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		it, err := e.server.QueueRepo.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		switch it.Status {
		case queue.StatusCompleted, queue.StatusFailed, queue.StatusStopped, queue.StatusConflict:
			return it
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("download %d did not finish in time", id)
	return queue.Item{}
}

func (e *checksEnv) movieStatus(t *testing.T) library.Status {
	t.Helper()
	m, err := e.server.MovieRepo.Get(e.movie.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m.Status
}

func (e *checksEnv) problemCodes(t *testing.T) []string {
	t.Helper()
	e.server.Problems.Flush() // the log is written in the background
	entries, _, err := e.server.Problems.List(problems.Filter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, p := range entries {
		codes = append(codes, p.Code)
	}
	return codes
}

func (e *checksEnv) activityHas(t *testing.T, kind, contains string) bool {
	t.Helper()
	entries, err := e.server.QueueRepo.ListActivity(200)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range entries {
		if a.EventType == kind && strings.Contains(a.Message, contains) {
			return true
		}
	}
	return false
}

var (
	movieFile = fixtureFile{"The.Fixture.Movie.1999.1080p.mkv", []byte("Fixture movie bytes. " + strings.Repeat("padding-", 200))}
	playerExe = fixtureFile{"Movie Player.exe", []byte("MZ pretend program")}
)

func TestReleaseWithAProgramInItIsRejectedBeforeDownloading(t *testing.T) {
	env := newChecksEnv(t)
	url := env.release("withexe", "exe", []fixtureFile{movieFile, playerExe})
	const title = "The.Fixture.Movie.1999.1080p.WEB-DL.x264-FIXTURE"
	id := env.grab(t, title, url, 1000)
	it := env.finish(t, id)

	if it.Status != queue.StatusFailed {
		t.Fatalf("status = %s, want failed", it.Status)
	}
	if !strings.Contains(it.Error, "programs or scripts") || !strings.Contains(it.Error, "Movie Player.exe") {
		t.Errorf("the person is told %q; want it to say what was found", it.Error)
	}
	if n := env.nntp.totalHits(); n != 0 {
		t.Errorf("%d articles were fetched; the check must come before any download", n)
	}
	// The item is marked failed a moment before the blocklist and the movie
	// are updated, so wait for those instead of reading them straight away.
	waitFor(t, "the release to be on the blocklist", func() bool {
		keys, _ := env.server.Blocklist.Keys()
		return keys[blocklist.Key(title)]
	})
	waitFor(t, "the movie to be missing again", func() bool { return env.movieStatus(t) == library.StatusMissing })
	found := false
	for _, c := range env.problemCodes(t) {
		if c == problems.CodeDownloadUnsafe {
			found = true
		}
	}
	if !found {
		t.Errorf("the problem log has %v, want a %s entry", env.problemCodes(t), problems.CodeDownloadUnsafe)
	}
	if !env.activityHas(t, "failed", "programs or scripts") || !env.activityHas(t, "blocklisted", title) {
		t.Error("activity should say the release was rejected and blocklisted")
	}
}

func TestProgramsAreAllowedWhenTheSwitchIsOff(t *testing.T) {
	env := newChecksEnv(t)
	env.put(t, map[string]any{"rejectExecutables": false})
	url := env.release("withexe", "exe", []fixtureFile{movieFile, playerExe})
	it := env.finish(t, env.grab(t, "The.Fixture.Movie.1999.1080p.WEB-DL.x264-FIXTURE", url, 1000))
	if it.Status != queue.StatusCompleted {
		t.Fatalf("status = %s (%s), want completed with the check off", it.Status, it.Error)
	}
}

func TestTheCheckIsOnByDefaultAndSwitchable(t *testing.T) {
	env := newChecksEnv(t)
	got := getJSON[map[string]any](t, env.client, env.base+"/api/settings")
	if got["rejectExecutables"] != true {
		t.Errorf("rejectExecutables = %v, want true by default", got["rejectExecutables"])
	}
	if got["skipDuplicates"] != false {
		t.Errorf("skipDuplicates = %v, want false by default", got["skipDuplicates"])
	}
	if got["usenetRetentionDays"] != float64(0) {
		t.Errorf("usenetRetentionDays = %v, want 0 by default", got["usenetRetentionDays"])
	}
	list, _ := got["unsafeExtensions"].([]any)
	if len(list) == 0 || !contains(list, ".exe") || !contains(list, ".scr") {
		t.Errorf("unsafeExtensions = %v, want the list the check uses", list)
	}
	env.put(t, map[string]any{"rejectExecutables": false, "skipDuplicates": true, "usenetRetentionDays": 1500})
	got = getJSON[map[string]any](t, env.client, env.base+"/api/settings")
	if got["rejectExecutables"] != false || got["skipDuplicates"] != true || got["usenetRetentionDays"] != float64(1500) {
		t.Errorf("settings did not stick: %v %v %v", got["rejectExecutables"], got["skipDuplicates"], got["usenetRetentionDays"])
	}
	for _, bad := range []int{-1, 10001} {
		postJSONMethod[map[string]any](t, env.client, http.MethodPut, env.base+"/api/settings", map[string]any{"usenetRetentionDays": bad}, http.StatusBadRequest)
	}
}

func contains(list []any, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestProgramInsideAnArchiveIsRejectedAfterUnpacking(t *testing.T) {
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for _, f := range []fixtureFile{movieFile, {"setup.exe", []byte("MZ pretend program")}} {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(f.data)
	}
	zw.Close()

	env := newChecksEnv(t)
	url := env.release("zipped", "zip", []fixtureFile{{"release.zip", zbuf.Bytes()}})
	const title = "The.Fixture.Movie.1999.1080p.WEB-DL.x264-ZIPPED"
	it := env.finish(t, env.grab(t, title, url, 1000))
	if it.Status != queue.StatusFailed || !strings.Contains(it.Error, "unpacked download contains programs or scripts") {
		t.Fatalf("status = %s error = %q; want it failed after unpacking", it.Status, it.Error)
	}
	if env.nntp.totalHits() == 0 {
		t.Error("the file list showed nothing, so the download should have run")
	}
	waitFor(t, "the release to be on the blocklist", func() bool {
		keys, _ := env.server.Blocklist.Keys()
		return keys[blocklist.Key(title)]
	})
	waitFor(t, "the movie to be missing again", func() bool { return env.movieStatus(t) == library.StatusMissing })
}

func TestDuplicateReleases(t *testing.T) {
	const first = "The.Fixture.Movie.1999.1080p.WEB-DL.x264-FIXTURE"
	tests := []struct {
		name        string
		setting     bool
		secondTitle string
		sameNZB     bool // the second release is the same post (same articles)
		deleteFile  bool // the movie's file is gone before the second try
		wantSkipped bool
	}{
		{"off: the same release again downloads again", false, first, true, false, false},
		{"on: same name and size is skipped", true, first, true, false, true},
		{"on: same NZB under another name is skipped", true, "The.Fixture.Movie.1999.1080p.BluRay.x264-OTHER", true, false, true},
		{"on: a different release is downloaded", true, "The.Fixture.Movie.1999.720p.WEB-DL.x264-OTHER", false, false, false},
		{"on: after the file is deleted it can be downloaded again", true, first, true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newChecksEnv(t)
			env.overwriteOnImport(t)
			env.put(t, map[string]any{"skipDuplicates": tt.setting})
			url1 := env.release("one", "dup", []fixtureFile{movieFile})
			one := env.finish(t, env.grab(t, first, url1, 1000))
			if one.Status != queue.StatusCompleted {
				t.Fatalf("first download: %s (%s)", one.Status, one.Error)
			}
			if tt.deleteFile {
				m, err := env.server.MovieRepo.Get(env.movie.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(m.FilePath); err != nil {
					t.Fatal(err)
				}
				if err := env.server.MovieRepo.SetStatus(env.movie.ID, library.StatusMissing, "", ""); err != nil {
					t.Fatal(err)
				}
			}
			prefix := "dup"
			files := []fixtureFile{movieFile}
			if !tt.sameNZB {
				prefix = "other"
				files = []fixtureFile{{movieFile.name, append([]byte("different "), movieFile.data...)}}
			}
			url2 := env.release("two", prefix, files)
			hitsBefore := env.nntp.totalHits()
			two := env.finish(t, env.grab(t, tt.secondTitle, url2, 1000))

			if tt.wantSkipped {
				if two.Status != queue.StatusFailed || !strings.HasPrefix(two.Error, "Skipped:") {
					t.Fatalf("second download: %s %q; want it skipped", two.Status, two.Error)
				}
				if env.nntp.totalHits() != hitsBefore {
					t.Error("a skipped duplicate must not fetch any article")
				}
				if got := env.movieStatus(t); got != library.StatusDownloaded {
					t.Errorf("movie is %s, want it left downloaded", got)
				}
				keys, _ := env.server.Blocklist.Keys()
				if len(keys) != 0 {
					t.Errorf("a duplicate is not a bad release, but the blocklist has %v", keys)
				}
				for _, c := range env.problemCodes(t) {
					t.Errorf("a duplicate is not a problem, but the log has %s", c)
				}
				if !env.activityHas(t, "skipped", "already in the queue or downloaded") && !env.activityHas(t, "skipped", "same NZB") {
					t.Error("activity should carry a plain note about the skipped duplicate")
				}
				return
			}
			if two.Status != queue.StatusCompleted {
				t.Fatalf("second download: %s (%s), want completed", two.Status, two.Error)
			}
		})
	}
}

// A download that failed once is not a duplicate of its own retry or of a later
// try at the same release.
func TestAFailedTryDoesNotMakeTheNextOneADuplicate(t *testing.T) {
	env := newChecksEnv(t)
	env.overwriteOnImport(t)
	env.put(t, map[string]any{"skipDuplicates": true})
	const title = "The.Fixture.Movie.1999.1080p.WEB-DL.x264-FIXTURE"
	bad := env.finish(t, env.grab(t, title, env.srv.URL+"/missing.nzb", 1000)) // 404: fails
	if bad.Status != queue.StatusFailed {
		t.Fatalf("setup: %s", bad.Status)
	}
	url := env.release("good", "good", []fixtureFile{movieFile})
	good := env.finish(t, env.grab(t, title, url, 1000))
	if good.Status != queue.StatusCompleted {
		t.Fatalf("second try: %s (%s), want completed", good.Status, good.Error)
	}
}

// --- Usenet retention ---

// newDatedIndexer is a Newznab indexer with one release posted postedDaysAgo
// days ago.
func newDatedIndexer(t *testing.T, postedDaysAgo int) *httptest.Server {
	t.Helper()
	posted := time.Now().UTC().Add(-time.Duration(postedDaysAgo) * 24 * time.Hour).Format(time.RFC1123Z)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
<channel>
<item>
<title>The.Fixture.Movie.1999.1080p.WEB-DL.x264-FIXTURE</title>
<guid>fixture-guid</guid>
<pubDate>%s</pubDate>
<enclosure url="http://%s/nzb/fixture.nzb" length="21474836480" type="application/x-nzb" />
<newznab:attr name="size" value="21474836480"/>
<newznab:attr name="category" value="2000"/>
</item>
</channel>
</rss>`, posted, r.Host)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRetentionInTheInteractiveListAndAutomaticSearch(t *testing.T) {
	tests := []struct {
		name          string
		postedDaysAgo int
		retention     int
		wantRejected  bool
	}{
		{"setting off: an old post is a normal result", 3000, 0, false},
		{"inside the retention", 200, 1000, false},
		{"older than the retention", 3000, 1000, true},
		{"just inside", 999, 1000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newChecksEnv(t)
			idx := newDatedIndexer(t, tt.postedDaysAgo)
			postJSON[map[string]any](t, env.client, env.base+"/api/indexers", map[string]any{
				"name": "Fixture Indexer", "definitionId": "fixture", "baseUrl": idx.URL, "apiKey": "fixture-key",
			}, http.StatusCreated)
			env.put(t, map[string]any{"usenetRetentionDays": tt.retention})

			// The interactive list still shows the release, with the reason.
			list := getJSON[[]map[string]any](t, env.client, fmt.Sprintf("%s/api/movies/%d/search", env.base, env.movie.ID))
			if len(list) != 1 {
				t.Fatalf("the list has %d results, want the release to stay in it: %+v", len(list), list)
			}
			reasons, _ := list[0]["rejections"].([]any)
			gotRejected := false
			for _, r := range reasons {
				if strings.Contains(fmt.Sprint(r), "Usenet retention") {
					gotRejected = true
				}
			}
			if gotRejected != tt.wantRejected {
				t.Errorf("rejections = %v, want a retention reason: %v", reasons, tt.wantRejected)
			}

			// The automatic search does not take it.
			res := postJSON[map[string]any](t, env.client, fmt.Sprintf("%s/api/movies/%d/search-now", env.base, env.movie.ID), map[string]any{}, http.StatusOK)
			grabbed := res["grabbed"] == float64(1)
			if grabbed == tt.wantRejected {
				t.Errorf("search now grabbed = %v with a post of %d days and retention %d (response %v)", grabbed, tt.postedDaysAgo, tt.retention, res)
			}
		})
	}
}
