package delay

import (
	"database/sql"
	"fmt"
	"time"
)

// Item names the movie, episode or season pack a held release is for.
type Item struct {
	Kind     string // "movie" or "episode"
	MovieID  int64
	SeriesID int64
	Season   int
	Episode  int // 0 = a whole season
}

// Pending is a release that automation found and is holding back.
type Pending struct {
	Item
	Release   string
	Protocol  string
	FirstSeen time.Time
	Due       time.Time
	Searched  bool // the search that follows the wait has run
}

// Store remembers held releases in the database. It keeps no copy of the
// release itself (its download address can hold an indexer key): it only
// remembers that the title must be searched again when the wait is over, and
// when a release without a posting date was first seen.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore returns a store on db. now is the clock (nil: the real one).
func NewStore(db *sql.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

const tf = time.RFC3339

// keepAfterDue is how long a row stays after its wait ended. The row is the
// memory of when a release without a posting date was first seen.
const keepAfterDue = 7 * 24 * time.Hour

// Hold records that a release is being held back until due. It reports
// whether this is the first time (so the caller writes one line in the title's
// activity, not one per search). firstSeen is kept from the first call.
func (s *Store) Hold(it Item, release, protocol string, due time.Time) (first bool, err error) {
	now := s.now().UTC().Format(tf)
	dueText := due.UTC().Format(tf)
	err = s.db.QueryRow(`SELECT 1 FROM delayed_releases WHERE kind = ? AND movie_id = ? AND series_id = ? AND season = ? AND episode = ? AND release_title = ?`,
		it.Kind, it.MovieID, it.SeriesID, it.Season, it.Episode, release).Scan(new(int))
	switch {
	case err == sql.ErrNoRows:
		_, err = s.db.Exec(`INSERT INTO delayed_releases (kind, movie_id, series_id, season, episode, release_title, protocol, first_seen_at, due_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			it.Kind, it.MovieID, it.SeriesID, it.Season, it.Episode, release, protocol, now, dueText)
		first = true
	case err == nil:
		// A new wait (the setting changed) needs its follow-up search again.
		_, err = s.db.Exec(`UPDATE delayed_releases SET due_at = ?, protocol = ?, searched = CASE WHEN ? > due_at THEN 0 ELSE searched END
			WHERE kind = ? AND movie_id = ? AND series_id = ? AND season = ? AND episode = ? AND release_title = ?`,
			dueText, protocol, dueText, it.Kind, it.MovieID, it.SeriesID, it.Season, it.Episode, release)
	}
	if err != nil {
		return false, fmt.Errorf("hold release: %w", err)
	}
	return first, nil
}

// FirstSeen is when a release was first held back for the item; zero when it
// never was.
func (s *Store) FirstSeen(it Item, release string) time.Time {
	var seen string
	err := s.db.QueryRow(`SELECT first_seen_at FROM delayed_releases WHERE kind = ? AND movie_id = ? AND series_id = ? AND season = ? AND episode = ? AND release_title = ?`,
		it.Kind, it.MovieID, it.SeriesID, it.Season, it.Episode, release).Scan(&seen)
	if err != nil {
		return time.Time{}
	}
	t, _ := time.Parse(tf, seen)
	return t
}

// DueSearches returns the items whose wait is over and whose follow-up search
// has not run. It does not mark them: the caller calls MarkSearched for each
// one once its search ran, so a search that could not run (no room for another
// automatic download, every indexer paused) is tried again on the next pass.
func (s *Store) DueSearches() ([]Item, error) {
	now := s.now().UTC().Format(tf)
	rows, err := s.db.Query(`SELECT DISTINCT kind, movie_id, series_id, season, episode FROM delayed_releases WHERE searched = 0 AND due_at <= ?`, now)
	if err != nil {
		return nil, fmt.Errorf("read due releases: %w", err)
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.Kind, &it.MovieID, &it.SeriesID, &it.Season, &it.Episode); err != nil {
			return nil, fmt.Errorf("read due releases: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// MarkSearched records that the follow-up search for an item ran. Only the
// rows whose wait is over are marked: a release held again, for longer, while
// the search ran keeps its own follow-up.
func (s *Store) MarkSearched(it Item) error {
	now := s.now().UTC().Format(tf)
	_, err := s.db.Exec(`UPDATE delayed_releases SET searched = 1
		WHERE searched = 0 AND due_at <= ? AND kind = ? AND movie_id = ? AND series_id = ? AND season = ? AND episode = ?`,
		now, it.Kind, it.MovieID, it.SeriesID, it.Season, it.Episode)
	if err != nil {
		return fmt.Errorf("mark due releases: %w", err)
	}
	return nil
}

// Waiting lists the releases still being held at this moment, soonest first.
func (s *Store) Waiting() ([]Pending, error) {
	now := s.now().UTC().Format(tf)
	rows, err := s.db.Query(`SELECT kind, movie_id, series_id, season, episode, release_title, protocol, first_seen_at, due_at, searched
		FROM delayed_releases WHERE due_at > ? ORDER BY due_at`, now)
	if err != nil {
		return nil, fmt.Errorf("read held releases: %w", err)
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		var (
			p         Pending
			seen, due string
		)
		if err := rows.Scan(&p.Kind, &p.MovieID, &p.SeriesID, &p.Season, &p.Episode, &p.Release, &p.Protocol, &seen, &due, &p.Searched); err != nil {
			return nil, fmt.Errorf("read held releases: %w", err)
		}
		p.FirstSeen, _ = time.Parse(tf, seen)
		p.Due, _ = time.Parse(tf, due)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Clear forgets everything held for a movie (movieID) or a show (seriesID),
// for example once it was grabbed, or the title is removed.
func (s *Store) Clear(movieID, seriesID int64) error {
	var err error
	switch {
	case movieID != 0:
		_, err = s.db.Exec(`DELETE FROM delayed_releases WHERE movie_id = ?`, movieID)
	case seriesID != 0:
		_, err = s.db.Exec(`DELETE FROM delayed_releases WHERE series_id = ?`, seriesID)
	}
	if err != nil {
		return fmt.Errorf("clear held releases: %w", err)
	}
	return nil
}

// ClearItem forgets what is held for one episode or season pack. A grabbed
// release also ends the wait for the rest of that season's pack and episodes
// it covers, so the caller passes each one.
func (s *Store) ClearItem(it Item) error {
	_, err := s.db.Exec(`DELETE FROM delayed_releases WHERE kind = ? AND movie_id = ? AND series_id = ? AND season = ? AND episode = ?`,
		it.Kind, it.MovieID, it.SeriesID, it.Season, it.Episode)
	if err != nil {
		return fmt.Errorf("clear held releases: %w", err)
	}
	return nil
}

// Prune forgets rows whose wait ended long ago.
func (s *Store) Prune() error {
	cut := s.now().Add(-keepAfterDue).UTC().Format(tf)
	if _, err := s.db.Exec(`DELETE FROM delayed_releases WHERE due_at < ?`, cut); err != nil {
		return fmt.Errorf("prune held releases: %w", err)
	}
	return nil
}
