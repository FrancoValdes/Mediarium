package subtitles

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEmbeddedSubtitlesInRealFiles(t *testing.T) {
	// Made with ffmpeg: one second of black video plus subtitle tracks.
	tests := []struct {
		file string
		want []EmbeddedTrack
	}{
		{"embedded.mkv", []EmbeddedTrack{
			{Lang: "en", Codec: "S_TEXT/UTF8"},
			{Lang: "de", Codec: "S_TEXT/UTF8", Forced: true},
			{Lang: "fr", Codec: "S_TEXT/UTF8", Name: "Forced", Forced: true},
			{Lang: "", Codec: "S_TEXT/UTF8"},
		}},
		// moov after the video, as ffmpeg writes it by default.
		{"embedded.mp4", []EmbeddedTrack{{Lang: "es", Codec: "tx3g"}, {Lang: "en", Codec: "tx3g"}}},
		// moov first ("fast start").
		{"embedded-faststart.m4v", []EmbeddedTrack{{Lang: "es", Codec: "tx3g"}, {Lang: "en", Codec: "tx3g"}}},
		{"nosubs.mkv", nil},
	}
	for _, tt := range tests {
		got, err := EmbeddedSubtitles(filepath.Join("testdata", tt.file))
		if err != nil {
			t.Fatalf("%s: %v", tt.file, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tt.file, got, tt.want)
		}
	}
}

func TestEmbeddedLanguagesLeavesOutForcedAndCaches(t *testing.T) {
	dir := t.TempDir()
	src, _ := os.ReadFile(filepath.Join("testdata", "embedded.mkv"))
	p := filepath.Join(dir, "Film (2020).mkv")
	os.WriteFile(p, src, 0o644)
	if got := EmbeddedLanguages(p); !reflect.DeepEqual(got, map[string]bool{"en": true}) {
		t.Fatalf("languages: %v", got)
	}
	// Replaced by a file without subtitles: the cache notices.
	nosubs, _ := os.ReadFile(filepath.Join("testdata", "nosubs.mkv"))
	os.WriteFile(p, nosubs, 0o644)
	if got := EmbeddedLanguages(p); len(got) != 0 {
		t.Fatalf("after replacing the file: %v", got)
	}
	// Not a video Mediarium reads: nothing, without opening it.
	txt := filepath.Join(dir, "notes.txt")
	os.WriteFile(txt, src, 0o644)
	if got := EmbeddedLanguages(txt); len(got) != 0 {
		t.Fatalf("a .txt: %v", got)
	}
}

// ebml builds one Matroska element.
func ebml(id uint64, payload ...[]byte) []byte {
	var idb []byte
	for v := id; v > 0; v >>= 8 {
		idb = append([]byte{byte(v)}, idb...)
	}
	body := bytes.Join(payload, nil)
	n := len(body)
	size := []byte{0x08, 0, 0, 0, 0, 0, 0, 0} // an 8-byte size, the simplest to write
	for i := 7; i >= 1; i-- {
		size[i] = byte(n)
		n >>= 8
	}
	size[0] = 0x01
	return append(append(idb, size...), body...)
}

func u(v uint64) []byte {
	var b []byte
	for ; v > 0; v >>= 8 {
		b = append([]byte{byte(v)}, b...)
	}
	if len(b) == 0 {
		b = []byte{0}
	}
	return b
}

func subTrack(lang, bcp47 string, forced bool) []byte {
	parts := [][]byte{ebml(idTrackType, u(trackSubtitle)), ebml(idCodecID, []byte("S_HDMV/PGS"))}
	if lang != "" {
		parts = append(parts, ebml(idLanguage, []byte(lang)))
	}
	if bcp47 != "" {
		parts = append(parts, ebml(idLanguageBCP47, []byte(bcp47)))
	}
	if forced {
		parts = append(parts, ebml(idFlagForced, u(1)))
	}
	return ebml(idTrackEntry, parts...)
}

func TestEmbeddedSubtitlesFindsTracksThroughTheSeekHead(t *testing.T) {
	tracks := ebml(idTracks,
		ebml(idTrackEntry, ebml(idTrackType, u(1)), ebml(idCodecID, []byte("V_MPEG4/ISO/AVC"))),
		subTrack("por", "pt-BR", false),
		subTrack("chi", "zh-Hant", false),
		subTrack("", "", false), // no Language element: English, Matroska's default
		subTrack("eng", "", true),
		subTrack("tlh", "", false), // a code Mediarium doesn't know is kept as it is
	)
	cluster := ebml(idCluster, ebml(0xE7, u(0)), bytes.Repeat([]byte{0}, 500))
	// SeekHead, then the video, then the tracks: the SeekHead has to be read.
	seekHeadFor := func(at uint64) []byte {
		return ebml(idSeekHead, ebml(idSeek, ebml(idSeekID, u(idTracks)), ebml(idSeekPosition, []byte{0, 0, 0, 0, byte(at >> 24), byte(at >> 16), byte(at >> 8), byte(at)})))
	}
	head := seekHeadFor(0)
	segment := append(append(seekHeadFor(uint64(len(head)+len(cluster))), cluster...), tracks...)
	file := append(ebml(idEBML, ebml(0x4282, []byte("matroska"))), ebml(idSegment, segment)...)

	p := filepath.Join(t.TempDir(), "late-tracks.mkv")
	os.WriteFile(p, file, 0o644)
	got, err := EmbeddedSubtitles(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []EmbeddedTrack{
		{Lang: "pt-BR", Codec: "S_HDMV/PGS"},
		{Lang: "zh-TW", Codec: "S_HDMV/PGS"},
		{Lang: "en", Codec: "S_HDMV/PGS"},
		{Lang: "en", Codec: "S_HDMV/PGS", Forced: true},
		{Lang: "tlh", Codec: "S_HDMV/PGS"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestEmbeddedSubtitlesSurvivesBrokenFiles(t *testing.T) {
	real, _ := os.ReadFile(filepath.Join("testdata", "embedded.mkv"))
	mp4, _ := os.ReadFile(filepath.Join("testdata", "embedded.mp4"))
	tests := map[string][]byte{
		"empty":               nil,
		"text":                []byte("this is not a video at all"),
		"mkv cut short":       real[:len(real)/3],
		"mkv cut in header":   real[:20],
		"mp4 cut short":       mp4[:len(mp4)/2],
		"mp4 box size 0 or 1": append([]byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 0, 0, 0, 1, 'm', 'o', 'o', 'v'}, bytes.Repeat([]byte{0xFF}, 8)...),
		"huge track list":     append(ebml(idEBML), append([]byte{0x18, 0x53, 0x80, 0x67, 0x01, 0, 0, 0, 0, 0, 0x10, 0}, 0x16, 0x54, 0xAE, 0x6B, 0x01, 0x7F, 0, 0, 0, 0, 0, 0)...),
	}
	for name, data := range tests {
		p := filepath.Join(t.TempDir(), "x.mkv")
		os.WriteFile(p, data, 0o644)
		got, _ := EmbeddedSubtitles(p) // must not panic or hang; anything found is a bonus
		if name == "empty" || name == "text" {
			if len(got) != 0 {
				t.Errorf("%s: %v", name, got)
			}
		}
	}
}

func FuzzEmbeddedSubtitles(f *testing.F) {
	for _, name := range []string{"embedded.mkv", "embedded.mp4", "nosubs.mkv"} {
		b, _ := os.ReadFile(filepath.Join("testdata", name))
		f.Add(b)
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		p := filepath.Join(dir, "f.mkv")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Skip()
		}
		EmbeddedSubtitles(p)
	})
}
