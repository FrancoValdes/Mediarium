package api

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/settings"
)

// What happens to monitoring once something is in. Both switches are off by
// default: a downloaded movie is only searched again for a better version
// when its profile asks for that, and a finished show only picks up episodes
// that are still missing, so leaving things monitored costs nothing. Some
// people would rather see them switched off, so a file deleted by hand is
// never fetched again.

func (s *Server) unmonitorDownloaded() bool {
	return s.settingOn(settings.KeyUnmonitorDownloaded, false)
}
func (s *Server) unmonitorFinished() bool { return s.settingOn(settings.KeyUnmonitorFinished, false) }

// afterMovieImported records the release a movie's new file came from and,
// when that switch is on, stops monitoring the movie.
func (s *Server) afterMovieImported(movieID int64, release string) {
	s.forgetHeldMovie(movieID) // downloaded: nothing is waiting for it any more
	if err := s.MovieRepo.SetReleaseTitle(movieID, release); err != nil {
		log.Printf("library: %v", err)
	}
	if !s.unmonitorDownloaded() {
		return
	}
	m, err := s.MovieRepo.Get(movieID)
	if err != nil || !m.Monitored {
		return
	}
	if err := s.MovieRepo.SetMonitored(movieID, false); err != nil {
		log.Printf("library: stop monitoring %q: %v", m.Title, err)
		return
	}
	_ = s.QueueRepo.LogActivity(movieID, "monitoring", m.Title+" is downloaded, so it is no longer monitored")
}

// afterEpisodeImported records the release an episode's new file came from.
func (s *Server) afterEpisodeImported(episodeID int64, release string) {
	if err := s.MovieRepo.SetEpisodeReleaseTitle(episodeID, release); err != nil {
		log.Printf("library: %v", err)
	}
}

// showEnded reports whether TMDB's status means no more episodes are coming.
func showEnded(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ended", "canceled", "cancelled":
		return true
	}
	return false
}

// stopMonitoringIfFinished stops monitoring a show that has ended and has
// every monitored episode (specials aside) downloaded, when that switch is on.
func (s *Server) stopMonitoringIfFinished(seriesID int64, status string) {
	if !s.unmonitorFinished() || !showEnded(status) {
		return
	}
	sr, err := s.MovieRepo.GetSeries(seriesID)
	if err != nil || !sr.Monitored {
		return
	}
	eps, err := s.MovieRepo.ListEpisodes(seriesID)
	if err != nil {
		return
	}
	any := false
	for _, ep := range eps {
		if ep.Season == 0 || !ep.Monitored {
			continue
		}
		any = true
		if ep.Status != library.StatusDownloaded {
			return
		}
	}
	if !any {
		return
	}
	if err := s.MovieRepo.SetSeriesMonitored(seriesID, false); err != nil {
		log.Printf("library: stop monitoring %q: %v", sr.Title, err)
		return
	}
	_ = s.QueueRepo.LogSeriesActivity(seriesID, "monitoring", sr.Title+" has ended and every episode is in, so it is no longer monitored")
}

// checkFinishedShowSoon re-reads a show from TMDB after an import, so a show
// that has just been completed stops being monitored without waiting for
// the daily refresh.
func (s *Server) checkFinishedShowSoon(seriesID int64) {
	if !s.unmonitorFinished() || !s.TMDB().HasAPIKey() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		sr, err := s.MovieRepo.GetSeries(seriesID)
		if err != nil || !sr.Monitored {
			return
		}
		if err := s.refreshSeries(ctx, sr); err != nil {
			log.Printf("library: refresh %q after an import: %v", sr.Title, err)
		}
	}()
}
