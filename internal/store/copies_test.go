package store_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/store"
)

// Upgrading to second copies rebuilds the movies and series tables. Every
// row and every link to them has to come through, and the foreign keys have
// to work afterwards.
func TestCopiesMigrationKeepsEverything(t *testing.T) {
	names := migrationNames(t)
	upTo := -1
	for i, n := range names {
		if strings.HasPrefix(n, "0046_") {
			upTo = i
		}
	}
	if upTo < 0 {
		t.Fatal("no 0046 migration")
	}
	path := oldInstall(t, upTo, func(db *sql.DB) {
		mustExec(t, db, `INSERT INTO movies (id, tmdb_id, title, year, status, quality, file_path, genres, root_path) VALUES (7, 603, 'The Matrix', 1999, 'downloaded', 'Bluray-1080p', '/movies/The Matrix (1999)/m.mkv', '[]', '')`)
		mustExec(t, db, `INSERT INTO movies (id, tmdb_id, title) VALUES (9, 604, 'The Matrix Reloaded')`)
		mustExec(t, db, `DELETE FROM movies WHERE id = 9`) // the id counter is past 9 now
		mustExec(t, db, `INSERT INTO movie_tags (movie_id, tag) VALUES (7, 'kids')`)
		mustExec(t, db, `INSERT INTO download_queue (id, movie_id, release_title) VALUES (3, 7, 'The.Matrix.1999.1080p')`)
		mustExec(t, db, `INSERT INTO activity (movie_id, event_type, message) VALUES (7, 'grabbed', 'x')`)
		mustExec(t, db, `INSERT INTO series (id, tmdb_id, title, series_type) VALUES (4, 1399, 'A Show', 'anime')`)
		mustExec(t, db, `INSERT INTO episodes (series_id, season, episode, status) VALUES (4, 1, 1, 'downloaded')`)
		mustExec(t, db, `INSERT INTO series_tags (series_id, tag) VALUES (4, 'kids')`)
		mustExec(t, db, `INSERT INTO download_queue (id, series_id, season, episode, release_title) VALUES (5, 4, 1, 1, 'A.Show.S01E01')`)
	})

	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer db.Close()

	one := func(q string, args ...any) any {
		t.Helper()
		var v any
		if err := db.QueryRow(q, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}
	if got := one(`SELECT title || '|' || quality || '|' || file_path FROM movies WHERE id = 7`); got != "The Matrix|Bluray-1080p|/movies/The Matrix (1999)/m.mkv" {
		t.Fatalf("movie: %v", got)
	}
	if got := one(`SELECT series_type FROM series WHERE id = 4`); got != "anime" {
		t.Fatalf("series: %v", got)
	}
	for q, want := range map[string]int64{
		`SELECT COUNT(*) FROM movie_tags WHERE movie_id = 7`:    1,
		`SELECT COUNT(*) FROM download_queue`:                   2,
		`SELECT COUNT(*) FROM activity WHERE movie_id = 7`:      1,
		`SELECT COUNT(*) FROM episodes WHERE series_id = 4`:     1,
		`SELECT COUNT(*) FROM series_tags WHERE series_id = 4`:  1,
		`SELECT COUNT(*) FROM movies WHERE copy_of IS NOT NULL`: 0,
		`SELECT COUNT(*) FROM pragma_foreign_key_check`:         0,
	} {
		if got := one(q); got != want {
			t.Errorf("%s = %v, want %d", q, got, want)
		}
	}

	// A removed movie's id isn't handed out again.
	res, err := db.Exec(`INSERT INTO movies (tmdb_id, title) VALUES (605, 'New')`)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id <= 9 {
		t.Fatalf("new id %d reuses a removed one", id)
	}

	// The same title once as a main title, then once per profile as a copy.
	if _, err := db.Exec(`INSERT INTO movies (tmdb_id, title) VALUES (603, 'The Matrix')`); err == nil {
		t.Fatal("a second main row for the same title was allowed")
	}
	mustExec(t, db, `INSERT INTO quality_profiles (id, name, allowed_tiers, cutoff) VALUES (90, '4K test', 'WEBDL-2160p', 'WEBDL-2160p')`)
	mustExec(t, db, `INSERT INTO movies (id, tmdb_id, title, copy_of, profile_id) VALUES (50, 603, 'The Matrix', 7, 90)`)
	if _, err := db.Exec(`INSERT INTO movies (tmdb_id, title, copy_of, profile_id) VALUES (603, 'The Matrix', 7, 90)`); err == nil {
		t.Fatal("two copies with the same profile were allowed")
	}
	mustExec(t, db, `INSERT INTO series (id, tmdb_id, title, copy_of, profile_id) VALUES (40, 1399, 'A Show', 4, 90)`)

	// Foreign keys are on again: removing the main title takes its copy and
	// its queue rows with it, and a row pointing at nothing is refused.
	mustExec(t, db, `DELETE FROM movies WHERE id = 7`)
	if got := one(`SELECT COUNT(*) FROM movies WHERE id IN (7, 50)`); got != int64(0) {
		t.Fatalf("copy left behind: %v", got)
	}
	if got := one(`SELECT COUNT(*) FROM download_queue WHERE id = 3`); got != int64(0) {
		t.Fatalf("queue row left behind: %v", got)
	}
	mustExec(t, db, `DELETE FROM series WHERE id = 4`)
	if got := one(`SELECT COUNT(*) FROM series WHERE id = 40`); got != int64(0) {
		t.Fatalf("series copy left behind: %v", got)
	}
	if _, err := db.Exec(`INSERT INTO movie_tags (movie_id, tag) VALUES (12345, 'x')`); err == nil {
		t.Fatal("foreign keys are off after the upgrade")
	}
}

// A database that already has a row pointing at nothing (from long ago)
// still upgrades: only new broken links stop a rebuild.
func TestCopiesMigrationToleratesOldBrokenLinks(t *testing.T) {
	names := migrationNames(t)
	upTo := 0
	for i, n := range names {
		if strings.HasPrefix(n, "0046_") {
			upTo = i
		}
	}
	path := oldInstall(t, upTo, func(db *sql.DB) {
		mustExec(t, db, `PRAGMA foreign_keys = OFF`)
		mustExec(t, db, `INSERT INTO movie_tags (movie_id, tag) VALUES (999, 'orphan')`)
		mustExec(t, db, `PRAGMA foreign_keys = ON`)
		mustExec(t, db, `INSERT INTO movies (id, tmdb_id, title) VALUES (1, 603, 'The Matrix')`)
	})
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("upgrade with an old broken link: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM movies`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("movies: %d %v", n, err)
	}
}
