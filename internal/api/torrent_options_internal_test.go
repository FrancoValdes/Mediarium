package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/config"
	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/queue"
	"github.com/rdborg/mediarium/internal/settings"
	"github.com/rdborg/mediarium/internal/speed"
	"github.com/rdborg/mediarium/internal/store"
	"github.com/rdborg/mediarium/internal/torrentclient"
	"github.com/rdborg/mediarium/internal/torrentopts"
)

// optionsServer is a bare server with what the torrent options need.
func optionsServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	work := filepath.Join(t.TempDir(), "downloads", "incomplete")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Server{
		cfg:         config.Config{DownloadsDir: filepath.Dir(work), DownloadsIncomplete: work},
		Settings:    settings.New(db, nil),
		QueueRepo:   queue.NewRepo(db),
		MovieRepo:   library.NewRepo(db),
		IndexerRepo: indexers.NewRepo(db, nil),
		TorrentOpts: torrentopts.NewRepo(db),
	}, db
}

func TestPackSkips(t *testing.T) {
	pack := []string{
		"Show.S01-S03/Show.S01E01.1080p.mkv",
		"Show.S01-S03/Show.S01E02.1080p.mkv",
		"Show.S01-S03/Season 2/Show.S02E01.1080p.mkv",
		"Show.S01-S03/Season 2/Show.S02E02.1080p.mkv",
		"Show.S01-S03/Season 2/Show.S02E02.1080p.srt",
		"Show.S01-S03/Season 3/Show.S03E01.1080p.mkv",
		"Show.S01-S03/Season 2/Extras/Behind the scenes.mkv",
		"Show.S01-S03/Specials/Show.S00E01.mkv",
	}
	tests := []struct {
		name     string
		season   int
		episodes []int
		want     []string
	}{
		{"season 2 of a three season pack", 2, nil, []string{
			"Show.S01-S03/Show.S01E01.1080p.mkv", "Show.S01-S03/Show.S01E02.1080p.mkv",
			"Show.S01-S03/Season 3/Show.S03E01.1080p.mkv", "Show.S01-S03/Specials/Show.S00E01.mkv",
		}},
		{"one episode of season 2", 2, []int{2}, []string{
			"Show.S01-S03/Show.S01E01.1080p.mkv", "Show.S01-S03/Show.S01E02.1080p.mkv",
			"Show.S01-S03/Season 2/Show.S02E01.1080p.mkv",
			"Show.S01-S03/Season 3/Show.S03E01.1080p.mkv", "Show.S01-S03/Specials/Show.S00E01.mkv",
		}},
		{"a season the pack does not have keeps everything", 7, nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := packSkips(fileStatesOf(pack), tc.season, tc.episodes)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("skips = %v\nwant    %v", got, tc.want)
			}
		})
	}

	// Names that say nothing about the season stay.
	plain := fileStatesOf([]string{"Pack/01 - Pilot.mkv", "Pack/02 - Second.mkv"})
	if got := packSkips(plain, 2, nil); len(got) != 0 {
		t.Fatalf("files with no season in their name must stay: %v", got)
	}
}

func fileStatesOf(paths []string) []torrentclient.FileState {
	out := make([]torrentclient.FileState, len(paths))
	for i, p := range paths {
		out[i] = torrentclient.FileState{Index: i, Path: p, Size: 1 << 30, Wanted: true}
	}
	return out
}

func TestAutoSkipsFollowTheSettings(t *testing.T) {
	s, _ := optionsServer(t)
	files := []torrentclient.FileState{
		{Path: "Show.S02/Show.S02E01.mkv", Size: 1 << 30},
		{Path: "Show.S02/Show.S03E01.mkv", Size: 1 << 30},
		{Path: "Show.S02/readme.txt", Size: 100},
		{Path: "Show.S02/Sample/sample.mkv", Size: 30 << 20},
	}
	tv := queue.Item{SeriesID: 5, Season: 2}

	// Junk is skipped unless the setting is switched off; packs are left whole.
	got := s.autoSkips(tv, files)
	if want := []string{"Show.S02/Sample/sample.mkv", "Show.S02/readme.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default skips = %v, want %v", got, want)
	}
	if err := s.Settings.Set(settings.KeyTorrentSkipJunk, "0", false); err != nil {
		t.Fatal(err)
	}
	if got := s.autoSkips(tv, files); len(got) != 0 {
		t.Fatalf("with the junk setting off nothing is skipped: %v", got)
	}

	// The pack setting adds the other seasons, for a TV grab only.
	if err := s.Settings.Set(settings.KeyTorrentPackOnlyWanted, "1", false); err != nil {
		t.Fatal(err)
	}
	if got := s.autoSkips(tv, files); !reflect.DeepEqual(got, []string{"Show.S02/Show.S03E01.mkv"}) {
		t.Fatalf("pack skips = %v", got)
	}
	if got := s.autoSkips(queue.Item{MovieID: 1}, files); len(got) != 0 {
		t.Fatalf("a movie is not cut down by season: %v", got)
	}

	// A book keeps its text files.
	if err := s.Settings.Set(settings.KeyTorrentSkipJunk, "1", false); err != nil {
		t.Fatal(err)
	}
	book := []torrentclient.FileState{{Path: "Book/chapter1.txt", Size: 100}, {Path: "Book/Book.epub", Size: 1 << 20}, {Path: "Book/x.nfo", Size: 10}}
	if got := s.autoSkips(queue.Item{BookID: 1}, book); !reflect.DeepEqual(got, []string{"Book/x.nfo"}) {
		t.Fatalf("book skips = %v", got)
	}

	// Never every file.
	only := []torrentclient.FileState{{Path: "a/readme.txt", Size: 1}, {Path: "a/info.nfo", Size: 1}}
	if got := s.autoSkips(queue.Item{MovieID: 1}, only); len(got) != 0 {
		t.Fatalf("skipping everything is not an option: %v", got)
	}
}

func TestFilesLeftOutAreNotImported(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, size int) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		// A sparse file of the full size, as the engine leaves one it only
		// touched at the edge.
		f, err := os.Create(full)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(int64(size)); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	write("Pack/Show.S01E01.mkv", 5_000)
	write("Pack/Show.S01E02.mkv", 9_000) // left out, partly there, and the biggest
	write("Pack/Show.S01E03.mkv", 4_000)

	// Without a note, the biggest file wins.
	if got, err := largestVideoFile(dir); err != nil || filepath.Base(got) != "Show.S01E02.mkv" {
		t.Fatalf("largest = %q, %v", got, err)
	}
	writeSkipped(dir, []string{"Pack/Show.S01E02.mkv"})
	got, err := largestVideoFile(dir)
	if err != nil || filepath.Base(got) != "Show.S01E01.mkv" {
		t.Fatalf("largest with a file left out = %q, %v", got, err)
	}

	all := []string{filepath.Join(dir, "Pack", "Show.S01E01.mkv"), filepath.Join(dir, "Pack", "Show.S01E02.mkv"), filepath.Join(dir, "Pack", "Show.S01E03.mkv")}
	kept := dropSkipped(dir, all, readSkipped(dir))
	if len(kept) != 2 || strings.Contains(strings.Join(kept, ","), "E02") {
		t.Fatalf("kept = %v", kept)
	}

	// Writing an empty list removes the note.
	writeSkipped(dir, nil)
	if readSkipped(dir) != nil {
		t.Fatal("an empty list should remove the note")
	}
}

func TestTorrentsAreFiledUnderTheirCategory(t *testing.T) {
	s, _ := optionsServer(t)
	work := s.downloadsIncompleteDir()
	cfg, err := torrentopts.CategoryConfig{
		Categories: []torrentopts.Category{{Name: "Movies", Subfolder: "movies"}, {Name: "Shows", Subfolder: "tv"}, {Name: "Anywhere"}},
		ByKind:     map[string]string{"movie": "Movies", "tv": "Shows", "music": "Anywhere"},
	}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	saveCategories(t, s, cfg)

	m, _ := s.MovieRepo.Add(library.Movie{TMDBID: 1, Title: "Film", Year: 2026})
	movie, _ := s.QueueRepo.Enqueue(queue.Item{MovieID: m.ID, ReleaseTitle: "Film.2026", Protocol: queue.ProtocolTorrent})
	second, _ := s.QueueRepo.Enqueue(queue.Item{MovieID: m.ID, ReleaseTitle: "Other", Protocol: queue.ProtocolTorrent})

	if got := s.workDirFor(movie); got != filepath.Join(work, queueName(movie)) {
		t.Fatalf("before it starts a download is in the plain folder: %s", got)
	}
	dir := s.assignTorrentDir(movie)
	if want := filepath.Join(work, "movies", queueName(movie)); dir != want {
		t.Fatalf("a movie goes to %s, got %s", want, dir)
	}
	if s.workDirFor(movie) != dir {
		t.Fatal("the folder has to stay the same once it is chosen")
	}

	// Changing the categories does not move a download that already has its folder.
	saveCategories(t, s, torrentopts.CategoryConfig{Categories: []torrentopts.Category{{Name: "Movies", Subfolder: "films"}}, ByKind: map[string]string{"movie": "Movies"}})
	if got := s.assignTorrentDir(movie); got != dir {
		t.Fatalf("the folder changed to %s", got)
	}
	// A new one follows the new list.
	if got := s.assignTorrentDir(second); got != filepath.Join(work, "films", queueName(second)) {
		t.Fatalf("a new download goes to %s", got)
	}
	cats, _ := s.TorrentOpts.Categories()
	if cats[movie] != "Movies" || cats[second] != "Movies" {
		t.Fatalf("categories = %v", cats)
	}

	// Both folders count as category folders, so their downloads can be removed and cleaned up.
	folders := strings.Join(s.categoryFolders(), ",")
	if !strings.Contains(folders, "films") || !strings.Contains(folders, "movies") {
		t.Fatalf("category folders = %s", folders)
	}
	if !s.isWorkDirPath(dir, work) || !s.isWorkDirPath(filepath.Join(work, queueName(5)), work) {
		t.Fatal("a download folder in a category folder or in the working folder is a download's own")
	}
	for _, bad := range []string{
		filepath.Join(work, "elsewhere", queueName(5)),
		filepath.Join(work, "movies", "x", queueName(5)),
		filepath.Join(work, "movies"),
		filepath.Join(filepath.Dir(work), queueName(5)),
		filepath.Join(work, "movies", "notes"),
	} {
		if s.isWorkDirPath(bad, work) {
			t.Fatalf("%s must not count as a download's folder", bad)
		}
	}
}

func queueName(id int64) string { return fmt.Sprintf("queue-%d", id) }

func saveCategories(t *testing.T, s *Server, c torrentopts.CategoryConfig) {
	t.Helper()
	list, _ := json.Marshal(c.Categories)
	byKind, _ := json.Marshal(c.ByKind)
	if err := s.Settings.Set(settings.KeyTorrentCategories, string(list), false); err != nil {
		t.Fatal(err)
	}
	if err := s.Settings.Set(settings.KeyTorrentCategoryMap, string(byKind), false); err != nil {
		t.Fatal(err)
	}
}

func TestAFolderAnOlderVersionStartedStaysPlain(t *testing.T) {
	s, _ := optionsServer(t)
	saveCategories(t, s, torrentopts.CategoryConfig{Categories: []torrentopts.Category{{Name: "Movies", Subfolder: "movies"}}, ByKind: map[string]string{"movie": "Movies"}})
	m, _ := s.MovieRepo.Add(library.Movie{TMDBID: 1, Title: "Film", Year: 2026})
	id, _ := s.QueueRepo.Enqueue(queue.Item{MovieID: m.ID, ReleaseTitle: "Film.2026", Protocol: queue.ProtocolTorrent})
	plain := filepath.Join(s.downloadsIncompleteDir(), queueName(id))
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := s.assignTorrentDir(id); got != plain {
		t.Fatalf("a download whose files are already in the plain folder stays there, got %s", got)
	}
}

func TestSeedGoalUsesTheIndexersRule(t *testing.T) {
	s, _ := optionsServer(t)
	inst, err := s.IndexerRepo.Create(indexers.Instance{Name: "Private", BaseURL: "https://t.example", Protocol: indexers.ProtocolTorrent, Kind: indexers.KindTorznab, Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := s.MovieRepo.Add(library.Movie{TMDBID: 1, Title: "Film", Year: 2026})
	withIndexer, _ := s.QueueRepo.Enqueue(queue.Item{MovieID: m.ID, IndexerID: sql.NullInt64{Int64: inst.ID, Valid: true}, ReleaseTitle: "A", Protocol: queue.ProtocolTorrent})
	without, _ := s.QueueRepo.Enqueue(queue.Item{MovieID: m.ID, ReleaseTitle: "B", Protocol: queue.ProtocolTorrent})

	if err := s.Settings.Set(settings.KeyTorrentSeedRatioLimit, "2", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Settings.Set(settings.KeyTorrentSeedTimeLimitH, "48", false); err != nil {
		t.Fatal(err)
	}
	global := torrentclient.SeedGoal{Ratio: 2, Time: 48 * time.Hour}
	if got := s.torrentSeedGoal(withIndexer); got != global {
		t.Fatalf("with no rule the global limits apply: %+v", got)
	}

	ratio, hours := 1.0, 72.0
	if err := s.TorrentOpts.SetSeedRule(inst.ID, torrentopts.SeedRule{Ratio: &ratio, Hours: &hours}); err != nil {
		t.Fatal(err)
	}
	if got := s.torrentSeedGoal(withIndexer); got != (torrentclient.SeedGoal{Ratio: 1, Time: 72 * time.Hour}) {
		t.Fatalf("the rule replaces the global limits: %+v", got)
	}
	if got := s.torrentSeedGoal(without); got != global {
		t.Fatalf("a download from no indexer keeps the global limits: %+v", got)
	}

	// Only the ratio: the hours still come from the global limit.
	if err := s.TorrentOpts.SetSeedRule(inst.ID, torrentopts.SeedRule{Ratio: &ratio}); err != nil {
		t.Fatal(err)
	}
	if got := s.torrentSeedGoal(withIndexer); got != (torrentclient.SeedGoal{Ratio: 1, Time: 48 * time.Hour}) {
		t.Fatalf("goal = %+v", got)
	}
	// Zero hours: no time limit at all.
	zero := 0.0
	if err := s.TorrentOpts.SetSeedRule(inst.ID, torrentopts.SeedRule{Hours: &zero}); err != nil {
		t.Fatal(err)
	}
	if got := s.torrentSeedGoal(withIndexer); got != (torrentclient.SeedGoal{Ratio: 2}) {
		t.Fatalf("goal = %+v", got)
	}
}

func TestUploadLimitFollowsTheSpeedHours(t *testing.T) {
	s, _ := optionsServer(t)
	t.Cleanup(func() { speed.Set(0); speed.SetUpload(0) })
	set := func(key, value string) {
		t.Helper()
		if err := s.Settings.Set(key, value, false); err != nil {
			t.Fatal(err)
		}
	}
	noon := time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)
	night := time.Date(2026, 10, 9, 3, 0, 0, 0, time.Local)

	s.applySpeedLimit(noon)
	if speed.Current() != 0 || speed.CurrentUpload() != 0 {
		t.Fatalf("with nothing set there is no limit: down %d up %d", speed.Current(), speed.CurrentUpload())
	}
	set(settings.KeyTorrentUploadLimitKB, "300")
	s.applySpeedLimit(noon)
	if speed.CurrentUpload() != 300<<10 || speed.Current() != 0 {
		t.Fatalf("an upload limit does not touch the download limit: down %d up %d", speed.Current(), speed.CurrentUpload())
	}
	set(settings.KeySpeedLimitMB, "5")
	set(settings.KeySpeedLimitHours, "8-23")
	s.applySpeedLimit(noon)
	if speed.Current() != 5<<20 || speed.CurrentUpload() != 300<<10 {
		t.Fatalf("inside the hours both limits apply: down %d up %d", speed.Current(), speed.CurrentUpload())
	}
	s.applySpeedLimit(night)
	if speed.Current() != 0 || speed.CurrentUpload() != 0 {
		t.Fatalf("outside the hours neither applies: down %d up %d", speed.Current(), speed.CurrentUpload())
	}
}

func TestSettingsReadersDefaultToTodaysBehaviour(t *testing.T) {
	s, _ := optionsServer(t)
	if s.torrentSequentialDefault() || s.torrentPackOnlyWanted() || s.torrentUploadLimitKB() != 0 {
		t.Fatal("sequential, pack filtering and an upload limit are all off until chosen")
	}
	if !s.torrentPortMapping() {
		t.Fatal("port mapping was on before, so it stays on")
	}
	if !s.torrentSkipJunk() {
		t.Fatal("junk is skipped unless it is switched off")
	}
	if _, ok := s.torrentCategoryConfig().For(torrentopts.KindMovie); ok {
		t.Fatal("there are no categories until some are made")
	}
}
