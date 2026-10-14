package torrentclient_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"

	mediarium "github.com/rdborg/mediarium/internal/torrentclient"
)

const testPieceLen = 64 * 1024

// swarmFiles is the content of a small multi-file torrent, by path inside the
// torrent folder. The content is made so that no two pieces are alike.
func swarmFiles() map[string][]byte {
	gen := func(seed byte, n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = seed + byte(i/251) + byte(i%251)
		}
		return b
	}
	return map[string][]byte{
		"movie.mkv":         gen(1, 700_000),
		"movie.nfo":         gen(2, 300_000),
		"Sample/sample.mkv": gen(3, 300_000),
	}
}

type swarm struct {
	mi      *metainfo.MetaInfo
	torrent string // path of the .torrent file
	name    string // the folder name inside the torrent
	files   map[string][]byte
	addr    string // where the seeder listens
}

// newSwarm writes files into a folder, makes a torrent of it and seeds it from a
// plain engine on localhost. uploadLimit (bytes per second, 0 = none) slows the
// seeder, which makes a download last long enough to steer.
func newSwarm(t *testing.T, files map[string][]byte, uploadLimit int) *swarm {
	t.Helper()
	root := t.TempDir()
	name := "Release"
	for p, b := range files {
		full := filepath.Join(root, name, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: testPieceLen}
	if err := info.BuildFromFilePath(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if err := info.GeneratePieces(func(fi metainfo.FileInfo) (io.ReadCloser, error) {
		return os.Open(filepath.Join(append([]string{root, name}, fi.Path...)...))
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
	if err := st.VerifyDataContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	torrentFile := filepath.Join(t.TempDir(), "release.torrent")
	f, err := os.Create(torrentFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := mi.Write(f); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var addr string
	for _, a := range seeder.ListenAddrs() {
		if _, ok := a.(*net.TCPAddr); ok {
			addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(a.(*net.TCPAddr).Port))
		}
	}
	if addr == "" {
		t.Fatal("the seeder has no TCP address")
	}
	return &swarm{mi: mi, torrent: torrentFile, name: name, files: files, addr: addr}
}

// leech starts an engine that downloads the swarm's torrent into a new folder.
// peers says whether to point it at the seeder now.
func (s *swarm) leech(t *testing.T, peers bool) (*mediarium.Client, *torrent.Torrent, string) {
	t.Helper()
	dir := t.TempDir()
	engine, err := mediarium.New(mediarium.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close(); time.Sleep(100 * time.Millisecond) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tr, err := engine.AddTorrentFileIn(ctx, s.torrent, dir)
	if err != nil {
		t.Fatal(err)
	}
	if peers {
		s.connect(t, tr)
	}
	return engine, tr, dir
}

func (s *swarm) connect(t *testing.T, tr *torrent.Torrent) {
	t.Helper()
	if tr.AddPeers([]torrent.PeerInfo{{Addr: stringAddr(s.addr)}}) == 0 {
		t.Fatal("the seeder was not added as a peer")
	}
}

func paths(files []mediarium.FileState) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

func TestOnlyWantedFilesAreDownloaded(t *testing.T) {
	sw := newSwarm(t, swarmFiles(), 0)
	_, tr, dir := sw.leech(t, true)
	ctl := mediarium.NewControl(tr)

	all := ctl.Files()
	want := []string{"Release/Sample/sample.mkv", "Release/movie.mkv", "Release/movie.nfo"}
	if got := paths(all); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for _, f := range all {
		if !f.Wanted {
			t.Fatalf("%s should be wanted until something is left out", f.Path)
		}
	}

	if err := ctl.SetSkipped([]string{"Release/movie.nfo", "Release/Sample/sample.mkv", "not/in/the/torrent"}); err != nil {
		t.Fatal(err)
	}
	if got := ctl.Skipped(); len(got) != 2 {
		t.Fatalf("skipped = %v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var lastDone, lastTotal int64
	if err := ctl.Download(ctx, 0, func(d, total int64) { lastDone, lastTotal = d, total }); err != nil {
		t.Fatalf("download: %v", err)
	}
	if lastTotal != 700_000 || lastDone != lastTotal {
		t.Fatalf("progress ended at %d of %d, want 700000 of 700000", lastDone, lastTotal)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Release", "movie.mkv"))
	if err != nil || !bytes.Equal(got, sw.files["movie.mkv"]) {
		t.Fatalf("the wanted file is wrong or missing: %v", err)
	}
	// The two files that were left out are 600 kB: well over nine whole
	// pieces never needed to cross the wire.
	stats := tr.Stats()
	read := stats.BytesReadData.Int64()
	if read > 700_000+2*testPieceLen {
		t.Fatalf("%d bytes came over the wire for a 700000 byte file", read)
	}
}

// TestADiskThatRefusesDataIsNotAStall proves a torrent whose data cannot be
// written ends with ErrWriteFailed straight away, not with ErrStalled (which
// would blocklist a good release) hours later.
func TestADiskThatRefusesDataIsNotAStall(t *testing.T) {
	sw := newSwarm(t, swarmFiles(), 0)
	_, tr, dir := sw.leech(t, false)
	// A file where the torrent's folder has to go: every write fails.
	if err := os.WriteFile(filepath.Join(dir, sw.name), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctl := mediarium.NewControl(tr)
	sw.connect(t, tr)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := ctl.Download(ctx, time.Hour, nil)
	if !errors.Is(err, mediarium.ErrWriteFailed) {
		t.Fatalf("err = %v, want ErrWriteFailed", err)
	}
	if errors.Is(err, mediarium.ErrStalled) {
		t.Fatal("a write failure must not look like a stalled torrent")
	}
}

func TestLeavingEveryFileOutIsRefused(t *testing.T) {
	sw := newSwarm(t, swarmFiles(), 0)
	_, tr, _ := sw.leech(t, false)
	ctl := mediarium.NewControl(tr)
	err := ctl.SetSkipped([]string{"Release/movie.mkv", "Release/movie.nfo", "Release/Sample/sample.mkv"})
	if !errors.Is(err, mediarium.ErrNoFileWanted) {
		t.Fatalf("err = %v, want ErrNoFileWanted", err)
	}
	if len(ctl.Skipped()) != 0 {
		t.Fatal("a refused selection must change nothing")
	}
}

func TestAFileCanBeLeftOutWhileItDownloads(t *testing.T) {
	// A slow seeder, so there is time to change our mind.
	sw := newSwarm(t, swarmFiles(), 300_000)
	_, tr, _ := sw.leech(t, true)
	ctl := mediarium.NewControl(tr)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	go ctl.Run(ctx)

	finished := make(chan error, 1)
	go func() { finished <- ctl.Download(ctx, 0, nil) }()
	time.Sleep(700 * time.Millisecond)
	if err := ctl.SetSkipped([]string{"Release/movie.nfo", "Release/Sample/sample.mkv"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("download: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the download did not finish after the extra files were left out")
	}
	for _, f := range ctl.Files() {
		if f.Path == "Release/Sample/sample.mkv" && f.Done > f.Size*8/10 {
			t.Fatalf("%s was downloaded to %d of %d bytes although it was left out", f.Path, f.Done, f.Size)
		}
	}
}

func TestSequentialDownloadAsksForTheEndOfTheFileEarly(t *testing.T) {
	sw := newSwarm(t, map[string][]byte{"movie.mkv": swarmFiles()["movie.mkv"], "a.nfo": swarmFiles()["movie.nfo"]}, 250_000)
	_, tr, _ := sw.leech(t, false)
	ctl := mediarium.NewControl(tr)
	ctl.SetSequential(true)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	go ctl.Run(ctx)
	ctl.SetSkipped([]string{"Release/a.nfo"})

	// Without any peer nothing moves, so the priorities can be read off. a.nfo
	// comes first in the torrent, so movie.mkv starts after it.
	first := -1
	var last int
	for _, f := range tr.Files() {
		if f.Path() == "Release/movie.mkv" {
			first, last = f.BeginPieceIndex(), f.EndPieceIndex()-1
		}
	}
	waitUntil(t, "the first pieces to be asked for urgently", func() bool {
		return tr.PieceState(first).Priority == torrent.PiecePriorityNow &&
			tr.PieceState(first+2).Priority == torrent.PiecePriorityNext &&
			tr.PieceState(first+7).Priority == torrent.PiecePriorityReadahead &&
			tr.PieceState(last).Priority == torrent.PiecePriorityNext
	})

	// Switching it off puts the pieces back.
	ctl.SetSequential(false)
	waitUntil(t, "the pieces to go back to normal", func() bool {
		return tr.PieceState(first).Priority == torrent.PiecePriorityNormal && tr.PieceState(last).Priority == torrent.PiecePriorityNormal
	})

	// With a seeder and sequential on again, the last piece is among the first to arrive.
	ctl.SetSequential(true)
	sw.connect(t, tr)
	lastEarly := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		done := 0
		for i := first; i <= last; i++ {
			if tr.PieceState(i).Complete {
				done++
			}
		}
		if tr.PieceState(last).Complete && done <= 8 {
			lastEarly = true
			break
		}
		if done == last-first+1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !lastEarly {
		t.Fatal("the last piece should arrive among the first few pieces in a sequential download")
	}
}

func TestATorrentCanHaveItsOwnDownloadLimit(t *testing.T) {
	files := map[string][]byte{"movie.mkv": swarmFiles()["movie.mkv"]}
	fast := newSwarm(t, files, 2_000_000)
	_, tr, _ := fast.leech(t, true)
	ctl := mediarium.NewControl(tr)
	ctl.SetLimits(300_000, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	go ctl.Run(ctx)
	start := time.Now()
	if err := ctl.Download(ctx, 0, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	took := time.Since(start)
	// 700 kB at 300 kB/s is about 2.3 s; with no limit it takes 0.4 s here.
	if took < 1500*time.Millisecond {
		t.Fatalf("a download limited to 300 kB/s took only %v", took)
	}
}

// pattern makes n bytes in which no two pieces are alike.
func pattern(seed byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i/251) + byte(i%251)
	}
	return b
}

func TestATorrentCanHaveItsOwnUploadLimit(t *testing.T) {
	// A file big enough to last a few seconds at the limit.
	files := map[string][]byte{"movie.mkv": pattern(7, 4_000_000)}
	sw := newSwarm(t, files, 0)
	// The engine under test seeds; a plain client downloads from it.
	seedDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(seedDir, "Release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "Release", "movie.mkv"), files["movie.mkv"], 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := mediarium.New(mediarium.Config{DataDir: t.TempDir(), ListenPort: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close(); time.Sleep(100 * time.Millisecond) })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	seeding, err := engine.AddTorrentFileIn(ctx, sw.torrent, seedDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := seeding.VerifyDataContext(ctx); err != nil {
		t.Fatal(err)
	}
	ctl := mediarium.NewControl(seeding)
	ctl.SetLimits(0, 1_000_000)
	go ctl.Run(ctx)

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = t.TempDir()
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.NoDefaultPortForwarding = true
	cfg.ListenPort = 0
	leecher, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer leecher.Close()
	lt, err := leecher.AddTorrent(sw.mi)
	if err != nil {
		t.Fatal(err)
	}
	lt.AddPeers([]torrent.PeerInfo{{Addr: stringAddr(net.JoinHostPort("127.0.0.1", strconv.Itoa(engine.ListenPort())))}})
	start := time.Now()
	if err := mediarium.Download(ctx, lt, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	if took := time.Since(start); took < 2500*time.Millisecond {
		t.Fatalf("a seeder limited to 1 MB/s gave away 4 MB in %v", took)
	}
	t.Logf("4 MB went out in %v with the upload limit at 1 MB/s", time.Since(start))
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
