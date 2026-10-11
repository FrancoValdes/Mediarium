package animemap

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// States of a lookup, as stored.
const (
	StateMapped  = "mapped"  // TheXEM has numbering for the show
	StateFound   = "found"   // the anime lists have entries for the show
	StateNone    = "none"    // the service doesn't know the show
	StateOffline = "offline" // the service couldn't be reached; what was known is kept
)

// Record is what is known about one show's numbering.
type Record struct {
	SeriesID   int64
	AniDB      int
	AniList    int
	MAL        int
	Numbering  Numbering
	Aliases    []string
	XEMState   string
	ListsState string
	Note       string
	CheckedAt  time.Time
	TitlesAt   time.Time
}

// Repo keeps the records in the database.
type Repo struct {
	db *sql.DB
}

func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// Get returns the record of a show. ok is false when it was never looked up.
func (r *Repo) Get(seriesID int64) (Record, bool, error) {
	var (
		rec                 Record
		numbering, aliases  string
		checkedAt, titlesAt string
	)
	err := r.db.QueryRow(`SELECT series_id, anidb_id, anilist_id, mal_id, numbering, aliases, xem_state, lists_state, note, checked_at, titles_at
		FROM series_numbering WHERE series_id = ?`, seriesID).
		Scan(&rec.SeriesID, &rec.AniDB, &rec.AniList, &rec.MAL, &numbering, &aliases, &rec.XEMState, &rec.ListsState, &rec.Note, &checkedAt, &titlesAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read numbering of show %d: %w", seriesID, err)
	}
	if numbering != "" {
		if err := json.Unmarshal([]byte(numbering), &rec.Numbering); err != nil {
			// A damaged row is the same as none: the next refresh writes it again.
			rec.Numbering = Numbering{}
		}
	}
	if aliases != "" {
		_ = json.Unmarshal([]byte(aliases), &rec.Aliases)
	}
	rec.CheckedAt, rec.TitlesAt = parseTime(checkedAt), parseTime(titlesAt)
	return rec, true, nil
}

// Save writes the record, replacing the show's earlier one.
func (r *Repo) Save(rec Record) error {
	numbering := ""
	if !rec.Numbering.Empty() {
		b, err := json.Marshal(rec.Numbering)
		if err != nil {
			return fmt.Errorf("save numbering of show %d: %w", rec.SeriesID, err)
		}
		numbering = string(b)
	}
	aliases := ""
	if len(rec.Aliases) > 0 {
		b, err := json.Marshal(rec.Aliases)
		if err != nil {
			return fmt.Errorf("save names of show %d: %w", rec.SeriesID, err)
		}
		aliases = string(b)
	}
	_, err := r.db.Exec(`INSERT INTO series_numbering (series_id, anidb_id, anilist_id, mal_id, numbering, aliases, xem_state, lists_state, note, checked_at, titles_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (series_id) DO UPDATE SET anidb_id = excluded.anidb_id, anilist_id = excluded.anilist_id, mal_id = excluded.mal_id,
			numbering = excluded.numbering, aliases = excluded.aliases, xem_state = excluded.xem_state, lists_state = excluded.lists_state,
			note = excluded.note, checked_at = excluded.checked_at, titles_at = excluded.titles_at`,
		rec.SeriesID, rec.AniDB, rec.AniList, rec.MAL, numbering, aliases, rec.XEMState, rec.ListsState, rec.Note, formatTime(rec.CheckedAt), formatTime(rec.TitlesAt))
	if err != nil {
		return fmt.Errorf("save numbering of show %d: %w", rec.SeriesID, err)
	}
	return nil
}
