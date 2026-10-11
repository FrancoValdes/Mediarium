package music_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/music"
)

const lrcText = "[ar:Someone]\n[00:01.50]First line\n[00:05.00]Second line\n[01:02.123]Third line\n"

func TestIsSyncedLyrics(t *testing.T) {
	for text, want := range map[string]bool{
		lrcText:                              true,
		"[00:01.00]one\n[00:02.00]two":       true,
		"[00:01.00]only one stamp":           false,
		"Just words\nAnd more words":         false,
		"":                                   false,
		"A line [00:01.00] in the middle\nx": false,
		"[1:02]a\n[1:05]b":                   true,
	} {
		if got := music.IsSyncedLyrics(text); got != want {
			t.Errorf("IsSyncedLyrics(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestSidecarLyrics(t *testing.T) {
	if got := music.SidecarName("Artist/Album/01 - Song.flac"); got != "Artist/Album/01 - Song.lrc" {
		t.Fatalf("sidecar name %q", got)
	}
	l, err := music.SidecarLyrics(strings.NewReader("\ufeff" + strings.ReplaceAll(lrcText, "\n", "\r\n")))
	if err != nil || l.Source != "sidecar" || !strings.HasPrefix(l.Synced, "[ar:Someone]") || l.Plain != "" || strings.Contains(l.Synced, "\r") {
		t.Fatalf("synced sidecar: %+v %v", l, err)
	}
	l, _ = music.SidecarLyrics(strings.NewReader("Plain words\nmore"))
	if l.Plain != "Plain words\nmore" || l.Synced != "" {
		t.Fatalf("plain sidecar: %+v", l)
	}
	if l, _ := music.SidecarLyrics(strings.NewReader("  \n ")); l.Has() || l.Source != "" {
		t.Fatalf("empty sidecar: %+v", l)
	}
}

func TestEmbeddedLyrics(t *testing.T) {
	flac := buildFLAC(flacSpec{sampleRate: 44100, channels: 2, bps: 16, seconds: 5, audioBytes: 50,
		comments: []string{"TITLE=x", "LYRICS=" + strings.ReplaceAll(lrcText, "\n", "\r\n")}})
	l, err := music.EmbeddedLyrics(bytes.NewReader(flac))
	if err != nil || l.Source != "file" || l.Synced == "" {
		t.Fatalf("vorbis synced: %+v %v", l, err)
	}

	flac = buildFLAC(flacSpec{sampleRate: 44100, channels: 2, bps: 16, seconds: 5, audioBytes: 50,
		comments: []string{"UNSYNCEDLYRICS=La la la\nLa la"}})
	if l, _ := music.EmbeddedLyrics(bytes.NewReader(flac)); l.Plain != "La la la\nLa la" || l.Synced != "" {
		t.Fatalf("vorbis plain: %+v", l)
	}

	// Plain and synced both in the file: both are kept.
	flac = buildFLAC(flacSpec{sampleRate: 44100, channels: 2, bps: 16, seconds: 5, audioBytes: 50,
		comments: []string{"LYRICS=Words only", "SYNCEDLYRICS=[00:01.00]a\n[00:02.00]b"}})
	if l, _ := music.EmbeddedLyrics(bytes.NewReader(flac)); l.Plain != "Words only" || !strings.HasPrefix(l.Synced, "[00:01.00]a") {
		t.Fatalf("both: %+v", l)
	}

	if l, err := music.EmbeddedLyrics(bytes.NewReader(bytes.Repeat([]byte("x"), 300))); err != nil || l.Has() {
		t.Fatalf("no tags: %+v %v", l, err)
	}
}

func TestCachedLyricsKeepAnswersAndMisses(t *testing.T) {
	e := newPlaylistEnv(t)
	id := e.tracks[0].ID
	if _, ok, err := e.repo.GetCachedLyrics(id); ok || err != nil {
		t.Fatalf("empty cache: %v %v", ok, err)
	}
	if err := e.repo.PutCachedLyrics(id, music.Lyrics{Source: "lrclib", Synced: lrcText, Plain: "words"}, true); err != nil {
		t.Fatal(err)
	}
	c, ok, err := e.repo.GetCachedLyrics(id)
	if err != nil || !ok || !c.Found || c.Synced != lrcText || c.Plain != "words" || c.Source != "lrclib" {
		t.Fatalf("cached: %+v %v %v", c, ok, err)
	}
	if !c.Fresh(time.Now().Add(400 * 24 * time.Hour)) {
		t.Error("lyrics that were found are kept for good")
	}
	if err := e.repo.PutCachedLyrics(id, music.Lyrics{Source: "lrclib"}, false); err != nil {
		t.Fatal(err)
	}
	c, _, _ = e.repo.GetCachedLyrics(id)
	if c.Found || c.Synced != "" || c.FetchedAt.IsZero() {
		t.Fatalf("a miss replaces the answer: %+v", c)
	}
	if !c.Fresh(time.Now().Add(24*time.Hour)) || c.Fresh(time.Now().Add(music.LyricsMissRetry+time.Hour)) {
		t.Error("a miss is believed for a month, then the track is asked again")
	}
}
