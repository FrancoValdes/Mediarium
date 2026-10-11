package torrentopts_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/queue"
	"github.com/rdborg/mediarium/internal/store"
	"github.com/rdborg/mediarium/internal/torrentopts"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func queueItem(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	m, err := library.NewRepo(db).Add(library.Movie{TMDBID: 1, Title: "Film", Year: 2026})
	if err != nil {
		t.Fatal(err)
	}
	id, err := queue.NewRepo(db).Enqueue(queue.Item{MovieID: m.ID, ReleaseTitle: "Film.2026.1080p", Protocol: queue.ProtocolTorrent})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestOptionsDefaultToFollowingTheSettings(t *testing.T) {
	db := openDB(t)
	repo := torrentopts.NewRepo(db)
	id := queueItem(t, db)

	o, ok, err := repo.Get(id)
	if err != nil || ok {
		t.Fatalf("Get = %+v, %v, %v; an item nobody set anything for has no row", o, ok, err)
	}
	if o.Sequential != torrentopts.Follow || o.DownKB != 0 || o.UpKB != 0 || o.FilesDecided || len(o.SkipFiles) != 0 {
		t.Fatalf("defaults = %+v", o)
	}
}

func TestOptionsAreKeptPerField(t *testing.T) {
	db := openDB(t)
	repo := torrentopts.NewRepo(db)
	id := queueItem(t, db)

	if err := repo.SetSequential(id, torrentopts.On); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetLimits(id, 500, 80); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSkipped(id, []string{"Show/e01.nfo", "Show/Sample/s.mkv"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetCategory(id, "Movies", "movies"); err != nil {
		t.Fatal(err)
	}
	o, ok, err := repo.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get = %+v, %v, %v", o, ok, err)
	}
	if o.Sequential != torrentopts.On || o.DownKB != 500 || o.UpKB != 80 || !o.FilesDecided || len(o.SkipFiles) != 2 || o.Category != "Movies" || o.Subfolder != "movies" || !o.DirDecided {
		t.Fatalf("options = %+v", o)
	}

	// Changing one field leaves the others alone.
	if err := repo.SetSequential(id, torrentopts.Off); err != nil {
		t.Fatal(err)
	}
	o, _, _ = repo.Get(id)
	if o.Sequential != torrentopts.Off || o.DownKB != 500 || len(o.SkipFiles) != 2 || o.Category != "Movies" {
		t.Fatalf("after changing the order: %+v", o)
	}
	if err := repo.SetSequential(id, 7); err == nil {
		t.Fatal("7 is not a valid choice")
	}
	if err := repo.SetLimits(id, -1, 0); err == nil {
		t.Fatal("a negative limit must be refused")
	}

	cats, err := repo.Categories()
	if err != nil || cats[id] != "Movies" || len(cats) != 1 {
		t.Fatalf("categories = %v, %v", cats, err)
	}
}

func TestOptionsGoWithTheDownload(t *testing.T) {
	db := openDB(t)
	repo := torrentopts.NewRepo(db)
	id := queueItem(t, db)
	if err := repo.SetLimits(id, 100, 0); err != nil {
		t.Fatal(err)
	}
	if err := queue.NewRepo(db).Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := repo.Get(id); ok {
		t.Fatal("removing the download should remove its options")
	}
}

func TestSeedRules(t *testing.T) {
	db := openDB(t)
	repo := torrentopts.NewRepo(db)
	indexerRepo := indexers.NewRepo(db, nil)
	inst, err := indexerRepo.Create(indexers.Instance{Name: "Tracker", BaseURL: "https://tracker.example", Protocol: indexers.ProtocolTorrent, Kind: indexers.KindTorznab, Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if rule, err := repo.SeedRule(inst.ID); err != nil || !rule.Empty() {
		t.Fatalf("a new indexer follows the global rule: %+v, %v", rule, err)
	}
	ratio, hours := 1.5, 72.0
	if err := repo.SetSeedRule(inst.ID, torrentopts.SeedRule{Ratio: &ratio, Hours: &hours}); err != nil {
		t.Fatal(err)
	}
	rule, _ := repo.SeedRule(inst.ID)
	if rule.Ratio == nil || *rule.Ratio != 1.5 || rule.Hours == nil || *rule.Hours != 72 {
		t.Fatalf("rule = %+v", rule)
	}
	all, _ := repo.SeedRules()
	if len(all) != 1 || all[inst.ID].Ratio == nil {
		t.Fatalf("all rules = %+v", all)
	}

	// Only the ratio: the hours follow the global rule.
	if err := repo.SetSeedRule(inst.ID, torrentopts.SeedRule{Ratio: &ratio}); err != nil {
		t.Fatal(err)
	}
	rule, _ = repo.SeedRule(inst.ID)
	r, h := rule.Goal(2, 48)
	if r != 1.5 || h != 48 {
		t.Fatalf("goal = %v, %v; want ratio 1.5 from the rule and 48 hours from the global limit", r, h)
	}
	// Zero means no limit, not "follow".
	zero := 0.0
	r, h = torrentopts.SeedRule{Hours: &zero}.Goal(2, 48)
	if r != 2 || h != 0 {
		t.Fatalf("goal = %v, %v", r, h)
	}

	bad := -1.0
	if err := repo.SetSeedRule(inst.ID, torrentopts.SeedRule{Ratio: &bad}); err == nil {
		t.Fatal("a negative ratio must be refused")
	}
	huge := 1e6
	if err := repo.SetSeedRule(inst.ID, torrentopts.SeedRule{Hours: &huge}); err == nil {
		t.Fatal("a million hours must be refused")
	}

	// An empty rule removes it, and so does removing the indexer.
	if err := repo.SetSeedRule(inst.ID, torrentopts.SeedRule{}); err != nil {
		t.Fatal(err)
	}
	if all, _ := repo.SeedRules(); len(all) != 0 {
		t.Fatalf("rules after clearing = %+v", all)
	}
	_ = repo.SetSeedRule(inst.ID, torrentopts.SeedRule{Ratio: &ratio})
	if err := indexerRepo.Delete(inst.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := repo.SeedRules(); len(all) != 0 {
		t.Fatalf("a removed indexer keeps its rule: %+v", all)
	}
}

func TestCleanSubfolder(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"  movies ", "movies", false},
		{"TV shows", "TV shows", false},
		{"a/b", "", true},
		{`a\b`, "", true},
		{"..", "", true},
		{".hidden", "", true},
		{"queue-12", "", true},
		{"Queue-1", "", true},
		{"what?", "", true},
		{strings.Repeat("x", 61), "", true},
		{"tab\there", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := torrentopts.CleanSubfolder(tc.in)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("CleanSubfolder(%q) = %q, %v", tc.in, got, err)
			}
		})
	}
}

func TestCategoryConfig(t *testing.T) {
	c := torrentopts.ParseCategories(
		`[{"name":"Movies","subfolder":"movies"},{"name":"Shows","subfolder":"tv"},{"name":"Plain","subfolder":""}]`,
		`{"movie":"movies","tv":"Shows"}`)
	if cat, ok := c.For(torrentopts.KindMovie); !ok || cat.Name != "Movies" || cat.Subfolder != "movies" {
		t.Fatalf("movie category = %+v, %v (names match without regard to case)", cat, ok)
	}
	if _, ok := c.For(torrentopts.KindMusic); ok {
		t.Fatal("a kind with no category has none: the plain default")
	}
	if _, ok := torrentopts.ParseCategories("", "").For(torrentopts.KindTV); ok {
		t.Fatal("nothing saved means no categories")
	}
	if got := torrentopts.ParseCategories("{not json", "[]"); len(got.Categories) != 0 || got.ByKind == nil {
		t.Fatalf("bad text should read as empty: %+v", got)
	}

	good, err := c.Validate()
	if err != nil || good.ByKind["movie"] != "Movies" || len(good.Categories) != 3 {
		t.Fatalf("a good configuration was refused or changed: %+v, %v", good, err)
	}

	bad := map[string]torrentopts.CategoryConfig{
		"no name":           {Categories: []torrentopts.Category{{Name: " "}}},
		"same name":         {Categories: []torrentopts.Category{{Name: "A"}, {Name: "a"}}},
		"same folder":       {Categories: []torrentopts.Category{{Name: "A", Subfolder: "x"}, {Name: "B", Subfolder: "X"}}},
		"path as folder":    {Categories: []torrentopts.Category{{Name: "A", Subfolder: "../etc"}}},
		"unknown target":    {Categories: []torrentopts.Category{{Name: "A"}}, ByKind: map[string]string{"movie": "B"}},
		"unknown kind":      {Categories: []torrentopts.Category{{Name: "A"}}, ByKind: map[string]string{"game": "A"}},
		"too many":          {Categories: make([]torrentopts.Category, torrentopts.MaxCategories+1)},
		"name way too long": {Categories: []torrentopts.Category{{Name: strings.Repeat("n", 41)}}},
	}
	for name, cfg := range bad {
		if _, err := cfg.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
