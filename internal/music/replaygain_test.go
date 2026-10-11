package music_test

import (
	"bytes"
	"math"
	"testing"

	"github.com/dhowden/tag"
	"github.com/rdborg/mediarium/internal/music"
)

func near(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < 1e-9 }

func TestReplayGainFromRaw(t *testing.T) {
	tests := []struct {
		name                       string
		raw                        map[string]interface{}
		trackGain, trackPeak       *float64
		albumGain, albumPeak       *float64
		wantTG, wantTP, wantAG, wP float64
		empty                      bool
	}{
		{name: "vorbis comments in lower case", raw: map[string]interface{}{
			"replaygain_track_gain": "-6.54 dB", "replaygain_track_peak": "0.987654", "replaygain_album_gain": "-7.10 dB", "replaygain_album_peak": "1.000000",
		}, wantTG: -6.54, wantTP: 0.987654, wantAG: -7.10, wP: 1},
		{name: "mp4 freeform atoms in capitals", raw: map[string]interface{}{
			"REPLAYGAIN_TRACK_GAIN": "+3.20 dB", "REPLAYGAIN_TRACK_PEAK": "0.5",
		}, wantTG: 3.2, wantTP: 0.5},
		{name: "id3 TXXX frames", raw: map[string]interface{}{
			"TXXX":   &tag.Comm{Description: "REPLAYGAIN_TRACK_GAIN", Text: "-1.5 dB"},
			"TXXX_0": &tag.Comm{Description: "replaygain_album_gain", Text: "-2dB"},
			"TXXX_1": &tag.Comm{Description: "MusicBrainz Album Id", Text: "x"},
		}, wantTG: -1.5, wantAG: -2},
		{name: "opus R128 is converted to ReplayGain 2 level", raw: map[string]interface{}{
			"r128_track_gain": "-512", "r128_album_gain": "256",
		}, wantTG: 3, wantAG: 6},
		{name: "ReplayGain wins over R128", raw: map[string]interface{}{
			"replaygain_track_gain": "-4 dB", "r128_track_gain": "-512",
		}, wantTG: -4},
		{name: "garbage and negative peaks are ignored", raw: map[string]interface{}{
			"replaygain_track_gain": "loud", "replaygain_track_peak": "-1", "replaygain_album_gain": "NaN", "title": "x",
		}, empty: true},
		{name: "no tags", raw: map[string]interface{}{}, empty: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := music.ReplayGainFromRaw(tc.raw)
			if g.Empty() != tc.empty {
				t.Fatalf("empty = %v, got %+v", g.Empty(), g)
			}
			for label, c := range map[string]struct {
				got  *float64
				want float64
			}{"track gain": {g.TrackGain, tc.wantTG}, "track peak": {g.TrackPeak, tc.wantTP}, "album gain": {g.AlbumGain, tc.wantAG}, "album peak": {g.AlbumPeak, tc.wP}} {
				if c.want == 0 {
					if c.got != nil {
						t.Errorf("%s should be missing, got %v", label, *c.got)
					}
				} else if !near(c.got, c.want) {
					t.Errorf("%s = %v, want %v", label, c.got, c.want)
				}
			}
		})
	}
}

func TestReadReplayGainFromFiles(t *testing.T) {
	flac := buildFLAC(flacSpec{sampleRate: 44100, channels: 2, bps: 16, seconds: 10, audioBytes: 100,
		comments: []string{"TITLE=x", "REPLAYGAIN_TRACK_GAIN=-8.25 dB", "REPLAYGAIN_TRACK_PEAK=0.91", "REPLAYGAIN_ALBUM_GAIN=-9 dB"}})
	g, err := music.ReadReplayGain(bytes.NewReader(flac))
	if err != nil || !near(g.TrackGain, -8.25) || !near(g.TrackPeak, 0.91) || !near(g.AlbumGain, -9) || g.AlbumPeak != nil {
		t.Fatalf("flac: %+v %v", g, err)
	}

	mp3 := append(id3v2([][2]string{{"TIT2", "Song"}, {"TXXX", "replaygain_track_gain\x00-3.1 dB"}, {"TXXX", "REPLAYGAIN_TRACK_PEAK\x000.8"}}), mp3Frame44()...)
	g, err = music.ReadReplayGain(bytes.NewReader(mp3))
	if err != nil || !near(g.TrackGain, -3.1) || !near(g.TrackPeak, 0.8) {
		t.Fatalf("mp3: %+v %v", g, err)
	}

	// No tags at all is not an error; damaged data never panics.
	if g, err := music.ReadReplayGain(bytes.NewReader(bytes.Repeat([]byte("x"), 300))); err != nil || !g.Empty() {
		t.Fatalf("no tags: %+v %v", g, err)
	}
	broken := append([]byte("ID3\x03\x00\x00\x00\x00\x00\x20TXXX\xff\xff\xff\xff"), make([]byte, 40)...)
	if g, err := music.ReadReplayGain(bytes.NewReader(broken)); err == nil && !g.Empty() {
		t.Fatalf("damaged tag gave %+v", g)
	}
}
