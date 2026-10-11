package library

import (
	"database/sql"
	"errors"
	"fmt"
)

// MovieByFile returns the movie whose file is at path.
func (r *Repo) MovieByFile(path string) (Movie, bool, error) {
	m, err := r.scanOne(r.db.QueryRow(`SELECT `+movieColumns+` FROM movies WHERE file_path = ? ORDER BY id LIMIT 1`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return Movie{}, false, nil
	}
	if err != nil {
		return Movie{}, false, err
	}
	return m, true, nil
}

// EpisodesByFiles returns the episodes whose file is one of paths, by show
// and then season and episode (a file that holds two episodes gives both).
func (r *Repo) EpisodesByFiles(paths []string) ([]Episode, error) {
	var out []Episode
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		rows, err := r.db.Query(episodeSelect+` WHERE file_path = ? ORDER BY series_id, season, episode`, p)
		if err != nil {
			return nil, fmt.Errorf("find episodes of %s: %w", p, err)
		}
		for rows.Next() {
			e, err := scanEpisode(rows.Scan)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}
