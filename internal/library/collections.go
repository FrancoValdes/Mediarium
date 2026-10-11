package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Collection is a TMDB collection (a series of films) that has a film in the
// library or that someone chose to follow, with the choices for adding the
// films that come out later.
type Collection struct {
	TMDBID       int
	Name         string
	Overview     string
	PosterPath   string
	BackdropPath string
	// Follow adds new films of the collection by itself. FollowProfileID is the
	// quality profile they get (0 = the default profile), FollowRootPath the
	// library folder ("" = the main one) and FollowMonitored whether they are
	// monitored.
	Follow          bool
	FollowProfileID int64
	FollowRootPath  string
	FollowMonitored bool
	// KnownParts are the TMDB ids of the films the collection had when the
	// choice to follow it was last settled: only a film not in this list is new.
	KnownParts []int
	// CheckedAt is when the collection was last looked at for new films
	// (RFC 3339, "" = never).
	CheckedAt string
}

const collectionColumns = `tmdb_id, name, overview, poster_path, backdrop_path, follow, COALESCE(follow_profile_id, 0), follow_root_path, follow_monitored, known_parts, checked_at`

func scanCollection(scan func(dest ...any) error) (Collection, error) {
	var (
		c     Collection
		known string
	)
	if err := scan(&c.TMDBID, &c.Name, &c.Overview, &c.PosterPath, &c.BackdropPath, &c.Follow, &c.FollowProfileID, &c.FollowRootPath, &c.FollowMonitored, &known, &c.CheckedAt); err != nil {
		return Collection{}, fmt.Errorf("scan collection: %w", err)
	}
	if known != "" {
		_ = json.Unmarshal([]byte(known), &c.KnownParts) // a damaged list reads as empty: nothing counts as known
	}
	return c, nil
}

// SaveCollection stores a collection's name and pictures. The follow choices
// of one that is already stored are kept.
func (r *Repo) SaveCollection(c Collection) error {
	_, err := r.db.Exec(`
		INSERT INTO collections (tmdb_id, name, overview, poster_path, backdrop_path) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tmdb_id) DO UPDATE SET
			name = excluded.name,
			overview = CASE WHEN excluded.overview <> '' THEN excluded.overview ELSE collections.overview END,
			poster_path = CASE WHEN excluded.poster_path <> '' THEN excluded.poster_path ELSE collections.poster_path END,
			backdrop_path = CASE WHEN excluded.backdrop_path <> '' THEN excluded.backdrop_path ELSE collections.backdrop_path END`,
		c.TMDBID, c.Name, c.Overview, c.PosterPath, c.BackdropPath)
	if err != nil {
		return fmt.Errorf("save collection %d: %w", c.TMDBID, err)
	}
	return nil
}

// GetCollection returns a stored collection.
func (r *Repo) GetCollection(tmdbID int) (Collection, bool, error) {
	c, err := scanCollection(r.db.QueryRow(`SELECT `+collectionColumns+` FROM collections WHERE tmdb_id = ?`, tmdbID).Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Collection{}, false, nil
		}
		return Collection{}, false, err
	}
	return c, true, nil
}

// CollectionInLibrary is a collection with how many of its films are in the
// library and which ones.
type CollectionInLibrary struct {
	Collection
	MovieIDs []int64
}

// ListCollections lists the collections that have at least one film in the
// library, or that are followed, biggest first.
func (r *Repo) ListCollections() ([]CollectionInLibrary, error) {
	rows, err := r.db.Query(`SELECT ` + collectionColumns + ` FROM collections
		WHERE follow = 1 OR tmdb_id IN (SELECT collection_id FROM movies WHERE collection_id > 0 AND copy_of IS NULL)
		ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()
	var out []CollectionInLibrary
	byID := map[int]int{}
	for rows.Next() {
		c, err := scanCollection(rows.Scan)
		if err != nil {
			return nil, err
		}
		byID[c.TMDBID] = len(out)
		out = append(out, CollectionInLibrary{Collection: c, MovieIDs: []int64{}})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	mrows, err := r.db.Query(`SELECT id, collection_id FROM movies WHERE collection_id > 0 AND copy_of IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list collection movies: %w", err)
	}
	defer mrows.Close()
	for mrows.Next() {
		var id int64
		var col int
		if err := mrows.Scan(&id, &col); err != nil {
			return nil, err
		}
		if i, ok := byID[col]; ok {
			out[i].MovieIDs = append(out[i].MovieIDs, id)
		}
	}
	return out, mrows.Err()
}

// FollowedCollections lists the collections whose new films are added by
// themselves, the one looked at longest ago first.
func (r *Repo) FollowedCollections() ([]Collection, error) {
	rows, err := r.db.Query(`SELECT ` + collectionColumns + ` FROM collections WHERE follow = 1 ORDER BY checked_at, tmdb_id`)
	if err != nil {
		return nil, fmt.Errorf("list followed collections: %w", err)
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		c, err := scanCollection(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCollectionFollow turns "also add new films" on or off, with what the
// films are added with. Turning it off keeps the choices for next time.
func (r *Repo) SetCollectionFollow(tmdbID int, follow bool, profileID int64, rootPath string, monitored bool) error {
	res, err := r.db.Exec(`UPDATE collections SET follow = ?, follow_profile_id = ?, follow_root_path = ?, follow_monitored = ? WHERE tmdb_id = ?`,
		follow, nullID(profileID), rootPath, monitored, tmdbID)
	if err != nil {
		return fmt.Errorf("follow collection %d: %w", tmdbID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("collection %d is not stored: %w", tmdbID, sql.ErrNoRows)
	}
	return nil
}

// SetCollectionKnown records the films a followed collection has now, so only
// films that come later count as new.
func (r *Repo) SetCollectionKnown(tmdbID int, ids []int) error {
	if ids == nil {
		ids = []int{}
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("encode known films: %w", err)
	}
	if _, err := r.db.Exec(`UPDATE collections SET known_parts = ? WHERE tmdb_id = ?`, string(b), tmdbID); err != nil {
		return fmt.Errorf("set known films of collection %d: %w", tmdbID, err)
	}
	return nil
}

// MarkCollectionChecked records that a collection was just looked at.
func (r *Repo) MarkCollectionChecked(tmdbID int, at time.Time) error {
	_, err := r.db.Exec(`UPDATE collections SET checked_at = ? WHERE tmdb_id = ?`, at.UTC().Format(time.RFC3339), tmdbID)
	if err != nil {
		return fmt.Errorf("mark collection %d checked: %w", tmdbID, err)
	}
	return nil
}

// SetMovieCollection records which collection a movie belongs to (0 = none)
// and that TMDB has been asked.
func (r *Repo) SetMovieCollection(movieID int64, collectionID int) error {
	if _, err := r.db.Exec(`UPDATE movies SET collection_id = ?, collection_checked = 1 WHERE id = ?`, collectionID, movieID); err != nil {
		return fmt.Errorf("set movie %d collection: %w", movieID, err)
	}
	return nil
}

// CollectionGap is a movie whose collection has not been looked up yet.
type CollectionGap struct {
	ID     int64
	TMDBID int
	Title  string
}

// MoviesWithoutCollectionCheck lists up to limit movies (newest first) that
// TMDB has not been asked about yet. Second copies share the main movie's
// collection and are left out.
func (r *Repo) MoviesWithoutCollectionCheck(limit int) ([]CollectionGap, error) {
	rows, err := r.db.Query(`SELECT id, tmdb_id, title FROM movies WHERE collection_checked = 0 AND copy_of IS NULL AND tmdb_id > 0 ORDER BY added_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list movies without a collection check: %w", err)
	}
	defer rows.Close()
	var out []CollectionGap
	for rows.Next() {
		var g CollectionGap
		if err := rows.Scan(&g.ID, &g.TMDBID, &g.Title); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CollectionOfMovie returns the collection a movie was found to belong to (0 = none).
func (r *Repo) CollectionOfMovie(movieID int64) (int, error) {
	var id int
	if err := r.db.QueryRow(`SELECT collection_id FROM movies WHERE id = ?`, movieID).Scan(&id); err != nil {
		return 0, fmt.Errorf("collection of movie %d: %w", movieID, err)
	}
	return id, nil
}

// MovieTMDBIDs returns the TMDB ids of every movie in the library (second
// copies are the same film and add nothing).
func (r *Repo) MovieTMDBIDs() (map[int]int64, error) {
	rows, err := r.db.Query(`SELECT tmdb_id, id FROM movies WHERE copy_of IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("list movie ids: %w", err)
	}
	defer rows.Close()
	out := map[int]int64{}
	for rows.Next() {
		var tmdb int
		var id int64
		if err := rows.Scan(&tmdb, &id); err != nil {
			return nil, err
		}
		out[tmdb] = id
	}
	return out, rows.Err()
}
