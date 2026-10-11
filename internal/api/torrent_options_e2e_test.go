package api_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"

	"github.com/rdborg/mediarium/internal/library"
)

func torrentBytes(seed byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i/251) + byte(i%251)
	}
	return b
}

// seedRelease seeds a release folder named "Release" with these files from a
// plain local engine, and returns a magnet link that names the seeder as a
// peer, so no tracker or DHT is needed.
func seedRelease(t *testing.T, files map[string][]byte, uploadLimit int) string {
	t.Helper()
	root := t.TempDir()
	for p, b := range files {
		full := filepath.Join(root, "Release", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 64 * 1024}
	if err := info.BuildFromFilePath(filepath.Join(root, "Release")); err != nil {
		t.Fatal(err)
	}
	if err := info.GeneratePieces(func(fi metainfo.FileInfo) (io.ReadCloser, error) {
		return os.Open(filepath.Join(append([]string{root, "Release"}, fi.Path...)...))
	}); err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{}
	mi.SetDefaults()
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi.InfoBytes = ib

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = root
	cfg.Seed = true
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.NoDefaultPortForwarding = true
	cfg.ListenPort = 0
	if uploadLimit > 0 {
		cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(uploadLimit), 64*1024)
	}
	seeder, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { seeder.Close(); time.Sleep(100 * time.Millisecond) })
	st, err := seeder.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := st.VerifyDataContext(ctx); err != nil {
		t.Fatal(err)
	}
	var port int
	for _, a := range seeder.ListenAddrs() {
		if tcp, ok := a.(*net.TCPAddr); ok {
			port = tcp.Port
		}
	}
	if port == 0 {
		t.Fatal("the seeder has no TCP port")
	}
	return fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=Release&x.pe=%s", mi.HashInfoBytes().HexString(), net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

func torrentMovieRelease() map[string][]byte {
	return map[string][]byte{
		"Torrent.Movie.2001.1080p.mkv": torrentBytes(1, 700_000),
		"Torrent.Movie.2001.nfo":       torrentBytes(2, 300_000),
		"Sample/sample.mkv":            torrentBytes(3, 300_000),
	}
}

func queueEntry(t *testing.T, client *http.Client, base string, id int64) map[string]any {
	t.Helper()
	for _, it := range getJSON[[]map[string]any](t, client, base+"/api/queue") {
		if int64(it["id"].(float64)) == id {
			return it
		}
	}
	t.Fatalf("queue item %d is not listed", id)
	return nil
}

func putJSON(t *testing.T, client *http.Client, url string, body any, want int) map[string]any {
	t.Helper()
	return postJSONMethod[map[string]any](t, client, http.MethodPut, url, body, want)
}

// TestBuiltInTorrentIsFiledSkippedAndImported follows a torrent from the grab
// to the library with the new controls at their defaults plus a category: the
// junk is left out, the download lands in its category's folder, the movie is
// imported, and the options are there to read.
func TestBuiltInTorrentIsFiledSkippedAndImported(t *testing.T) {
	server, base, client := newControlServer(t, false)
	t.Cleanup(func() { server.TestStopTorrents() })
	magnet := seedRelease(t, torrentMovieRelease(), 0)

	opts := putJSON(t, client, base+"/api/downloads/torrent-options", map[string]any{
		"categories":     []map[string]string{{"name": "Movies", "subfolder": "movies"}},
		"categoryByKind": map[string]string{"movie": "Movies"},
	}, http.StatusOK)
	if cats := opts["categories"].([]any); len(cats) != 1 {
		t.Fatalf("categories = %v", opts["categories"])
	}

	m, err := server.MovieRepo.Add(library.Movie{TMDBID: 77, Title: "Torrent Movie", Year: 2001, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := server.TestGrabMovieTorrent(m, "Torrent.Movie.2001.1080p.BluRay.x264-GRP", magnet)
	if err != nil {
		t.Fatal(err)
	}
	done := waitForQueueStatus(t, client, base, "completed")
	if int64(done["id"].(float64)) != id {
		t.Fatalf("unexpected queue item: %v", done)
	}

	// Imported, with the right content.
	got, err := server.MovieRepo.Get(m.ID)
	if err != nil || got.FilePath == "" {
		t.Fatalf("the movie was not imported: %+v, %v", got, err)
	}
	if b, err := os.ReadFile(got.FilePath); err != nil || len(b) != 700_000 {
		t.Fatalf("imported file: %d bytes, %v", len(b), err)
	}

	// Filed under the category, and shown so.
	if dir := server.TestWorkDirFor(id); !strings.Contains(filepath.ToSlash(dir), "/incomplete/movies/queue-") {
		t.Fatalf("working folder = %s", dir)
	}
	if entry := queueEntry(t, client, base, id); entry["category"] != "Movies" {
		t.Fatalf("queue entry = %v", entry)
	}

	// The junk was left out and the options say so.
	detail := getJSON[map[string]any](t, client, base+fmt.Sprintf("/api/queue/%d/torrent", id))
	if detail["category"] != "Movies" || detail["sequential"] != "default" || detail["live"] != true {
		t.Fatalf("detail = %v", detail)
	}
	wanted := map[string]bool{}
	for _, f := range detail["files"].([]any) {
		fm := f.(map[string]any)
		wanted[fm["path"].(string)] = fm["wanted"].(bool)
	}
	if len(wanted) != 3 || !wanted["Release/Torrent.Movie.2001.1080p.mkv"] || wanted["Release/Torrent.Movie.2001.nfo"] || wanted["Release/Sample/sample.mkv"] {
		t.Fatalf("files = %v", wanted)
	}

	// Per-torrent choices are kept and read back.
	detail = putJSON(t, client, base+fmt.Sprintf("/api/queue/%d/torrent", id), map[string]any{"sequential": "on", "downLimitKB": 500, "upLimitKB": 40}, http.StatusOK)
	if detail["sequential"] != "on" || detail["downLimitKB"] != float64(500) || detail["upLimitKB"] != float64(40) {
		t.Fatalf("detail after the change = %v", detail)
	}
	putJSON(t, client, base+fmt.Sprintf("/api/queue/%d/torrent", id), map[string]any{"sequential": "sometimes"}, http.StatusBadRequest)
	putJSON(t, client, base+fmt.Sprintf("/api/queue/%d/torrent", id), map[string]any{"downLimitKB": -5}, http.StatusBadRequest)
	// Files can only be changed while it downloads.
	putJSON(t, client, base+fmt.Sprintf("/api/queue/%d/torrent/files", id), map[string]any{"skip": []string{}}, http.StatusConflict)
}

// TestFilesCanBeTickedWhileATorrentDownloads changes the file selection of a
// torrent that is still coming in, through the API.
func TestFilesCanBeTickedWhileATorrentDownloads(t *testing.T) {
	server, base, client := newControlServer(t, false)
	t.Cleanup(func() { server.TestStopTorrents() })
	magnet := seedRelease(t, torrentMovieRelease(), 250_000)

	m, err := server.MovieRepo.Add(library.Movie{TMDBID: 78, Title: "Slow Movie", Year: 2002, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := server.TestGrabMovieTorrent(m, "Slow.Movie.2002.1080p.BluRay.x264-GRP", magnet)
	if err != nil {
		t.Fatal(err)
	}
	url := base + fmt.Sprintf("/api/queue/%d/torrent", id)
	waitFor(t, "the torrent to be added with its files", func() bool {
		d := getJSON[map[string]any](t, client, url)
		return d["live"] == true && d["filesOpen"] == true && len(d["files"].([]any)) == 3
	})

	// The sample is wanted after all.
	d := putJSON(t, client, url+"/files", map[string]any{"skip": []string{"Release/Torrent.Movie.2001.nfo"}}, http.StatusOK)
	wanted := map[string]bool{}
	for _, f := range d["files"].([]any) {
		fm := f.(map[string]any)
		wanted[fm["path"].(string)] = fm["wanted"].(bool)
	}
	if !wanted["Release/Sample/sample.mkv"] || wanted["Release/Torrent.Movie.2001.nfo"] {
		t.Fatalf("files after ticking the sample = %v", wanted)
	}
	// Every file unticked is refused and changes nothing.
	putJSON(t, client, url+"/files", map[string]any{"skip": []string{"Release/Torrent.Movie.2001.nfo", "Release/Sample/sample.mkv", "Release/Torrent.Movie.2001.1080p.mkv"}}, http.StatusBadRequest)
	if skipped := server.TestTorrentSkipped(id); len(skipped) != 1 {
		t.Fatalf("a refused selection changed the files: %v", skipped)
	}

	waitForQueueStatus(t, client, base, "completed")
	got, err := server.MovieRepo.Get(m.ID)
	if err != nil || got.FilePath == "" {
		t.Fatalf("the movie was not imported: %+v, %v", got, err)
	}
	if b, err := os.ReadFile(got.FilePath); err != nil || len(b) != 700_000 {
		t.Fatalf("the movie, not the sample, has to be imported: %d bytes, %v", len(b), err)
	}
}

func TestTorrentOptionsAreValidatedAndKept(t *testing.T) {
	_, base, client := loginNewServer(t)
	opts := getJSON[map[string]any](t, client, base+"/api/downloads/torrent-options")
	if opts["sequentialDefault"] != false || opts["skipJunk"] != true || opts["packOnlyWanted"] != false || opts["uploadLimitKB"] != float64(0) {
		t.Fatalf("defaults = %v", opts)
	}
	pm := opts["portMapping"].(map[string]any)
	if pm["enabled"] != true {
		t.Fatalf("port mapping is on by default (as the engine always did): %v", pm)
	}

	opts = putJSON(t, client, base+"/api/downloads/torrent-options", map[string]any{
		"sequentialDefault": true, "packOnlyWanted": true, "skipJunk": false, "uploadLimitKB": 250, "portMapping": false,
	}, http.StatusOK)
	if opts["sequentialDefault"] != true || opts["packOnlyWanted"] != true || opts["skipJunk"] != false || opts["uploadLimitKB"] != float64(250) || opts["portMapping"].(map[string]any)["state"] != "off" {
		t.Fatalf("saved = %v", opts)
	}
	again := getJSON[map[string]any](t, client, base+"/api/downloads/torrent-options")
	if again["uploadLimitKB"] != float64(250) || again["sequentialDefault"] != true {
		t.Fatalf("read back = %v", again)
	}

	// Bad input is refused with a plain reason and nothing changes.
	for name, body := range map[string]map[string]any{
		"negative upload limit": {"uploadLimitKB": -1},
		"huge upload limit":     {"uploadLimitKB": 99999999999},
		"folder with a slash":   {"categories": []map[string]string{{"name": "A", "subfolder": "a/b"}}},
		"two with one name":     {"categories": []map[string]string{{"name": "A"}, {"name": "a"}}},
		"kind to nowhere":       {"categories": []map[string]string{{"name": "A"}}, "categoryByKind": map[string]string{"movie": "B"}},
	} {
		got := putJSON(t, client, base+"/api/downloads/torrent-options", body, http.StatusBadRequest)
		if msg, _ := got["error"].(string); msg == "" {
			t.Errorf("%s: no reason given: %v", name, got)
		}
	}
	if after := getJSON[map[string]any](t, client, base+"/api/downloads/torrent-options"); after["uploadLimitKB"] != float64(250) {
		t.Fatalf("a refused request changed something: %v", after)
	}
}

func TestIndexerSeedRulesCanBeSavedAndCleared(t *testing.T) {
	_, base, client := loginNewServer(t)
	created := postJSON[map[string]any](t, client, base+"/api/indexers", map[string]any{
		"name": "Private tracker", "baseUrl": "https://tracker.example", "protocol": "torrent", "apiKey": "fake-key",
	}, http.StatusCreated)
	id := int64(created["id"].(float64))
	url := base + fmt.Sprintf("/api/indexers/%d/seed-rules", id)

	if rules := getJSON[map[string]any](t, client, base+"/api/indexers/seed-rules"); len(rules) != 0 {
		t.Fatalf("no rules yet: %v", rules)
	}
	rule := putJSON(t, client, url, map[string]any{"ratio": 1.5, "hours": 72}, http.StatusOK)
	if rule["ratio"] != 1.5 || rule["hours"] != float64(72) {
		t.Fatalf("rule = %v", rule)
	}
	rules := getJSON[map[string]any](t, client, base+"/api/indexers/seed-rules")
	if r, ok := rules[strconv.FormatInt(id, 10)].(map[string]any); !ok || r["ratio"] != 1.5 {
		t.Fatalf("rules = %v", rules)
	}
	putJSON(t, client, url, map[string]any{"ratio": -2}, http.StatusBadRequest)
	putJSON(t, client, base+"/api/indexers/9999/seed-rules", map[string]any{"ratio": 1}, http.StatusNotFound)

	putJSON(t, client, url, map[string]any{"ratio": nil, "hours": nil}, http.StatusOK)
	if rules := getJSON[map[string]any](t, client, base+"/api/indexers/seed-rules"); len(rules) != 0 {
		t.Fatalf("an empty rule should remove it: %v", rules)
	}
}
