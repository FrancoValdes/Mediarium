package library

import (
	"fmt"
	"strings"
	"time"
)

// Art is a title's backdrop and logo, as TMDB image paths.
type Art struct {
	Backdrop  string
	Logo      string
	CheckedAt string
}

// TitleArt returns what is known about the artwork of the movies or shows
// (kind "movie" or "tv") with these TMDB ids. Titles never asked about are
// not in the map.
func (r *Repo) TitleArt(kind string, ids []int) (map[int]Art, error) {
	out := map[int]Art{}
	for start := 0; start < len(ids); start += 400 {
		end := min(start+400, len(ids))
		chunk := ids[start:end]
		marks := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := []any{kind}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := r.db.Query(`SELECT tmdb_id, backdrop, logo, checked_at FROM title_art WHERE kind = ? AND tmdb_id IN (`+marks+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("read title art: %w", err)
		}
		for rows.Next() {
			var id int
			var a Art
			if err := rows.Scan(&id, &a.Backdrop, &a.Logo, &a.CheckedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan title art: %w", err)
			}
			out[id] = a
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// SaveTitleArt records the artwork TMDB gave for a title (empty when it has none).
func (r *Repo) SaveTitleArt(kind string, tmdbID int, backdrop, logo string, now time.Time) error {
	_, err := r.db.Exec(`INSERT INTO title_art (kind, tmdb_id, backdrop, logo, checked_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (kind, tmdb_id) DO UPDATE SET backdrop = excluded.backdrop, logo = excluded.logo, checked_at = excluded.checked_at`,
		kind, tmdbID, backdrop, logo, now.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save title art: %w", err)
	}
	return nil
}
