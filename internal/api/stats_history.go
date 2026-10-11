package api

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Statistics over time: one small snapshot a day (how much is in the library
// and how the day's downloads went), kept for good, so the Statistics page
// can show how the library grew. Days before the first snapshot are filled
// in as far as the data allows: titles from when they were added, downloads
// from the download history that's still kept. Library size can't be known
// for those days and is left out.

const statsSnapshotEvery = 3 * time.Hour

type statsDay struct {
	Day          string `json:"day"` // "2026-10-06"
	Movies       int    `json:"movies"`
	MoviesHave   int    `json:"moviesHave"`
	Shows        int    `json:"shows"`
	EpisodesHave int    `json:"episodesHave"`
	AlbumsHave   int    `json:"albumsHave"`
	// LibraryBytes is -1 for a day before snapshots began.
	LibraryBytes int64 `json:"libraryBytes"`
	Completed    int   `json:"completed"`
	Failed       int   `json:"failed"`
	// DownloadBytes is what the completed downloads of the day weighed.
	DownloadBytes int64 `json:"downloadBytes"`
	// Estimated days are filled in from older data, not a snapshot.
	Estimated bool `json:"estimated,omitempty"`
}

// takeStatsSnapshot records today (replacing an earlier snapshot of today).
func (s *Server) takeStatsSnapshot(now time.Time) error {
	day := now.UTC().Format("2006-01-02")
	var d statsDay
	movies, err := s.MovieRepo.List()
	if err != nil {
		return err
	}
	d.Movies = len(movies)
	for _, m := range movies {
		if m.FilePath != "" {
			d.MoviesHave++
			d.LibraryBytes += fileBytes(m.FilePath)
		}
	}
	shows, err := s.MovieRepo.ListSeries()
	if err != nil {
		return err
	}
	d.Shows = len(shows)
	seen := map[string]bool{}
	for _, sr := range shows {
		eps, err := s.MovieRepo.ListEpisodes(sr.ID)
		if err != nil {
			return err
		}
		for _, ep := range eps {
			if ep.FilePath == "" {
				continue
			}
			d.EpisodesHave++
			if !seen[ep.FilePath] {
				seen[ep.FilePath] = true
				d.LibraryBytes += fileBytes(ep.FilePath)
			}
		}
	}
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(CASE WHEN status = 'downloaded' THEN 1 ELSE 0 END), 0) FROM albums`).Scan(&d.AlbumsHave)
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'completed' THEN size_bytes ELSE 0 END), 0)
		FROM download_queue WHERE substr(COALESCE(completed_at, added_at), 1, 10) = ?`, day).Scan(&d.Completed, &d.Failed, &d.DownloadBytes)
	_, err = s.db.Exec(`INSERT INTO stats_daily (day, movies, movies_have, shows, episodes_have, albums_have, library_bytes, completed, failed, download_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(day) DO UPDATE SET movies = excluded.movies, movies_have = excluded.movies_have, shows = excluded.shows,
			episodes_have = excluded.episodes_have, albums_have = excluded.albums_have, library_bytes = excluded.library_bytes,
			completed = MAX(stats_daily.completed, excluded.completed), failed = MAX(stats_daily.failed, excluded.failed),
			download_bytes = MAX(stats_daily.download_bytes, excluded.download_bytes)`,
		day, d.Movies, d.MoviesHave, d.Shows, d.EpisodesHave, d.AlbumsHave, d.LibraryBytes, d.Completed, d.Failed, d.DownloadBytes)
	return err
}

// statsSnapshotJob is the automation loop's call: a snapshot every few hours,
// so today's row stays current and a day is never missed while it runs.
func (s *Server) statsSnapshotJob(context.Context) {
	if err := s.takeStatsSnapshot(time.Now()); err != nil {
		log.Printf("statistics: daily snapshot: %v", err)
	}
}

// statsHistory is every day from the first known one to today: snapshots
// where there are some, estimates before them.
func (s *Server) statsHistory() ([]statsDay, error) {
	byDay := map[string]*statsDay{}
	rows, err := s.db.Query(`SELECT day, movies, movies_have, shows, episodes_have, albums_have, library_bytes, completed, failed, download_bytes FROM stats_daily`)
	if err != nil {
		return nil, err
	}
	first := ""
	for rows.Next() {
		var d statsDay
		if err := rows.Scan(&d.Day, &d.Movies, &d.MoviesHave, &d.Shows, &d.EpisodesHave, &d.AlbumsHave, &d.LibraryBytes, &d.Completed, &d.Failed, &d.DownloadBytes); err != nil {
			rows.Close()
			return nil, err
		}
		dd := d
		byDay[d.Day] = &dd
		if first == "" || d.Day < first {
			first = d.Day
		}
	}
	rows.Close()

	// Before the first snapshot: titles by when they were added, and the
	// downloads still in the history.
	added := map[string][2]int{} // day -> movies, shows added that day
	for _, q := range []struct {
		sql  string
		slot int
	}{
		{`SELECT substr(added_at, 1, 10), COUNT(*) FROM movies WHERE copy_of IS NULL GROUP BY 1`, 0},
		{`SELECT substr(added_at, 1, 10), COUNT(*) FROM series WHERE copy_of IS NULL GROUP BY 1`, 1},
	} {
		r, err := s.db.Query(q.sql)
		if err != nil {
			return nil, err
		}
		for r.Next() {
			var day string
			var n int
			if r.Scan(&day, &n) == nil && day != "" {
				v := added[day]
				v[q.slot] += n
				added[day] = v
			}
		}
		r.Close()
	}
	downloads := map[string]statsDay{}
	r, err := s.db.Query(`SELECT substr(COALESCE(completed_at, added_at), 1, 10),
			SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END),
			COALESCE(SUM(CASE WHEN status = 'completed' THEN size_bytes ELSE 0 END), 0)
		FROM download_queue GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	for r.Next() {
		var d statsDay
		if r.Scan(&d.Day, &d.Completed, &d.Failed, &d.DownloadBytes) == nil && d.Day != "" {
			downloads[d.Day] = d
		}
	}
	r.Close()

	start := first
	for day := range added {
		if start == "" || day < start {
			start = day
		}
	}
	for day := range downloads {
		if start == "" || day < start {
			start = day
		}
	}
	today := time.Now().UTC().Format("2006-01-02")
	if start == "" {
		return []statsDay{}, nil
	}
	from, err := time.Parse("2006-01-02", start)
	if err != nil {
		return nil, err
	}
	var out []statsDay
	var movies, shows int
	var last *statsDay
	for t := from; ; t = t.AddDate(0, 0, 1) {
		day := t.Format("2006-01-02")
		if day > today {
			break
		}
		a := added[day]
		movies += a[0]
		shows += a[1]
		if snap, ok := byDay[day]; ok {
			out = append(out, *snap)
			last = snap
			continue
		}
		var d statsDay
		if last != nil {
			// A day the server was off: the library as it last was.
			d = *last
			d.Completed, d.Failed, d.DownloadBytes = 0, 0, 0
		} else {
			d = statsDay{Movies: movies, Shows: shows, LibraryBytes: -1}
		}
		d.Day, d.Estimated = day, true
		if dl, ok := downloads[day]; ok {
			d.Completed, d.Failed, d.DownloadBytes = dl.Completed, dl.Failed, dl.DownloadBytes
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

// handleStatsHistory answers GET /api/stats/history?days=N (0 or missing:
// everything).
func (s *Server) handleStatsHistory(w http.ResponseWriter, r *http.Request) {
	days, err := s.statsHistory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := strconv.Atoi(r.URL.Query().Get("days")); n > 0 && len(days) > n {
		days = days[len(days)-n:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}
