package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/notify"
	"github.com/rdborg/mediarium/internal/settings"
)

// The weekly summary: once a week, at the day and hour the person chose, a
// short message to the notification targets that listen for it says what came
// in, what failed and what is still missing. It is off until a day is chosen.

const (
	digestCheck   = 15 * time.Minute
	digestPeriod  = 7 * 24 * time.Hour
	defaultDigest = 9 // the hour, when none is set
)

// digestWhen is the day (0 is Sunday, -1 for no summary) and the hour.
func (s *Server) digestWhen() (day, hour int) {
	day, hour = -1, defaultDigest
	if v, _ := s.Settings.Get(settings.KeyDigestDay); strings.TrimSpace(v) != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 && n <= 6 {
			day = n
		}
	}
	if v, _ := s.Settings.Get(settings.KeyDigestHour); strings.TrimSpace(v) != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 && n <= 23 {
			hour = n
		}
	}
	return day, hour
}

// digestData collects the week.
func (s *Server) digestData(now time.Time) notify.Digest {
	since := now.Add(-digestPeriod)
	d := notify.Digest{Since: since}
	if done, err := s.QueueRepo.RecentCompleted(2000); err == nil {
		for _, c := range done {
			at, err := time.Parse(time.RFC3339Nano, c.CompletedAt)
			if err != nil || at.Before(since) {
				continue
			}
			switch {
			case c.MovieID > 0:
				d.Movies++
			case c.SeriesID > 0:
				d.Episodes++
			case c.AlbumID > 0:
				d.Albums++
			case c.BookID > 0:
				d.Books++
			}
		}
	}
	d.Failed, _ = s.QueueRepo.RecentFailed(since)
	d.Subtitles, _ = s.QueueRepo.CountEventsSince("subtitle", since)
	d.Waiting, _ = s.QueueRepo.CountWaiting()
	if files, err := s.MovieRepo.MissingMovieFiles(); err == nil {
		d.MissingMovies = len(files)
	}
	if files, err := s.MovieRepo.MissingEpisodeFiles(now.Format("2006-01-02")); err == nil {
		d.MissingEpisodes = len(files)
	}
	return d
}

// sendDigest builds the summary and hands it to the targets that listen for it.
func (s *Server) sendDigest(now time.Time) {
	ev := notify.ComposeDigest(s.digestData(now), s.publicLinks(), now)
	s.deliverNotification(ev)
}

// digestJob sends the summary once the chosen day and hour have come, once a week.
func (s *Server) digestJob(context.Context) {
	day, hour := s.digestWhen()
	if day < 0 {
		return
	}
	now := time.Now()
	if int(now.Weekday()) != day || now.Hour() < hour {
		return
	}
	if v, _ := s.Settings.Get(settings.KeyDigestSent); v != "" {
		if last, err := time.Parse(time.RFC3339, v); err == nil && now.Sub(last) < 6*24*time.Hour {
			return
		}
	}
	// Only this scheduled send is marked, and before it goes out: a mark
	// that can't be saved would otherwise send it again every 15 minutes.
	if err := s.Settings.Set(settings.KeyDigestSent, now.UTC().Format(time.RFC3339), false); err != nil {
		slog.Warn("weekly summary: couldn't note that it was sent, so it wasn't sent", "err", err)
		return
	}
	s.sendDigest(now)
}

// handleSendDigest sends the summary now, so a person can see what it looks
// like. It doesn't count as this week's: the scheduled one still goes out.
func (s *Server) handleSendDigest(w http.ResponseWriter, r *http.Request) {
	senders, err := s.NotifyRepo.SendersFor(notify.EventDigest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(senders) == 0 {
		writeError(w, http.StatusBadRequest, "No notification target is set to get the weekly summary. Add one, or tick Weekly summary on one you have.")
		return
	}
	s.sendDigest(time.Now())
	writeJSON(w, http.StatusOK, map[string]any{"sent": len(senders)})
}
