package library

import (
	"database/sql"
	"fmt"
)

// ApplyMove records that a title's files moved to another library folder: the
// movie's file or the show's episode files, the subtitles Mediarium keeps track
// of, and the library folder the title now belongs to (root, "" for the main
// folder). mapPath gives a path's new place; a path it does not cover stays as
// it is. It is all done in one step or not at all.
func (r *Repo) ApplyMove(kind string, id int64, root string, mapPath func(string) (string, bool)) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin move: %w", err)
	}
	defer tx.Rollback()

	switch kind {
	case KindMovie:
		var file sql.NullString
		if err := tx.QueryRow(`SELECT file_path FROM movies WHERE id = ?`, id).Scan(&file); err != nil {
			return fmt.Errorf("read movie %d: %w", id, err)
		}
		newFile := file.String
		if p, ok := mapPath(file.String); ok && file.Valid {
			newFile = p
		}
		if _, err := tx.Exec(`UPDATE movies SET file_path = ?, root_path = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
			nullIfEmpty(newFile, file), root, id); err != nil {
			return fmt.Errorf("move movie %d: %w", id, err)
		}
		if err := mapSubtitles(tx, `SELECT media_id, language, sub_path FROM subtitle_files WHERE kind = 'movie' AND media_id = ?`, id, "movie", mapPath); err != nil {
			return err
		}
	case KindShow:
		rows, err := tx.Query(`SELECT id, file_path FROM episodes WHERE series_id = ? AND COALESCE(file_path, '') != ''`, id)
		if err != nil {
			return fmt.Errorf("read the episodes of show %d: %w", id, err)
		}
		type change struct {
			id   int64
			path string
		}
		var changes []change
		for rows.Next() {
			var epID int64
			var file string
			if err := rows.Scan(&epID, &file); err != nil {
				rows.Close()
				return err
			}
			if p, ok := mapPath(file); ok {
				changes = append(changes, change{epID, p})
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, c := range changes {
			if _, err := tx.Exec(`UPDATE episodes SET file_path = ? WHERE id = ?`, c.path, c.id); err != nil {
				return fmt.Errorf("move episode %d: %w", c.id, err)
			}
		}
		if err := mapSubtitles(tx, `SELECT sf.media_id, sf.language, sf.sub_path FROM subtitle_files sf JOIN episodes e ON e.id = sf.media_id WHERE sf.kind = 'episode' AND e.series_id = ?`, id, "episode", mapPath); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE series SET root_path = ? WHERE id = ?`, root, id); err != nil {
			return fmt.Errorf("move show %d: %w", id, err)
		}
	default:
		return fmt.Errorf("unknown kind %q", kind)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit move: %w", err)
	}
	return nil
}

// nullIfEmpty keeps a NULL file path NULL.
func nullIfEmpty(v string, was sql.NullString) any {
	if !was.Valid && v == "" {
		return nil
	}
	return v
}

func mapSubtitles(tx *sql.Tx, query string, id int64, kind string, mapPath func(string) (string, bool)) error {
	rows, err := tx.Query(query, id)
	if err != nil {
		return fmt.Errorf("read subtitle records: %w", err)
	}
	type change struct {
		media    int64
		language string
		path     string
	}
	var changes []change
	for rows.Next() {
		var media int64
		var lang, path string
		if err := rows.Scan(&media, &lang, &path); err != nil {
			rows.Close()
			return err
		}
		if p, ok := mapPath(path); ok {
			changes = append(changes, change{media, lang, p})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, c := range changes {
		if _, err := tx.Exec(`UPDATE subtitle_files SET sub_path = ? WHERE kind = ? AND media_id = ? AND language = ?`, c.path, kind, c.media, c.language); err != nil {
			return fmt.Errorf("move subtitle record: %w", err)
		}
	}
	return nil
}
