package queue

import (
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// The download line: items with status "queued" are waiting for a free place.
// A person's downloads go first (PriorityManual before PriorityAutomatic);
// within a priority the lower LineSeq goes first, which is the order they
// were added, except for a resumed item, which goes back to the front of its
// priority.

// newestFirst is the "start newer releases first" rule. Among the downloads
// the automatic searches added, the movie that came out (or the episode that
// aired) most recently starts first. What a person asked for by hand, and
// what was moved to the front, still goes first.
var newestFirst atomic.Bool

// SetNewestFirst switches the rule on or off.
func SetNewestFirst(on bool) { newestFirst.Store(on) }

const releasedExpr = `COALESCE((SELECT release_date FROM movies WHERE id = q.movie_id), (SELECT air_date FROM episodes WHERE series_id = q.series_id AND season = q.season AND episode = q.episode), '')`

// lineDateExpr is the date an item is ranked by under the rule: its release
// date, or for what has none (a season pack, an album, a book) the day it was
// added, as if it came out then. Undated items then take their fair turn
// instead of waiting behind every dated one that keeps arriving.
const lineDateExpr = `COALESCE(NULLIF(` + releasedExpr + `, ''), substr(q.added_at, 1, 10))`

// A resumed item (line_seq <= 0, see Requeue) goes first in its priority
// whatever the rule says: it was started once and has files on disk.
const newestFirstOrder = `ORDER BY q.priority DESC, CASE WHEN q.line_seq <= 0 THEN 0 ELSE 1 END ASC, CASE WHEN q.priority = 0 THEN ` + lineDateExpr + ` ELSE '' END DESC, q.line_seq ASC, q.id ASC`

// NextWaiting returns the item that should start next, or false when nothing
// is waiting. With manualOnly it only looks at what a person asked for.
func (r *Repo) NextWaiting(manualOnly bool) (Item, bool, error) {
	query := `SELECT q.id FROM download_queue q WHERE q.status = 'queued' `
	if manualOnly {
		query += `AND q.priority >= 1 `
	}
	order := newestFirstOrder
	if !newestFirst.Load() {
		order = `ORDER BY q.priority DESC, q.line_seq ASC, q.id ASC`
	}
	var id int64
	err := r.db.QueryRow(query + order + ` LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, fmt.Errorf("find the next waiting download: %w", err)
	}
	it, err := r.Get(id)
	if err != nil {
		return Item{}, false, err
	}
	return it, true, nil
}

// Claim takes a waiting item out of the line and marks it as downloading, in
// one step: it reports false when the item is no longer waiting (a person
// paused, stopped or removed it a moment ago). The "download started" note is
// left to the pipeline, which sets the same status when it begins.
func (r *Repo) Claim(id int64) (bool, error) {
	res, err := r.db.Exec(`UPDATE download_queue SET status = ?, error = NULL WHERE id = ? AND status = ?`, string(StatusDownloading), id, string(StatusQueued))
	if err != nil {
		return false, fmt.Errorf("start queue item %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Requeue puts a paused item back in the line, at the front of its priority
// (it was already started once, so it goes before the ones that never were).
// Its place is 0 or below, which marks it as resumed: new items always get a
// place above 0 (Enqueue), and "newest first" never puts one ahead of it.
// It reports false when the item is not paused.
func (r *Repo) Requeue(id int64) (bool, error) {
	res, err := r.db.Exec(
		`UPDATE download_queue SET status = ?, interrupted = 0, error = NULL,
		   line_seq = (SELECT MIN(COALESCE(MIN(line_seq), 1), 1) - 1 FROM download_queue WHERE status = ? AND priority = (SELECT priority FROM download_queue WHERE id = ?))
		 WHERE id = ? AND status = ?`,
		string(StatusQueued), string(StatusQueued), id, id, string(StatusPaused))
	if err != nil {
		return false, fmt.Errorf("put queue item %d back in line: %w", id, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CountWaiting counts the downloads waiting in line.
func (r *Repo) CountWaiting() (int, error) {
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM download_queue WHERE status = 'queued'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count waiting downloads: %w", err)
	}
	return n, nil
}

// CountAutomaticAddedSince counts the downloads the automatic searches added
// to the line at or after since, however they finished. Downloads a person
// asked for are not counted.
func (r *Repo) CountAutomaticAddedSince(since time.Time) (int, error) {
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM download_queue WHERE priority = 0 AND added_at >= ?`, since.UTC().Format("2006-01-02T15:04:05.000Z")).Scan(&n); err != nil {
		return 0, fmt.Errorf("count recent automatic downloads: %w", err)
	}
	return n, nil
}

// LineOrder sorts waiting items into the order they will start in, the same
// way NextWaiting picks them. The items need AddedAt (List fills it in).
func LineOrder(a, b Item) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if ra, rb := a.LineSeq <= 0, b.LineSeq <= 0; ra != rb {
		return ra // a resumed item goes first
	}
	if da, db := lineDate(a), lineDate(b); newestFirst.Load() && a.Priority == 0 && da != db {
		return da > db
	}
	if a.LineSeq != b.LineSeq {
		return a.LineSeq < b.LineSeq
	}
	return a.ID < b.ID
}

// lineDate is lineDateExpr for an item read by List.
func lineDate(it Item) string {
	if it.Released != "" {
		return it.Released
	}
	if len(it.AddedAt) >= 10 {
		return it.AddedAt[:10]
	}
	return it.AddedAt
}

// PauseWaiting parks every download waiting in line as paused and marked
// interrupted, so a person has to press Resume. Safe mode uses it at start-up.
// It returns how many it paused.
func (r *Repo) PauseWaiting() (int64, error) {
	res, err := r.db.Exec(`UPDATE download_queue SET status = ?, interrupted = 1 WHERE status = ?`, string(StatusPaused), string(StatusQueued))
	if err != nil {
		return 0, fmt.Errorf("pause waiting downloads: %w", err)
	}
	return res.RowsAffected()
}

// CountAutomaticOpen counts the downloads the automatic searches added that
// are still waiting or running.
func (r *Repo) CountAutomaticOpen() (int, error) {
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM download_queue WHERE priority = 0 AND status IN ('queued', 'downloading', 'importing')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count automatic downloads in line: %w", err)
	}
	return n, nil
}

// MoveToFront puts a waiting item first in the line, ahead of everything else
// that waits, including what other people asked for by hand: it becomes a
// person's download, and goes before the rest of its priority. It reports
// false when the item is not waiting (running, paused or finished).
func (r *Repo) MoveToFront(id int64) (bool, error) {
	res, err := r.db.Exec(
		`UPDATE download_queue SET priority = ?,
		   line_seq = (SELECT COALESCE(MIN(line_seq), 1) - 1 FROM download_queue WHERE status = ? AND priority >= ?)
		 WHERE id = ? AND status = ?`,
		int(PriorityManual), string(StatusQueued), int(PriorityManual), id, string(StatusQueued))
	if err != nil {
		return false, fmt.Errorf("move queue item %d to the front: %w", id, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
