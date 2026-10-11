package library

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// What the Radarr/Sonarr-style API (internal/api/arr*.go) needs from the
// library beyond the usual lists: a number for each tag, and episode counts
// per season.

// ArrTag is a tag with the number apps built for Radarr and Sonarr know it by.
type ArrTag struct {
	ID    int64
	Label string
}

// ArrTags lists every tag with its number, giving tags that don't have one
// yet a number first.
func (r *Repo) ArrTags() ([]ArrTag, error) {
	used, err := r.Tags()
	if err != nil {
		return nil, err
	}
	for _, t := range used {
		if _, err := r.db.Exec(`INSERT OR IGNORE INTO arr_tags (label) VALUES (?)`, t.Name); err != nil {
			return nil, fmt.Errorf("number tag: %w", err)
		}
	}
	rows, err := r.db.Query(`SELECT id, label FROM arr_tags ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list tag numbers: %w", err)
	}
	defer rows.Close()
	out := []ArrTag{}
	for rows.Next() {
		var t ArrTag
		if err := rows.Scan(&t.ID, &t.Label); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ArrTagFor returns the tag with that label, numbering it if it's new.
func (r *Repo) ArrTagFor(label string) (ArrTag, error) {
	norm := NormalizeTags([]string{label})
	if len(norm) != 1 {
		return ArrTag{}, fmt.Errorf("%w: a tag needs a name", ErrInvalidTag)
	}
	if _, err := r.db.Exec(`INSERT OR IGNORE INTO arr_tags (label) VALUES (?)`, norm[0]); err != nil {
		return ArrTag{}, fmt.Errorf("number tag: %w", err)
	}
	var t ArrTag
	err := r.db.QueryRow(`SELECT id, label FROM arr_tags WHERE label = ? COLLATE NOCASE`, norm[0]).Scan(&t.ID, &t.Label)
	return t, err
}

// ErrInvalidTag is a tag without a usable name.
var ErrInvalidTag = errors.New("invalid tag")

// ArrTagLabels turns tag numbers into names; unknown numbers are skipped.
func (r *Repo) ArrTagLabels(ids []int64) ([]string, error) {
	out := []string{}
	for _, id := range ids {
		var label string
		err := r.db.QueryRow(`SELECT label FROM arr_tags WHERE id = ?`, id).Scan(&label)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, label)
	}
	return out, nil
}

// RenameArrTag renames a numbered tag, on every title that has it too.
func (r *Repo) RenameArrTag(id int64, label string) (ArrTag, error) {
	norm := NormalizeTags([]string{label})
	if len(norm) != 1 {
		return ArrTag{}, fmt.Errorf("%w: a tag needs a name", ErrInvalidTag)
	}
	var old string
	if err := r.db.QueryRow(`SELECT label FROM arr_tags WHERE id = ?`, id).Scan(&old); err != nil {
		return ArrTag{}, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return ArrTag{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE arr_tags SET label = ? WHERE id = ?`, norm[0], id); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ArrTag{}, fmt.Errorf("%w: another tag already has that name", ErrInvalidTag)
		}
		return ArrTag{}, err
	}
	for _, table := range []string{"movie_tags", "series_tags"} {
		if _, err := tx.Exec(`UPDATE OR IGNORE `+table+` SET tag = ? WHERE tag = ? COLLATE NOCASE`, norm[0], old); err != nil {
			return ArrTag{}, fmt.Errorf("rename tag: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return ArrTag{}, err
	}
	return ArrTag{ID: id, Label: norm[0]}, nil
}

// SeasonStat counts one season's episodes.
type SeasonStat struct {
	Total      int // every episode
	Aired      int // aired by today, or already downloaded
	Downloaded int
	Monitored  int // monitored episodes
}

// SeasonStats counts the episodes of every show per season, in one query.
// today is "YYYY-MM-DD".
func (r *Repo) SeasonStats(today string) (map[int64]map[int]SeasonStat, error) {
	rows, err := r.db.Query(`SELECT series_id, season, COUNT(*),
		SUM(CASE WHEN status = 'downloaded' OR (air_date != '' AND air_date <= ?) THEN 1 ELSE 0 END),
		SUM(CASE WHEN status = 'downloaded' THEN 1 ELSE 0 END),
		SUM(CASE WHEN monitored = 1 THEN 1 ELSE 0 END)
		FROM episodes GROUP BY series_id, season`, today)
	if err != nil {
		return nil, fmt.Errorf("season stats: %w", err)
	}
	defer rows.Close()
	out := map[int64]map[int]SeasonStat{}
	for rows.Next() {
		var (
			id     int64
			season int
			st     SeasonStat
		)
		if err := rows.Scan(&id, &season, &st.Total, &st.Aired, &st.Downloaded, &st.Monitored); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[int]SeasonStat{}
		}
		out[id][season] = st
	}
	return out, rows.Err()
}

// AddedTimes maps each movie (or show, when series is set) to when it was
// added (RFC 3339 text).
func (r *Repo) AddedTimes(series bool) (map[int64]string, error) {
	table := "movies"
	if series {
		table = "series"
	}
	rows, err := r.db.Query(`SELECT id, added_at FROM ` + table)
	if err != nil {
		return nil, fmt.Errorf("added times: %w", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var (
			id int64
			at string
		)
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		out[id] = at
	}
	return out, rows.Err()
}
