package music

import (
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/dhowden/tag"
)

// maxLyricsBytes caps the lyrics read from a file or a .lrc next to it.
const maxLyricsBytes = 256 << 10

// A line of a synced lyric starts with [mm:ss], [mm:ss.xx] or [mm:ss.xxx].
var lrcStamp = regexp.MustCompile(`(?m)^\s*\[\d{1,3}:\d{2}(?:[.:]\d{1,3})?\]`)

// IsSyncedLyrics reports whether text is in LRC form: at least two lines that
// start with a time stamp.
func IsSyncedLyrics(text string) bool {
	return len(lrcStamp.FindAllStringIndex(text, 2)) >= 2
}

// Lyrics is what a file or an online service has for a track. Synced is LRC
// text, Plain is the words without times; either may be empty.
type Lyrics struct {
	Source       string `json:"source"` // "sidecar", "file" or "lrclib"; "" when there are none
	Synced       string `json:"synced"`
	Plain        string `json:"plain"`
	Instrumental bool   `json:"instrumental"`
}

// Has reports whether there is anything to show.
func (l Lyrics) Has() bool { return l.Synced != "" || l.Plain != "" || l.Instrumental }

// classifyLyrics puts text in the synced or plain field of l.
func classifyLyrics(l *Lyrics, text string) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return
	}
	if IsSyncedLyrics(text) {
		if l.Synced == "" {
			l.Synced = text
		}
	} else if l.Plain == "" {
		l.Plain = text
	}
}

// SidecarLyrics reads the text of a .lrc file that sits next to an audio file.
func SidecarLyrics(r io.Reader) (Lyrics, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxLyricsBytes))
	if err != nil {
		return Lyrics{}, fmt.Errorf("read lyrics file: %w", err)
	}
	var l Lyrics
	classifyLyrics(&l, strings.TrimPrefix(string(b), "\ufeff"))
	if l.Has() {
		l.Source = "sidecar"
	}
	return l, nil
}

// SidecarName is the name of the .lrc file for an audio file: the same name
// with the extension swapped.
func SidecarName(audioRel string) string {
	return strings.TrimSuffix(audioRel, path.Ext(audioRel)) + ".lrc"
}

// EmbeddedLyrics reads the lyrics stored in an audio file's tags (ID3 USLT,
// Vorbis LYRICS / UNSYNCEDLYRICS / SYNCEDLYRICS, MP4 lyrics). Text with time
// stamps counts as synced. It only reads.
func EmbeddedLyrics(f io.ReadSeeker) (l Lyrics, err error) {
	defer func() {
		if r := recover(); r != nil {
			l, err = Lyrics{}, errors.New("read tags: unreadable tag data")
		}
	}()
	m, err := tag.ReadFrom(f)
	if errors.Is(err, tag.ErrNoTagsFound) {
		return Lyrics{}, nil
	}
	if err != nil {
		return Lyrics{}, fmt.Errorf("read tags: %w", err)
	}
	texts := []string{m.Lyrics()}
	raw := m.Raw()
	for _, k := range []string{"syncedlyrics", "unsyncedlyrics", "lyrics"} {
		for key, v := range raw {
			if strings.EqualFold(key, k) {
				if s, ok := v.(string); ok {
					texts = append(texts, s)
				}
			}
		}
	}
	for key, v := range raw {
		if c, ok := v.(*tag.Comm); ok && c != nil && strings.HasPrefix(key, "TXX") && strings.Contains(strings.ToLower(c.Description), "lyrics") {
			texts = append(texts, c.Text)
		}
	}
	for _, s := range texts {
		if len(s) > maxLyricsBytes {
			s = s[:maxLyricsBytes]
		}
		classifyLyrics(&l, s)
	}
	if l.Has() {
		l.Source = "file"
	}
	return l, nil
}
