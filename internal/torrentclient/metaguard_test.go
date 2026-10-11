package torrentclient_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/anacrolix/torrent"

	mediarium "github.com/rdborg/mediarium/internal/torrentclient"
)

// A .torrent file made of millions of nested lists must be turned away without
// the library's decoder ever seeing it (it would end the whole test binary).
func TestAddTorrentFileRefusesNestingBomb(t *testing.T) {
	bomb := filepath.Join(t.TempDir(), "bomb.torrent")
	if err := os.WriteFile(bomb, append([]byte("d1:x"), bytes.Repeat([]byte("l"), 6<<20)...), 0o600); err != nil {
		t.Fatal(err)
	}
	engine, err := mediarium.New(mediarium.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := engine.AddTorrentFileIn(ctx, bomb, t.TempDir()); err == nil {
		t.Fatal("AddTorrentFileIn accepted a nesting bomb")
	}
	if _, err := engine.AddTorrentFile(ctx, bomb); err == nil {
		t.Fatal("AddTorrentFile accepted a nesting bomb")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("refusing the files took %v", took)
	}
	if engine.Torrents() != 0 {
		t.Fatalf("engine runs %d torrents, want 0", engine.Torrents())
	}
}

// The metadata guard must not get in the way of a real magnet link: the engine
// has only the info hash, a seeder sends the file list, and the download
// completes.
func TestMagnetMetadataStillArrivesThroughTheGuard(t *testing.T) {
	content := []byte("magnet metadata fixture, repeated to fill a few pieces. ")
	for len(content) < 200_000 {
		content = append(content, content...)
	}
	seedDir := t.TempDir()
	file := filepath.Join(seedDir, "movie.mkv")
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatal(err)
	}
	mi := buildMetaInfo(t, file)

	seederCfg := torrent.NewDefaultClientConfig()
	seederCfg.DataDir = seedDir
	seederCfg.Seed = true
	seederCfg.NoDHT = true
	seederCfg.DisableTrackers = true
	seederCfg.ListenPort = 0
	seeder, err := torrent.NewClient(seederCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer seeder.Close()
	st, err := seeder.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if err := st.VerifyDataContext(ctx); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	engine, err := mediarium.New(mediarium.Config{DataDir: t.TempDir(), ListenPort: port})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	magnet := "magnet:?xt=urn:btih:" + mi.HashInfoBytes().HexString()
	type result struct {
		t   *torrent.Torrent
		err error
	}
	done := make(chan result, 1)
	go func() {
		tr, err := engine.AddMagnetIn(ctx, magnet, t.TempDir())
		done <- result{tr, err}
	}()

	// The engine knows the torrent by its hash as soon as the magnet is added;
	// from then on the seeder can connect to it and hand over the metadata.
	deadline := time.Now().Add(10 * time.Second)
	for engine.Torrents() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if engine.Torrents() == 0 {
		t.Fatal("the engine never started the magnet torrent")
	}
	st.AddPeers([]torrent.PeerInfo{{Addr: stringAddr(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))}})

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("magnet: %v", r.err)
		}
		if r.t.Info() == nil || r.t.Info().TotalLength() != int64(len(content)) {
			t.Fatal("the metadata that arrived does not match the torrent")
		}
	case <-ctx.Done():
		t.Fatal("the metadata never arrived")
	}
}
