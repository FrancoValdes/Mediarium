package subtitles

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Subtitles stored inside the video file: the subtitle tracks of an MKV, or
// of an MP4/M4V/MOV. Only the file's headers are read, never the video, so
// this is quick even on a network share.

// EmbeddedTrack is one subtitle track inside a video file.
type EmbeddedTrack struct {
	Lang   string // canonical code ("en", "pt-BR"), or the file's own code when Mediarium doesn't know it; "" when the track has none
	Codec  string // "S_TEXT/UTF8", "S_HDMV/PGS", "tx3g"...
	Name   string
	Forced bool // only the foreign-language parts: doesn't count as a full subtitle
	SDH    bool // for the deaf and hard of hearing (Matroska's flag, or said so in the name)
}

// ErrNotVideo means the file isn't an MKV or MP4 this can read.
var ErrNotVideo = errors.New("not an MKV or MP4 file")

const (
	maxTracksBytes = 4 << 20 // an MKV's track list
	maxBoxBytes    = 1 << 20 // one small MP4 box (mdhd, hdlr, elng)
	maxScanBoxes   = 100_000 // MP4 boxes visited, against a damaged file
)

// EmbeddedSubtitles lists the subtitle tracks inside the video at path.
func EmbeddedSubtitles(path string) ([]EmbeddedTrack, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 12)
	if _, err := io.ReadFull(f, head); err != nil {
		return nil, ErrNotVideo
	}
	switch {
	case binary.BigEndian.Uint32(head) == idEBML:
		return mkvSubtitles(f)
	case string(head[4:8]) == "ftyp":
		return mp4Subtitles(f)
	}
	return nil, ErrNotVideo
}

type embeddedEntry struct {
	size  int64
	mod   int64
	langs map[string]bool
	have  HaveMap
}

var (
	embeddedMu    sync.Mutex
	embeddedCache = map[string]embeddedEntry{}
)

const maxEmbeddedCache = 50_000

// EmbeddedLanguages are the languages of the full (not forced) subtitle
// tracks inside the video at path, lower case, as languagesOnDisk keys them.
// Results are kept while the file's size and time stay the same. A file that
// can't be read has none.
func EmbeddedLanguages(path string) map[string]bool {
	langs, _ := embeddedInfo(path)
	return langs
}

// EmbeddedHave is which kinds of subtitle the video's tracks make, per language.
func EmbeddedHave(path string) HaveMap {
	_, have := embeddedInfo(path)
	return have
}

func embeddedInfo(path string) (map[string]bool, HaveMap) {
	info, err := os.Stat(path)
	if err != nil || !isVideoForEmbedded(path) {
		return map[string]bool{}, HaveMap{}
	}
	embeddedMu.Lock()
	e, ok := embeddedCache[path]
	embeddedMu.Unlock()
	if ok && e.size == info.Size() && e.mod == info.ModTime().UnixNano() {
		return e.langs, e.have
	}
	langs := map[string]bool{}
	have := HaveMap{}
	tracks, _ := EmbeddedSubtitles(path)
	for _, t := range tracks {
		if t.Lang == "" {
			continue
		}
		if !t.Forced {
			langs[strings.ToLower(t.Lang)] = true
		}
		switch {
		case t.Forced:
			have.Add(t.Lang, VariantForced)
		case t.SDH:
			have.Add(t.Lang, VariantSDH)
		default:
			have.Add(t.Lang, VariantFull)
		}
	}
	embeddedMu.Lock()
	if len(embeddedCache) >= maxEmbeddedCache {
		clear(embeddedCache)
	}
	embeddedCache[path] = embeddedEntry{size: info.Size(), mod: info.ModTime().UnixNano(), langs: langs, have: have}
	embeddedMu.Unlock()
	return langs, have
}

func isVideoForEmbedded(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mk3d", ".webm", ".mp4", ".m4v", ".mov":
		return true
	}
	return false
}

// trackLanguage turns a track's language tag into Mediarium's code. A
// BCP 47 tag ("pt-BR") wins over the older three-letter code ("por").
func trackLanguage(bcp47, iso string) string {
	for _, tag := range []string{bcp47, iso} {
		tag = strings.TrimSpace(tag)
		if tag == "" || strings.EqualFold(tag, "und") || strings.EqualFold(tag, "zxx") || strings.EqualFold(tag, "mul") {
			continue
		}
		if code, ok := languageByName[fold(tag)]; ok {
			return code
		}
		if base, _, cut := strings.Cut(tag, "-"); cut {
			if code, ok := languageByName[fold(base)]; ok {
				return code
			}
		}
		return strings.ToLower(tag)
	}
	return ""
}

// --- Matroska (MKV, WebM) ---

const (
	idEBML          = 0x1A45DFA3
	idSegment       = 0x18538067
	idSeekHead      = 0x114D9B74
	idSeek          = 0x4DBB
	idSeekID        = 0x53AB
	idSeekPosition  = 0x53AC
	idTracks        = 0x1654AE6B
	idCluster       = 0x1F43B675
	idTrackEntry    = 0xAE
	idTrackType     = 0x83
	idCodecID       = 0x86
	idLanguage      = 0x22B59C
	idLanguageBCP47 = 0x22B59D
	idName          = 0x536E
	idFlagForced    = 0x55AA
	idFlagHearing   = 0x55AB
	trackSubtitle   = 17
	unknownSize     = -1
)

// readVint reads an EBML variable-length number. keepMarker keeps the length
// bit (element IDs are written that way); sizes drop it, and a size of all
// ones means "unknown".
func readVint(r io.Reader, keepMarker bool) (int64, int, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:1]); err != nil {
		return 0, 0, err
	}
	n := 1
	for mask := byte(0x80); n <= 8 && b[0]&mask == 0; mask >>= 1 {
		n++
	}
	if n > 8 {
		return 0, 0, errors.New("bad EBML number")
	}
	if _, err := io.ReadFull(r, b[1:n]); err != nil {
		return 0, 0, err
	}
	v := int64(b[0])
	if !keepMarker {
		v &= int64(0xFF >> n)
	}
	allOnes := v == int64(0xFF>>n)
	for i := 1; i < n; i++ {
		v = v<<8 | int64(b[i])
		allOnes = allOnes && b[i] == 0xFF
	}
	if !keepMarker && allOnes {
		return unknownSize, n, nil
	}
	return v, n, nil
}

// elementHeader reads an element's ID and size and says how many bytes the
// header took.
func elementHeader(r io.Reader) (id, size int64, n int, err error) {
	id, a, err := readVint(r, true)
	if err != nil {
		return 0, 0, 0, err
	}
	size, b, err := readVint(r, false)
	if err != nil {
		return 0, 0, 0, err
	}
	return id, size, a + b, nil
}

func mkvSubtitles(f *os.File) ([]EmbeddedTrack, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	// The EBML header, then the Segment.
	_, size, n, err := elementHeader(f)
	if err != nil || size < 0 {
		return nil, ErrNotVideo
	}
	pos := int64(n) + size
	if _, err := f.Seek(pos, io.SeekStart); err != nil {
		return nil, err
	}
	id, segSize, n, err := elementHeader(f)
	if err != nil || id != idSegment {
		return nil, ErrNotVideo
	}
	segStart := pos + int64(n)
	segEnd := int64(-1)
	if segSize != unknownSize {
		segEnd = segStart + segSize
	}
	var tracksAt int64 = -1
	pos = segStart
scan:
	for i := 0; i < 64; i++ { // the top-level elements before the first cluster
		if segEnd >= 0 && pos >= segEnd {
			break
		}
		if _, err := f.Seek(pos, io.SeekStart); err != nil {
			return nil, err
		}
		id, size, n, err := elementHeader(f)
		if err != nil || size == unknownSize {
			break
		}
		switch id {
		case idTracks:
			return readTracks(f, size)
		case idSeekHead:
			if at, ok := seekTracks(f, size, segStart); ok {
				tracksAt = at
			}
		case idCluster:
			break scan // the tracks come before the video, or the SeekHead says where
		}
		pos += int64(n) + size
	}
	if tracksAt < 0 {
		return nil, nil
	}
	if _, err := f.Seek(tracksAt, io.SeekStart); err != nil {
		return nil, err
	}
	id, size, _, err = elementHeader(f)
	if err != nil || id != idTracks || size == unknownSize {
		return nil, nil
	}
	return readTracks(f, size)
}

// seekTracks finds where the Tracks element is from a SeekHead.
func seekTracks(r io.Reader, size, segStart int64) (int64, bool) {
	if size > maxTracksBytes {
		return 0, false
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, false
	}
	found := int64(-1)
	walkElements(buf, func(id int64, data []byte) {
		if id != idSeek {
			return
		}
		var target, at int64 = 0, -1
		walkElements(data, func(id int64, v []byte) {
			switch id {
			case idSeekID:
				target = int64(beUint(v))
			case idSeekPosition:
				at = int64(beUint(v))
			}
		})
		if target == idTracks && at >= 0 {
			found = segStart + at
		}
	})
	return found, found >= 0
}

func readTracks(r io.Reader, size int64) ([]EmbeddedTrack, error) {
	if size > maxTracksBytes {
		return nil, fmt.Errorf("the track list is %d bytes", size)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	var out []EmbeddedTrack
	walkElements(buf, func(id int64, entry []byte) {
		if id != idTrackEntry {
			return
		}
		var (
			kind        uint64
			codec, name string
			lang, bcp47 = "eng", "" // Matroska's default language is English
			forced      bool
			hearing     bool
		)
		walkElements(entry, func(id int64, v []byte) {
			switch id {
			case idTrackType:
				kind = beUint(v)
			case idCodecID:
				codec = cString(v)
			case idLanguage:
				lang = cString(v)
			case idLanguageBCP47:
				bcp47 = cString(v)
			case idName:
				name = cString(v)
			case idFlagForced:
				forced = beUint(v) == 1
			case idFlagHearing:
				hearing = beUint(v) == 1
			}
		})
		if kind != trackSubtitle {
			return
		}
		if !forced {
			// Muxers that don't set the flag often say so in the name.
			n := strings.ToLower(name)
			forced = strings.Contains(n, "forced") || strings.Contains(n, "foreign parts")
		}
		if !hearing {
			hearing, _ = ClassifyName(name)
		}
		out = append(out, EmbeddedTrack{Lang: trackLanguage(bcp47, lang), Codec: codec, Name: name, Forced: forced, SDH: hearing && !forced})
	})
	return out, nil
}

// walkElements calls fn for each element directly inside buf.
func walkElements(buf []byte, fn func(id int64, data []byte)) {
	r := &sliceReader{b: buf}
	for r.off < len(buf) {
		id, size, _, err := elementHeader(r)
		if err != nil || size < 0 || size > int64(len(buf)-r.off) {
			return
		}
		fn(id, buf[r.off:r.off+int(size)])
		r.off += int(size)
	}
}

type sliceReader struct {
	b   []byte
	off int
}

func (s *sliceReader) Read(p []byte) (int, error) {
	if s.off >= len(s.b) {
		return 0, io.EOF
	}
	n := copy(p, s.b[s.off:])
	s.off += n
	return n, nil
}

func beUint(b []byte) uint64 {
	var v uint64
	for _, c := range b[:min(len(b), 8)] {
		v = v<<8 | uint64(c)
	}
	return v
}

func cString(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}

// --- MP4, M4V, MOV ---

type mp4Box struct {
	typ        string
	start, end int64 // the box's contents
}

// mp4Boxes lists the boxes between start and end.
func mp4Boxes(f *os.File, start, end int64, visited *int) []mp4Box {
	var out []mp4Box
	var hdr [16]byte
	for pos := start; pos+8 <= end; {
		*visited++
		if *visited > maxScanBoxes {
			break
		}
		if _, err := f.ReadAt(hdr[:8], pos); err != nil {
			break
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		head := int64(8)
		switch size {
		case 1:
			if _, err := f.ReadAt(hdr[8:16], pos+8); err != nil {
				return out
			}
			size = int64(binary.BigEndian.Uint64(hdr[8:16]))
			head = 16
		case 0:
			size = end - pos // to the end of the file
		}
		if size < head || pos+size > end {
			break
		}
		out = append(out, mp4Box{typ: typ, start: pos + head, end: pos + size})
		pos += size
	}
	return out
}

func readBox(f *os.File, b mp4Box) []byte {
	if b.end-b.start > maxBoxBytes {
		return nil
	}
	buf := make([]byte, b.end-b.start)
	if _, err := f.ReadAt(buf, b.start); err != nil {
		return nil
	}
	return buf
}

func mp4Subtitles(f *os.File) ([]EmbeddedTrack, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	visited := 0
	var out []EmbeddedTrack
	for _, top := range mp4Boxes(f, 0, info.Size(), &visited) {
		if top.typ != "moov" {
			continue
		}
		for _, trak := range mp4Boxes(f, top.start, top.end, &visited) {
			if trak.typ != "trak" {
				continue
			}
			for _, mdia := range mp4Boxes(f, trak.start, trak.end, &visited) {
				if mdia.typ != "mdia" {
					continue
				}
				if t, ok := mp4Track(f, mdia, &visited); ok {
					out = append(out, t)
				}
			}
		}
	}
	return out, nil
}

// mp4Track reads one track's media box: a subtitle track has the handler
// "sbtl" (Apple, tx3g) or "subt" (ISO). QuickTime "text" tracks are left
// out: they are mostly chapter titles.
func mp4Track(f *os.File, mdia mp4Box, visited *int) (EmbeddedTrack, bool) {
	var handler, iso, bcp47, codec string
	for _, b := range mp4Boxes(f, mdia.start, mdia.end, visited) {
		switch b.typ {
		case "hdlr":
			if d := readBox(f, b); len(d) >= 12 {
				handler = string(d[8:12])
			}
		case "mdhd":
			iso = mdhdLanguage(readBox(f, b))
		case "elng":
			if d := readBox(f, b); len(d) > 4 {
				bcp47 = cString(d[4:])
			}
		case "minf":
			codec = mp4Codec(f, b, visited)
		}
	}
	if handler != "sbtl" && handler != "subt" {
		return EmbeddedTrack{}, false
	}
	return EmbeddedTrack{Lang: trackLanguage(bcp47, iso), Codec: codec}, true
}

// mdhdLanguage is the packed ISO 639-2 code in a media header.
func mdhdLanguage(d []byte) string {
	if len(d) < 4 {
		return ""
	}
	off := 4 + 4 + 4 + 4 + 4 // version 0: times, scale and length are 32-bit
	if d[0] == 1 {
		off = 4 + 8 + 8 + 4 + 8
	}
	if len(d) < off+2 {
		return ""
	}
	v := binary.BigEndian.Uint16(d[off:])
	if v == 0 || v == 0x7FFF {
		return ""
	}
	return string([]byte{byte(v>>10&0x1F) + 0x60, byte(v>>5&0x1F) + 0x60, byte(v&0x1F) + 0x60})
}

// mp4Codec is the sample entry type in minf/stbl/stsd ("tx3g", "wvtt"...).
func mp4Codec(f *os.File, minf mp4Box, visited *int) string {
	for _, stbl := range mp4Boxes(f, minf.start, minf.end, visited) {
		if stbl.typ != "stbl" {
			continue
		}
		for _, b := range mp4Boxes(f, stbl.start, stbl.end, visited) {
			if b.typ != "stsd" || b.end-b.start < 16 {
				continue
			}
			var t [4]byte
			if _, err := f.ReadAt(t[:], b.start+12); err == nil {
				return string(t[:])
			}
		}
	}
	return ""
}
