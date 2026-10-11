package subtitles

import (
	"os"
	"path/filepath"
	"strings"
)

// SidecarPath is where a downloaded subtitle is saved next to the video:
// "<video name>.<lang>.srt", "<video name>.<lang>.sdh.srt" or
// "<video name>.<lang>.forced.srt". Plex, Jellyfin and Emby read the language
// and these two markers from the name.
func SidecarPath(videoPath, lang string, v Variant) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	return base + "." + lang + v.Suffix() + ".srt"
}

// VariantOfPath reads the variant from a subtitle saved by SidecarPath: the
// part of the name between the language and the extension. ok is false when
// path isn't a subtitle of that video and language.
func VariantOfPath(videoPath, lang, path string) (v Variant, ok bool) {
	for _, cand := range []Variant{VariantFull, VariantSDH, VariantForced} {
		if path == SidecarPath(videoPath, lang, cand) {
			return cand, true
		}
	}
	return VariantFull, false
}

// sidecarFlags are the words after the language in a subtitle file's name.
var (
	fileForcedFlags = map[string]bool{"forced": true, "foreign": true}
	fileSDHFlags    = map[string]bool{"sdh": true, "hi": true, "cc": true, "hearing": true}
)

// DiskHave lists the subtitles sitting next to a video (".srt", ".ass", ".ssa",
// ".sub", ".sup" files named "<video name>.<lang>[.flag].<ext>") by language
// and kind: a ".forced" or ".foreign" file is a forced track, ".sdh", ".hi"
// and ".cc" mark one for the hard of hearing, and a file with neither is a
// full subtitle.
func DiskHave(videoPath string) HaveMap {
	out := HaveMap{}
	if videoPath == "" {
		return out
	}
	dir := filepath.Dir(videoPath)
	prefix := strings.ToLower(filepath.Base(strings.TrimSuffix(videoPath, filepath.Ext(videoPath)))) + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if e.IsDir() || !strings.HasPrefix(name, prefix) {
			continue
		}
		ext := filepath.Ext(name)
		switch ext {
		case ".srt", ".ass", ".ssa", ".sub", ".sup":
		default:
			continue
		}
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ext), ".")
		lang := parts[0]
		if lang == "" || fileForcedFlags[lang] || fileSDHFlags[lang] {
			continue // no language in the name
		}
		v := VariantFull
		for _, flag := range parts[1:] {
			switch {
			case fileForcedFlags[flag]:
				v = VariantForced
			case fileSDHFlags[flag] && v != VariantForced:
				v = VariantSDH
			}
		}
		out.Add(lang, v)
	}
	return out
}
