package bencsafe_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/rdborg/mediarium/internal/bencsafe"
)

func TestPrefix(t *testing.T) {
	nested := func(n int) string { return strings.Repeat("l", n) + strings.Repeat("e", n) }
	tests := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{"empty", "", 0, true},
		{"integer", "i42e", 4, false},
		{"negative integer", "i-7e", 4, false},
		{"string", "4:spam", 6, false},
		{"empty string", "0:", 2, false},
		{"list", "l4:spami3ee", 11, false},
		{"empty list", "le", 2, false},
		{"dictionary", "d3:cow3:moo4:spam4:eggse", 24, false},
		{"nested", "d1:ald1:bi1eeee", 15, false},
		{"stops after the first value", "i1eGARBAGE", 3, false},
		{"header before raw data", "d8:msg_typei1e5:piecei0eeXXXXXXXX", 25, false},
		{"depth at the limit", nested(bencsafe.MaxDepth), 2 * bencsafe.MaxDepth, false},
		{"one level too deep", nested(bencsafe.MaxDepth + 1), 0, true},
		{"unterminated list", "l4:spam", 0, true},
		{"unterminated integer", "i42", 0, true},
		{"integer without digits", "ie", 0, true},
		{"integer with letters", "i4x2e", 0, true},
		{"string runs past the end", "10:abc", 0, true},
		{"string length without colon", "12", 0, true},
		{"huge string length", "99999999999:a", 0, true},
		{"end marker alone", "e", 0, true},
		{"dictionary key that is a number", "di1ei2ee", 0, true},
		{"dictionary key that is a list", "dlee", 0, true},
		{"dictionary key without a value", "d3:cowe", 0, true},
		{"unknown first byte", "x", 0, true},
		{"number that never ends", "i" + strings.Repeat("9", 100) + "e", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bencsafe.Prefix([]byte(tc.in))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Prefix(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("Prefix(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestTooDeepIsTheNamedError(t *testing.T) {
	err := bencsafe.Check([]byte(strings.Repeat("l", bencsafe.MaxDepth+1)))
	if !errors.Is(err, bencsafe.ErrTooDeep) {
		t.Fatalf("error = %v, want ErrTooDeep", err)
	}
}

// The input that kills the torrent library: a dictionary whose value is a list
// nested millions of levels deep. The library runs out of stack on it and the
// process dies, so this test must never hand it to the library.
func TestCrashInputIsRefusedQuickly(t *testing.T) {
	crash := append([]byte("d1:x"), bytes.Repeat([]byte("l"), 6<<20)...)
	start := time.Now()
	err := bencsafe.Check(crash)
	took := time.Since(start)
	if !errors.Is(err, bencsafe.ErrTooDeep) {
		t.Fatalf("error = %v, want ErrTooDeep", err)
	}
	if took > time.Second {
		t.Fatalf("refusing the input took %v", took)
	}

	// The same with dictionaries inside dictionaries, and with a list that
	// closes correctly: depth is what counts.
	dicts := bytes.Repeat([]byte("d1:x"), 1<<20)
	if err := bencsafe.Check(dicts); !errors.Is(err, bencsafe.ErrTooDeep) {
		t.Fatalf("nested dictionaries: error = %v, want ErrTooDeep", err)
	}
	closed := append(bytes.Repeat([]byte("l"), 1<<20), bytes.Repeat([]byte("e"), 1<<20)...)
	if err := bencsafe.Check(closed); !errors.Is(err, bencsafe.ErrTooDeep) {
		t.Fatalf("closed nesting: error = %v, want ErrTooDeep", err)
	}
}

// Real torrents, made the way the rest of the tests make them, still pass and
// still load.
func TestRealTorrentsStillLoad(t *testing.T) {
	info := metainfo.Info{
		PieceLength: 16384,
		Name:        "Some Movie (2020)",
		Length:      3 * 16384,
		Pieces:      bytes.Repeat([]byte{0x6c, 0x64, 0x01, 0x02, 0x03}, 12),
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{
		InfoBytes:    infoBytes,
		Announce:     "http://tracker.invalid/announce",
		AnnounceList: metainfo.AnnounceList{{"http://tracker.invalid/announce"}},
		Comment:      strings.Repeat("ld", 40),
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if err := bencsafe.Check(buf.Bytes()); err != nil {
		t.Fatalf("Check on a real torrent: %v", err)
	}
	if n, err := bencsafe.Prefix(buf.Bytes()); err != nil || n != buf.Len() {
		t.Fatalf("Prefix = %d, %v; want %d, nil", n, err, buf.Len())
	}
	if _, err := metainfo.Load(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("metainfo.Load: %v", err)
	}

	// A multi-file torrent with a folder tree, the deepest real shape.
	multi := metainfo.Info{
		PieceLength: 16384,
		Name:        "Show",
		Pieces:      bytes.Repeat([]byte{7}, 20),
		Files: []metainfo.FileInfo{
			{Length: 100, Path: []string{"Season 1", "Disc 1", "Extras", "ep1.mkv"}},
			{Length: 200, Path: []string{"Season 1", "ep2.mkv"}},
		},
	}
	multiBytes, err := bencode.Marshal(multi)
	if err != nil {
		t.Fatal(err)
	}
	if err := bencsafe.Check(multiBytes); err != nil {
		t.Fatalf("Check on a multi-file info dictionary: %v", err)
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good.torrent")
	if err := os.WriteFile(good, []byte("d4:name3:abce"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := bencsafe.ReadFile(good)
	if err != nil || string(data) != "d4:name3:abce" {
		t.Fatalf("ReadFile good = %q, %v", data, err)
	}

	bomb := filepath.Join(dir, "bomb.torrent")
	if err := os.WriteFile(bomb, append([]byte("d1:x"), bytes.Repeat([]byte("l"), 6<<20)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := bencsafe.ReadFile(bomb); !errors.Is(err, bencsafe.ErrTooDeep) {
		t.Fatalf("ReadFile bomb error = %v, want ErrTooDeep", err)
	}

	if _, err := bencsafe.ReadFile(filepath.Join(dir, "missing.torrent")); err == nil {
		t.Fatal("ReadFile of a missing file must fail")
	}
}

func TestLooksLikeNestingBomb(t *testing.T) {
	random := make([]byte, 16384) // stands in for the piece hashes: any byte value
	for i := range random {
		random[i] = byte(i*131 + i/7)
	}
	text := []byte(strings.Repeat("Planet of the Apes (1968) - Director's Cut - Dolby Digital.mkv", 300))
	if len(text) > 16384 {
		text = text[:16384]
	}
	tests := []struct {
		name  string
		chunk []byte
		want  bool
	}{
		{"hash data", random, false},
		{"file names", text, false},
		{"all lists", bytes.Repeat([]byte("l"), 16384), true},
		{"all dictionaries", bytes.Repeat([]byte("d"), 16384), true},
		{"lists with strings between", bytes.Repeat([]byte("l1:a"), 4096), true},
		{"a short run is nothing", bytes.Repeat([]byte("l"), 500), false},
		{"empty", nil, false},
	}
	for _, tc := range tests {
		if got := bencsafe.LooksLikeNestingBomb(tc.chunk); got != tc.want {
			t.Errorf("%s: LooksLikeNestingBomb = %v, want %v", tc.name, got, tc.want)
		}
	}
}
