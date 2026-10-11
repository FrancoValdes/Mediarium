package music

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/dhowden/tag"
)

// ReplayGain is the loudness information written in a file's tags. A nil field
// is one the file doesn't have. Gains are in dB, peaks are linear (1.0 is full
// scale).
type ReplayGain struct {
	TrackGain *float64 `json:"trackGain"`
	TrackPeak *float64 `json:"trackPeak"`
	AlbumGain *float64 `json:"albumGain"`
	AlbumPeak *float64 `json:"albumPeak"`
}

// Empty reports whether the file carries no ReplayGain information at all.
func (g ReplayGain) Empty() bool {
	return g.TrackGain == nil && g.TrackPeak == nil && g.AlbumGain == nil && g.AlbumPeak == nil
}

// parseGainValue reads "-6.54 dB", "+3.2dB" or "-6.54". ok is false for
// anything else, including values that are not finite.
func parseGainValue(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSpace(strings.TrimSuffix(s, "db"))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimPrefix(s, "+"), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// ReplayGainFromRaw picks the ReplayGain values out of the raw tag map that
// the tag library returns. The names are not the same in every format:
// Vorbis comments (FLAC, Ogg, Opus) and MP4 freeform atoms use the field name
// as the key, ID3v2 keeps them in TXXX frames whose description is the name.
// Names are matched without regard to case. When a file has no ReplayGain
// fields but Opus-style R128 ones, those are converted: R128 gains are Q7.8
// fixed point relative to -23 LUFS, and ReplayGain 2.0 uses -18 LUFS, so the
// difference is 5 dB.
func ReplayGainFromRaw(raw map[string]interface{}) ReplayGain {
	var g ReplayGain
	var r128Track, r128Album *float64
	set := func(name, value string) {
		v, ok := parseGainValue(value)
		if !ok {
			return
		}
		switch name {
		case "replaygain_track_gain":
			g.TrackGain = &v
		case "replaygain_album_gain":
			g.AlbumGain = &v
		case "replaygain_track_peak":
			if v >= 0 {
				g.TrackPeak = &v
			}
		case "replaygain_album_peak":
			if v >= 0 {
				g.AlbumPeak = &v
			}
		case "r128_track_gain":
			x := v/256 + 5
			r128Track = &x
		case "r128_album_gain":
			x := v/256 + 5
			r128Album = &x
		}
	}
	for key, val := range raw {
		switch x := val.(type) {
		case string:
			set(strings.ToLower(key), x)
		case *tag.Comm:
			if x != nil && strings.HasPrefix(key, "TXX") {
				set(strings.ToLower(strings.TrimSpace(x.Description)), x.Text)
			}
		case tag.Comm:
			if strings.HasPrefix(key, "TXX") {
				set(strings.ToLower(strings.TrimSpace(x.Description)), x.Text)
			}
		}
	}
	if g.TrackGain == nil {
		g.TrackGain = r128Track
	}
	if g.AlbumGain == nil {
		g.AlbumGain = r128Album
	}
	return g
}

// ReadReplayGain reads the ReplayGain tags of an audio file. A file without
// tags gives an empty result and no error. Nothing is ever written.
func ReadReplayGain(r io.ReadSeeker) (g ReplayGain, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			g, err = ReplayGain{}, errors.New("read tags: unreadable tag data")
		}
	}()
	m, err := tag.ReadFrom(r)
	if errors.Is(err, tag.ErrNoTagsFound) {
		return ReplayGain{}, nil
	}
	if err != nil {
		return ReplayGain{}, fmt.Errorf("read tags: %w", err)
	}
	return ReplayGainFromRaw(m.Raw()), nil
}
