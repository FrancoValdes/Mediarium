package music

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LyricsMissRetry is how long a "nothing found online" answer is believed
// before the track is asked about again.
const LyricsMissRetry = 30 * 24 * time.Hour

// CachedLyrics is what was found online for a track the last time it was
// asked. Found is false when the service had nothing.
type CachedLyrics struct {
	Lyrics
	Found     bool
	FetchedAt time.Time
}

// Fresh reports whether the answer can still be used without asking again.
// Lyrics that were found are kept for good; a miss for LyricsMissRetry.
func (c CachedLyrics) Fresh(now time.Time) bool {
	return c.Found || now.Sub(c.FetchedAt) < LyricsMissRetry
}

// GetCachedLyrics returns the stored online answer for a track, if any.
func (r *Repo) GetCachedLyrics(trackID int64) (CachedLyrics, bool, error) {
	var c CachedLyrics
	var found, instr int
	var at string
	err := r.db.QueryRow(`SELECT source, found, instrumental, synced, plain, fetched_at FROM music_lyrics WHERE track_id = ?`, trackID).
		Scan(&c.Source, &found, &instr, &c.Synced, &c.Plain, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return CachedLyrics{}, false, nil
	}
	if err != nil {
		return CachedLyrics{}, false, fmt.Errorf("read cached lyrics of track %d: %w", trackID, err)
	}
	c.Found, c.Instrumental = found != 0, instr != 0
	c.FetchedAt, _ = time.Parse(time.RFC3339Nano, at)
	return c, true, nil
}

// PutCachedLyrics stores the online answer for a track. found false records
// that nothing was there.
func (r *Repo) PutCachedLyrics(trackID int64, l Lyrics, found bool) error {
	_, err := r.db.Exec(`INSERT INTO music_lyrics (track_id, source, found, instrumental, synced, plain, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		ON CONFLICT(track_id) DO UPDATE SET source = excluded.source, found = excluded.found, instrumental = excluded.instrumental,
			synced = excluded.synced, plain = excluded.plain, fetched_at = excluded.fetched_at`,
		trackID, l.Source, boolInt(found), boolInt(l.Instrumental), l.Synced, l.Plain)
	if err != nil {
		return fmt.Errorf("store lyrics of track %d: %w", trackID, err)
	}
	return nil
}
