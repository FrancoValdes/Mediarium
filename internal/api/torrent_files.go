package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/rdborg/mediarium/internal/organizer"
	"github.com/rdborg/mediarium/internal/parser"
	"github.com/rdborg/mediarium/internal/queue"
	"github.com/rdborg/mediarium/internal/torrentclient"
)

// Which files of a torrent are downloaded. Samples and notes are left out by
// default, a pack can be cut down to the wanted season, and a person can tick
// files on and off while the torrent runs (see handleTorrentFiles).

// skippedMarker lists, inside a download's folder, the files of the torrent
// that were left out. A file that shares a piece with its neighbour is still
// created on disk, partly filled; the import must not mistake it for a video.
const skippedMarker = ".mediarium-skipped"

// writeSkipped records the files left out of a download. With none, an old
// marker is removed.
func writeSkipped(dir string, paths []string) {
	file := filepath.Join(dir, skippedMarker)
	if len(paths) == 0 {
		_ = os.Remove(file)
		return
	}
	b, err := json.Marshal(paths)
	if err != nil {
		return
	}
	if err := os.WriteFile(file, b, 0o644); err != nil {
		slog.Warn("torrent: note the files left out", "dir", dir, "err", err)
	}
}

// readSkipped returns the files recorded by writeSkipped.
func readSkipped(dir string) map[string]bool {
	b, err := os.ReadFile(filepath.Join(dir, skippedMarker))
	if err != nil {
		return nil
	}
	var paths []string
	if json.Unmarshal(b, &paths) != nil {
		return nil
	}
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[p] = true
	}
	return out
}

// dropSkipped removes the files a download left out from a list of paths found
// under dir.
func dropSkipped(dir string, files []string, skip map[string]bool) []string {
	if len(skip) == 0 {
		return files
	}
	out := files[:0:0]
	for _, f := range files {
		rel, err := filepath.Rel(dir, f)
		if err == nil && skip[filepath.ToSlash(rel)] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// largestVideoFile is organizer.FindLargestVideoFile for a finished download:
// files the torrent left out do not count.
func largestVideoFile(dir string) (string, error) {
	skip := readSkipped(dir)
	if len(skip) == 0 {
		return organizer.FindLargestVideoFile(dir, findOptions(dir)...)
	}
	files, err := organizer.FindVideoFiles(dir, findOptions(dir)...)
	if err != nil {
		return "", err
	}
	var best string
	var bestSize int64
	for _, f := range dropSkipped(dir, files, skip) {
		if info, err := os.Stat(f); err == nil && info.Size() > bestSize {
			best, bestSize = f, info.Size()
		}
	}
	if best == "" {
		return organizer.FindLargestVideoFile(dir, findOptions(dir)...)
	}
	return best, nil
}

// autoSkips decides which files of a torrent are not worth downloading for the
// queue item it was grabbed for: obvious junk, and, when the setting is on, the
// files of other seasons of a pack. Nothing is skipped when that would leave
// no file.
func (s *Server) autoSkips(item queue.Item, files []torrentclient.FileState) []string {
	skip := map[string]bool{}
	if s.torrentSkipJunk() {
		for _, p := range torrentclient.JunkPaths(files, item.BookID > 0) {
			skip[p] = true
		}
	}
	if s.torrentPackOnlyWanted() && item.SeriesID > 0 && item.Season > 0 {
		var episodes []int
		if item.Episode > 0 {
			episodes = []int{item.Episode}
		}
		for _, p := range packSkips(files, item.Season, episodes) {
			skip[p] = true
		}
	}
	if len(skip) >= len(files) {
		return nil
	}
	out := make([]string, 0, len(skip))
	for p := range skip {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// seasonFolder matches a folder that is a season on its own: "Season 2", "S02".
var seasonFolder = regexp.MustCompile(`(?i)^(?:season|series|s)[ ._-]*(\d{1,2})$`)

// packSkips lists the files of a pack that are not for the wanted season, and,
// for a grab of particular episodes, the files of other episodes of that season.
// A file whose name does not say which episode it is stays. When no video file
// is recognised as one of the wanted season, nothing is skipped: the names are
// not understood.
func packSkips(files []torrentclient.FileState, season int, episodes []int) []string {
	want := map[int]bool{}
	for _, e := range episodes {
		want[e] = true
	}
	var skip []string
	matched := 0
	for _, f := range files {
		known, fits := packFileFits(f.Path, season, want)
		switch {
		case known && !fits:
			skip = append(skip, f.Path)
		case known && organizer.IsVideoFile(f.Path):
			matched++
		}
	}
	if matched == 0 {
		return nil
	}
	return skip
}

// packFileFits says whether a file's name places it in the wanted season (and
// episodes, when only some are wanted). known is false when the name does not
// say.
func packFileFits(p string, season int, want map[int]bool) (known, fits bool) {
	base := path.Base(p)
	rel := parser.Parse(base)
	fileSeason := rel.Season
	known = fileSeason > 0 || rel.Special
	if !known {
		// Often the folder says it: Show/Season 2/Show - 05.mkv.
		if dir := path.Base(path.Dir(p)); dir != "." && dir != "/" {
			if m := seasonFolder.FindStringSubmatch(dir); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil {
					fileSeason, known = n, n > 0
				}
			}
		}
	}
	if !known {
		return false, true
	}
	if rel.Special || fileSeason != season {
		return true, false
	}
	if len(want) == 0 || len(rel.Episodes) == 0 {
		return true, true
	}
	for _, e := range rel.Episodes {
		if want[e] {
			return true, true
		}
	}
	return true, false
}
