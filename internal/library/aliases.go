package library

import (
	"encoding/json"
)

// A show's other names (the romaji title of an anime, the names of its
// seasons) are kept with its numbering in series_numbering, which the
// animemap package fills. The library only reads them, so release names that
// use one of them are taken for the show. A second copy of a show uses its
// main show's names.

func decodeAliases(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// loadAliases fills in s.Aliases. A problem reading them leaves the show
// without: they only add matches.
func (r *Repo) loadAliases(s *Series) {
	var raw string
	err := r.db.QueryRow(`SELECT n.aliases FROM series s JOIN series_numbering n ON n.series_id = COALESCE(s.copy_of, s.id) WHERE s.id = ?`, s.ID).Scan(&raw)
	if err != nil {
		return
	}
	s.Aliases = decodeAliases(raw)
}

// loadAllAliases fills in the aliases of every show in list with one query.
func (r *Repo) loadAllAliases(list []Series) {
	if len(list) == 0 {
		return
	}
	rows, err := r.db.Query(`SELECT s.id, n.aliases FROM series s JOIN series_numbering n ON n.series_id = COALESCE(s.copy_of, s.id) WHERE n.aliases != ''`)
	if err != nil {
		return
	}
	defer rows.Close()
	byID := map[int64][]string{}
	for rows.Next() {
		var id int64
		var raw string
		if rows.Scan(&id, &raw) == nil {
			byID[id] = decodeAliases(raw)
		}
	}
	for i := range list {
		list[i].Aliases = byID[list[i].ID]
	}
}
