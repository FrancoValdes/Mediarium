package metafiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rdborg/mediarium/internal/fileperms"
)

// Kind is what a planned file is.
type Kind string

const (
	KindNFO          Kind = "nfo"          // movie.nfo or tvshow.nfo
	KindEpisodeNFO   Kind = "episodenfo"   // an episode's NFO, named after its video
	KindPoster       Kind = "poster"       // poster.jpg
	KindFanart       Kind = "fanart"       // fanart.jpg
	KindLogo         Kind = "clearlogo"    // clearlogo.png
	KindSeasonPoster Kind = "seasonposter" // season01-poster.jpg
)

// Want says which families of file to write.
type Want struct {
	NFO     bool
	Artwork bool
}

// Item is one file that could be written.
type Item struct {
	Kind   Kind
	Season int    // for a season poster: 0 is the specials
	Dir    string // the folder it goes in
	// Name is the full file name for an NFO, and the name without extension for
	// a picture (the extension depends on what TMDB sends).
	Name string
	// Exists is true when the same thing is already there: the file itself, or
	// one that does the same job (folder.jpg next to a missing poster.jpg, a
	// movie.nfo next to a missing Film.nfo). Ours is true when the file this
	// item would write is there.
	Exists bool
	Ours   bool
	// Video is the video file an episode NFO or a stem-named movie file belongs to.
	Video string
}

// Needed reports whether the item should be written: nothing of the kind is
// there yet, or it is Mediarium's own file and the person said replace.
func (it Item) Needed(replace bool) bool {
	return !it.Exists || (replace && it.Ours)
}

// Path is where the file goes. For a picture the extension is added.
func (it Item) Path(ext string) string { return filepath.Join(it.Dir, it.Name+ext) }

var imageExts = []string{".jpg", ".jpeg", ".png", ".webp", ".tbn"}

var videoExts = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".mov": true, ".wmv": true, ".m4v": true, ".ts": true, ".m2ts": true}

// dirIndex lists a folder's file names, lower case, so a name is found
// whatever its case.
type dirIndex map[string]bool

func readDir(dir string) dirIndex {
	idx := dirIndex{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return idx
	}
	for _, e := range entries {
		idx[strings.ToLower(e.Name())] = true
	}
	return idx
}

func (d dirIndex) has(name string) bool { return d[strings.ToLower(name)] }

// anyImage reports whether a picture called one of the bases is there, in any
// of the picture formats.
func (d dirIndex) anyImage(bases ...string) bool {
	for _, b := range bases {
		for _, ext := range imageExts {
			if d.has(b + ext) {
				return true
			}
		}
	}
	return false
}

// Layout is where one movie's files go.
type Layout struct {
	Dir  string
	Stem string // the video's name without extension
	// Alone is true when the movie has a folder of its own (it is not the
	// library folder and holds no other video): then movie.nfo and poster.jpg
	// are used. Otherwise every file is named after the video.
	Alone bool
}

// MovieLayout works out where a movie's NFO and pictures go. It reports false
// for a video that is not inside root.
func MovieLayout(root, video string) (Layout, bool) {
	if root == "" || video == "" {
		return Layout{}, false
	}
	dir := filepath.Dir(video)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Layout{}, false
	}
	stem := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	l := Layout{Dir: dir, Stem: stem}
	if rel != "." && videosIn(dir) <= 1 {
		l.Alone = true
	}
	return l, true
}

// videosIn counts the video files directly in dir, samples left out.
func videosIn(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		if strings.Contains(strings.ToLower(e.Name()), "sample") {
			continue
		}
		n++
	}
	return n
}

// PlanMovie lists the files to write for a movie.
func PlanMovie(l Layout, video string, want Want) []Item {
	idx := readDir(l.Dir)
	var items []Item
	if want.NFO {
		name := l.Stem + ".nfo"
		if l.Alone {
			name = "movie.nfo"
		}
		it := Item{Kind: KindNFO, Dir: l.Dir, Name: name, Video: video, Ours: idx.has(name)}
		// Either name is the person's own NFO.
		it.Exists = idx.has("movie.nfo") || idx.has(l.Stem+".nfo")
		items = append(items, it)
	}
	if want.Artwork {
		prefix := func(base string) string {
			if l.Alone {
				return base
			}
			return l.Stem + "-" + base
		}
		add := func(kind Kind, base string, equivalents ...string) {
			name := prefix(base)
			it := Item{Kind: kind, Dir: l.Dir, Name: name, Video: video, Ours: idx.anyImage(name)}
			names := []string{name}
			for _, e := range equivalents {
				names = append(names, prefix(e))
			}
			it.Exists = idx.anyImage(names...)
			items = append(items, it)
		}
		add(KindPoster, "poster", "folder", "cover")
		add(KindFanart, "fanart", "backdrop", "background", "art")
		add(KindLogo, "clearlogo", "logo")
	}
	return items
}

// seasonDir matches the names season folders have: "Season 01", "Series 2",
// "Staffel 3", "S01", "Specials".
var seasonDir = regexp.MustCompile(`(?i)^(?:(?:season|series|staffel|saison|temporada|stagione)[-_. ]*\d+|specials?|s\d{1,3})$`)

// ShowDir finds the folder of a show from its episode files: the folder above
// the season folders. It reports false when the files are not in a folder of
// the show's own inside root (a flat dump of episodes straight in the library
// folder has no show folder).
func ShowDir(root string, files []string) (string, bool) {
	if root == "" {
		return "", false
	}
	votes := map[string]int{}
	for _, f := range files {
		d := filepath.Dir(f)
		if seasonDir.MatchString(filepath.Base(d)) {
			d = filepath.Dir(d)
		}
		rel, err := filepath.Rel(root, d)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		votes[d]++
	}
	best, bestN := "", 0
	dirs := make([]string, 0, len(votes))
	for d := range votes {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if votes[d] > bestN {
			best, bestN = d, votes[d]
		}
	}
	return best, best != ""
}

// SeasonPosterName is the name (without extension) Kodi gives a season's
// poster: season01-poster, season-specials-poster for season 0.
func SeasonPosterName(season int) string {
	if season == 0 {
		return "season-specials-poster"
	}
	return fmt.Sprintf("season%02d-poster", season)
}

// PlanShow lists the files to write in a show's folder: tvshow.nfo, poster,
// fanart, clearlogo and one poster for each of the seasons given.
func PlanShow(dir string, seasons []int, want Want) []Item {
	idx := readDir(dir)
	var items []Item
	if want.NFO {
		items = append(items, Item{Kind: KindNFO, Dir: dir, Name: "tvshow.nfo", Ours: idx.has("tvshow.nfo"), Exists: idx.has("tvshow.nfo")})
	}
	if want.Artwork {
		add := func(kind Kind, base string, equivalents ...string) {
			it := Item{Kind: kind, Dir: dir, Name: base, Ours: idx.anyImage(base)}
			it.Exists = idx.anyImage(append([]string{base}, equivalents...)...)
			items = append(items, it)
		}
		add(KindPoster, "poster", "folder", "cover")
		add(KindFanart, "fanart", "backdrop", "background", "art")
		add(KindLogo, "clearlogo", "logo")
		seen := map[int]bool{}
		sorted := append([]int(nil), seasons...)
		sort.Ints(sorted)
		for _, s := range sorted {
			if seen[s] || s < 0 {
				continue
			}
			seen[s] = true
			name := SeasonPosterName(s)
			base := strings.TrimSuffix(name, "-poster")
			it := Item{Kind: KindSeasonPoster, Season: s, Dir: dir, Name: name, Ours: idx.anyImage(name)}
			// season01.jpg is the same poster under its older name.
			it.Exists = idx.anyImage(name, base)
			items = append(items, it)
		}
	}
	return items
}

// PlanEpisode lists an episode NFO, named after its video.
func PlanEpisode(video string) Item {
	dir := filepath.Dir(video)
	name := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video)) + ".nfo"
	has := readDir(dir).has(name)
	return Item{Kind: KindEpisodeNFO, Dir: dir, Name: name, Video: video, Ours: has, Exists: has}
}

// Counts says how many files of each kind are still to be written.
type Counts map[Kind]int

// Count adds up what Needed says.
func Count(items []Item, replace bool) Counts {
	c := Counts{}
	for _, it := range items {
		if it.Needed(replace) {
			c[it.Kind]++
		}
	}
	return c
}

// Source gives the content of the files. Each is asked for only when a file is
// really going to be written, so nothing is fetched for what is already there.
type Source interface {
	// NFO returns the text of an NFO item.
	NFO(ctx context.Context, it Item) ([]byte, error)
	// Image returns a picture, or nil when there is none for this item.
	Image(ctx context.Context, it Item) ([]byte, error)
}

// Result is what Execute did.
type Result struct {
	Written Counts
	Failed  []string
}

// Execute writes the items that are needed. A file that cannot be made is
// reported in Failed and the rest carry on.
func Execute(ctx context.Context, items []Item, src Source, replace bool) Result {
	res := Result{Written: Counts{}}
	for _, it := range items {
		if ctx.Err() != nil {
			return res
		}
		if !it.Needed(replace) {
			continue
		}
		wrote, err := writeItem(ctx, it, src, replace)
		if err != nil {
			if ctx.Err() != nil {
				return res // stopped: not a failure of this file
			}
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", it.Name, err))
			continue
		}
		if wrote {
			res.Written[it.Kind]++
		}
	}
	return res
}

func writeItem(ctx context.Context, it Item, src Source, replace bool) (bool, error) {
	switch it.Kind {
	case KindNFO, KindEpisodeNFO:
		data, err := src.NFO(ctx, it)
		if err != nil {
			return false, err
		}
		if len(data) == 0 {
			return false, nil
		}
		return WriteFile(it.Path(""), data, replace)
	default:
		data, err := src.Image(ctx, it)
		if err != nil {
			return false, err
		}
		if len(data) == 0 {
			return false, nil
		}
		ext, ok := SniffImage(data)
		if !ok {
			return false, errors.New("what TMDB sent isn't a JPEG or PNG picture")
		}
		path := it.Path(ext)
		// A different extension of the same name is the person's own picture:
		// ours is only the exact file.
		return WriteFile(path, data, replace)
	}
}

// SniffImage says what a picture is from its first bytes: ".jpg" or ".png".
func SniffImage(data []byte) (string, bool) {
	switch {
	case len(data) > 3 && bytes.Equal(data[:3], []byte{0xff, 0xd8, 0xff}):
		return ".jpg", true
	case len(data) > 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return ".png", true
	}
	return "", false
}

// stagingSuffix marks the file being written before it takes its name, so a
// media server never reads half a file.
const stagingSuffix = ".mediarium-tmp"

// WriteFile writes data to path as a whole or not at all. A file that is there
// stays untouched unless replace is true, and then it is swapped in one step.
// It reports whether the file was written. The file gets the permissions set in
// Settings (see internal/fileperms), else the usual ones.
func WriteFile(path string, data []byte, replace bool) (bool, error) {
	if !replace {
		if _, err := os.Lstat(path); err == nil {
			return false, nil
		}
	}
	staging := path + stagingSuffix
	_ = os.Remove(staging) // left by an earlier attempt that died; ours by name
	out, err := os.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return false, fmt.Errorf("create: %w", err)
	}
	if _, err := out.Write(data); err != nil {
		out.Close()
		_ = os.Remove(staging)
		return false, fmt.Errorf("write: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(staging)
		return false, fmt.Errorf("close: %w", err)
	}
	_ = fileperms.ApplyFile(staging)
	if replace {
		if err := os.Rename(staging, path); err != nil {
			_ = os.Remove(staging)
			return false, fmt.Errorf("replace: %w", err)
		}
		return true, nil
	}
	// A hard link fails when the name is taken, which is what a file that
	// appeared in the meantime must mean: leave it alone.
	err = os.Link(staging, path)
	if err == nil {
		_ = os.Remove(staging)
		return true, nil
	}
	if errors.Is(err, fs.ErrExist) {
		_ = os.Remove(staging)
		return false, nil
	}
	// No hard links here (some network shares): rename, after a last look.
	if _, statErr := os.Lstat(path); statErr == nil {
		_ = os.Remove(staging)
		return false, nil
	}
	if err := os.Rename(staging, path); err != nil {
		_ = os.Remove(staging)
		return false, fmt.Errorf("move into place: %w", err)
	}
	return true, nil
}
