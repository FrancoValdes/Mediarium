package ereader

import (
	"database/sql"
	"errors"
	"fmt"
)

// LibEntry is a book that was sent to an account's Kobo.
type LibEntry struct {
	BookID   int64
	Title    string
	FileSig  string // size and date of the file that was sent
	Archived bool   // taken off the Kobo by hand
}

// Library returns the books sent to the account's Kobo, by book id.
func (r *Repo) Library(userID int64) (map[int64]LibEntry, error) {
	rows, err := r.db.Query(`SELECT book_id, title, file_sig, archived FROM kobo_library WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("read Kobo library: %w", err)
	}
	defer rows.Close()
	out := map[int64]LibEntry{}
	for rows.Next() {
		var e LibEntry
		if err := rows.Scan(&e.BookID, &e.Title, &e.FileSig, &e.Archived); err != nil {
			return nil, fmt.Errorf("scan Kobo library: %w", err)
		}
		out[e.BookID] = e
	}
	return out, rows.Err()
}

// MarkSynced records that a book was sent to the account's Kobo.
func (r *Repo) MarkSynced(userID, bookID int64, title, sig string) error {
	_, err := r.db.Exec(`INSERT INTO kobo_library (user_id, book_id, title, file_sig, synced_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id, book_id) DO UPDATE SET title = excluded.title, file_sig = excluded.file_sig, synced_at = excluded.synced_at`,
		userID, bookID, title, sig, now())
	if err != nil {
		return fmt.Errorf("record Kobo book: %w", err)
	}
	return nil
}

// Archive notes that the book was taken off the Kobo by hand, so it is not
// sent again.
func (r *Repo) Archive(userID, bookID int64) error {
	_, err := r.db.Exec(`INSERT INTO kobo_library (user_id, book_id, archived) VALUES (?, ?, 1)
		ON CONFLICT (user_id, book_id) DO UPDATE SET archived = 1`, userID, bookID)
	if err != nil {
		return fmt.Errorf("archive Kobo book: %w", err)
	}
	return nil
}

// Forget drops a book from the list of what was sent.
func (r *Repo) Forget(userID, bookID int64) error {
	_, err := r.db.Exec(`DELETE FROM kobo_library WHERE user_id = ? AND book_id = ?`, userID, bookID)
	if err != nil {
		return fmt.Errorf("forget Kobo book: %w", err)
	}
	return nil
}

// ResetKobo forgets what was sent to the account's Kobo and what it reported,
// so the next sync sends everything again.
func (r *Repo) ResetKobo(userID int64) error {
	if _, err := r.db.Exec(`DELETE FROM kobo_library WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("reset Kobo library: %w", err)
	}
	if _, err := r.db.Exec(`UPDATE kobo_state SET device_ts = '' WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("reset Kobo state: %w", err)
	}
	return nil
}

// State is what a Kobo reported about one book.
type State struct {
	BookID           int64
	Status           string // ReadyToRead, Reading or Finished
	LocationValue    string
	LocationType     string
	LocationSource   string
	ContentPercent   *float64
	SpentMinutes     int
	RemainingMinutes int
	TimesStarted     int
	LastStarted      string
	DeviceTS         string // the version of the reading progress the Kobo already has
	ModifiedAt       string
}

const stateColumns = `book_id, status, location_value, location_type, location_source, content_percent,
	spent_minutes, remaining_minutes, times_started, last_started, device_ts, modified_at`

func scanState(sc interface{ Scan(...any) error }) (State, error) {
	var s State
	var cp sql.NullFloat64
	err := sc.Scan(&s.BookID, &s.Status, &s.LocationValue, &s.LocationType, &s.LocationSource, &cp,
		&s.SpentMinutes, &s.RemainingMinutes, &s.TimesStarted, &s.LastStarted, &s.DeviceTS, &s.ModifiedAt)
	if cp.Valid {
		v := cp.Float64
		s.ContentPercent = &v
	}
	return s, err
}

// GetState returns what the account's Kobo reported about a book; ok is false
// when it never did.
func (r *Repo) GetState(userID, bookID int64) (State, bool, error) {
	s, err := scanState(r.db.QueryRow(`SELECT `+stateColumns+` FROM kobo_state WHERE user_id = ? AND book_id = ?`, userID, bookID))
	if errors.Is(err, sql.ErrNoRows) {
		return State{BookID: bookID}, false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("read Kobo state: %w", err)
	}
	return s, true, nil
}

// States returns everything the account's Kobo reported, by book id.
func (r *Repo) States(userID int64) (map[int64]State, error) {
	rows, err := r.db.Query(`SELECT `+stateColumns+` FROM kobo_state WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("read Kobo states: %w", err)
	}
	defer rows.Close()
	out := map[int64]State{}
	for rows.Next() {
		s, err := scanState(rows)
		if err != nil {
			return nil, fmt.Errorf("scan Kobo state: %w", err)
		}
		out[s.BookID] = s
	}
	return out, rows.Err()
}

// SaveState stores what the Kobo reported about a book.
func (r *Repo) SaveState(userID int64, s State) error {
	var cp any
	if s.ContentPercent != nil {
		cp = *s.ContentPercent
	}
	_, err := r.db.Exec(`INSERT INTO kobo_state (user_id, `+stateColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, book_id) DO UPDATE SET status = excluded.status, location_value = excluded.location_value,
			location_type = excluded.location_type, location_source = excluded.location_source, content_percent = excluded.content_percent,
			spent_minutes = excluded.spent_minutes, remaining_minutes = excluded.remaining_minutes, times_started = excluded.times_started,
			last_started = excluded.last_started, device_ts = excluded.device_ts, modified_at = excluded.modified_at`,
		userID, s.BookID, s.Status, s.LocationValue, s.LocationType, s.LocationSource, cp,
		s.SpentMinutes, s.RemainingMinutes, s.TimesStarted, s.LastStarted, s.DeviceTS, s.ModifiedAt)
	if err != nil {
		return fmt.Errorf("save Kobo state: %w", err)
	}
	return nil
}

// SetDeviceTS notes the reading progress version the Kobo now has (an opaque signature).
func (r *Repo) SetDeviceTS(userID, bookID int64, ts string) error {
	if _, err := r.db.Exec(`INSERT INTO kobo_state (user_id, book_id, device_ts, modified_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, book_id) DO UPDATE SET device_ts = excluded.device_ts`, userID, bookID, ts, now()); err != nil {
		return fmt.Errorf("save Kobo state: %w", err)
	}
	return nil
}
