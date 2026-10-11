package torrentclient

import (
	"path"
	"strings"
)

// junkExtensions are files nobody wants out of a release: notes, links and
// programs. They are tiny, but a few of them are also the odd thing to find in
// a movie folder.
var junkExtensions = map[string]bool{
	".nfo": true,
	".txt": true,
	".exe": true,
	".url": true,
	".lnk": true,
}

var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true, ".mov": true, ".wmv": true,
	".ts": true, ".m2ts": true, ".mpg": true, ".mpeg": true, ".webm": true, ".flv": true,
}

// sampleMaxBytes is the largest file that can still be a sample clip. A real
// film that happens to have "sample" in its name is much bigger.
const sampleMaxBytes = 250 << 20

// JunkFile reports whether a file of a torrent is obvious junk: a sample clip
// (a short video named sample, or anything small in a folder called Sample), or
// an .nfo, .txt, .exe, .url or .lnk file. p is the path inside the torrent
// with forward slashes. keepText leaves .txt files alone, for torrents where
// they can be the real thing (books).
func JunkFile(p string, size int64, keepText bool) bool {
	p = strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
	ext := path.Ext(p)
	if junkExtensions[ext] && !(keepText && ext == ".txt") {
		return true
	}
	if size > sampleMaxBytes {
		return false
	}
	dir, file := path.Split(p)
	for _, part := range strings.Split(strings.Trim(dir, "/"), "/") {
		if part == "sample" || part == "samples" {
			return true
		}
	}
	if !videoExtensions[ext] {
		return false
	}
	return nameIsSample(strings.TrimSuffix(file, ext))
}

// nameIsSample reports whether "sample" stands alone in a file name, as in
// "sample.mkv", "movie-sample" or "Show.S01E01.sample", but not in "Sampler".
func nameIsSample(name string) bool {
	for i := 0; ; {
		at := strings.Index(name[i:], "sample")
		if at < 0 {
			return false
		}
		start, end := i+at, i+at+len("sample")
		before := start == 0 || !isWordChar(name[start-1])
		after := end == len(name) || !isWordChar(name[end])
		if before && after {
			return true
		}
		i = end
	}
}

func isWordChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// JunkPaths lists the files of a torrent that are junk. When that would be
// every file there is nothing to leave out, and the result is empty.
func JunkPaths(files []FileState, keepText bool) []string {
	var junk []string
	for _, f := range files {
		if JunkFile(f.Path, f.Size, keepText) {
			junk = append(junk, f.Path)
		}
	}
	if len(junk) >= len(files) {
		return nil
	}
	return junk
}
