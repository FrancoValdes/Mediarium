package subtitles

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

// ToUTF8 returns subtitle text as UTF-8. A lot of subtitle files, Romanian,
// Polish, Greek and Hebrew ones among them, are saved in an older Windows code
// page, and players show them as garbage unless they are converted. Text that
// already is UTF-8 (with or without a byte order mark) is left alone; anything
// else is read as the code page that language is usually saved in.
func ToUTF8(data []byte, language string) []byte {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		if out, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder().Bytes(data); err == nil {
			return out
		}
		return data
	case utf8.Valid(data):
		return data
	}
	if out, err := codePageFor(language).NewDecoder().Bytes(data); err == nil {
		return out
	}
	return data
}

// codePageFor is the Windows code page subtitles in a language are usually
// saved in.
func codePageFor(language string) encoding.Encoding {
	var e encoding.Encoding
	switch baseLanguage(strings.ToLower(language)) {
	case "ro", "pl", "cs", "sk", "hu", "hr", "sl", "sr", "bs", "sq":
		e = charmap.Windows1250
	case "ru", "uk", "bg", "be", "mk", "kk":
		e = charmap.Windows1251
	case "el":
		e = charmap.Windows1253
	case "tr", "az":
		e = charmap.Windows1254
	case "he":
		e = charmap.Windows1255
	case "ar", "fa", "ur":
		e = charmap.Windows1256
	case "et", "lv", "lt":
		e = charmap.Windows1257
	case "vi":
		e = charmap.Windows1258
	default:
		e = charmap.Windows1252
	}
	return e
}

var (
	vttTiming = regexp.MustCompile(`^(?:(\d+):)?(\d{2}):(\d{2})\.(\d{3})\s*-->\s*(?:(\d+):)?(\d{2}):(\d{2})\.(\d{3})`)
	vttTag    = regexp.MustCompile(`</?(?:c|v|lang|ruby|rt)[^>]*>`)
)

// VTTToSRT converts WebVTT text to SRT: the header and notes go, cues are
// numbered, and the time stamps use a comma before the milliseconds.
func VTTToSRT(data []byte) []byte {
	text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	blocks := strings.Split(text, "\n\n")
	var out strings.Builder
	n := 0
	for _, b := range blocks {
		lines := strings.Split(strings.TrimSpace(b), "\n")
		at := -1
		for i, l := range lines {
			if vttTiming.MatchString(strings.TrimSpace(l)) {
				at = i
				break
			}
		}
		if at < 0 {
			continue // the WEBVTT header, a NOTE or a STYLE block
		}
		m := vttTiming.FindStringSubmatch(strings.TrimSpace(lines[at]))
		stamp := func(h, mi, s, ms string) string {
			if h == "" {
				h = "0"
			}
			return strings.Repeat("0", max(0, 2-len(h))) + h + ":" + mi + ":" + s + "," + ms
		}
		n++
		out.WriteString(strings.Join([]string{
			strconv.Itoa(n),
			stamp(m[1], m[2], m[3], m[4]) + " --> " + stamp(m[5], m[6], m[7], m[8]),
			vttTag.ReplaceAllString(strings.Join(lines[at+1:], "\n"), ""),
		}, "\n"))
		out.WriteString("\n\n")
	}
	return []byte(out.String())
}
