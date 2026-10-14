package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/rdborg/mediarium/internal/metadata"
)

// The Backdrops view of the calendar shows each release as a wide still with
// the title's logo on it. TMDB hands those out in a separate call for every
// title, so Mediarium asks for them slowly in the background (a few every few
// minutes, only for what is on the calendar) and keeps the answer. A title
// shows up with its picture once it has been fetched; until then the view
// falls back to the poster.

const (
	artworkEvery = 5 * time.Minute
	artworkBatch = 12                     // titles asked about in one run
	artworkPause = 400 * time.Millisecond // between two calls, to go easy on TMDB
	artworkAgain = 90 * 24 * time.Hour    // a title that had no picture is asked about again after this
)

func artKind(entryKind string) string {
	if entryKind == "episode" {
		return "tv"
	}
	return "movie"
}

// attachArt puts the backdrop and logo that are already known on the entries.
func (s *Server) attachArt(entries []calendarEntryPayload) {
	ids := map[string][]int{}
	for _, e := range entries {
		if (e.Kind == "movie" || e.Kind == "episode") && e.TMDBID > 0 {
			k := artKind(e.Kind)
			ids[k] = append(ids[k], e.TMDBID)
		}
	}
	art := map[string]map[int]metadataArt{}
	for kind, list := range ids {
		found, err := s.MovieRepo.TitleArt(kind, list)
		if err != nil {
			slog.Warn("calendar: couldn't read the saved artwork", "err", err)
			return
		}
		m := map[int]metadataArt{}
		for id, a := range found {
			m[id] = metadataArt{backdrop: a.Backdrop, logo: a.Logo}
		}
		art[kind] = m
	}
	for i := range entries {
		e := &entries[i]
		if e.Kind != "movie" && e.Kind != "episode" {
			continue
		}
		if a, ok := art[artKind(e.Kind)][e.TMDBID]; ok {
			e.BackdropURL, e.LogoURL = metadata.WideURL(a.backdrop), metadata.LogoURL(a.logo)
		}
	}
}

type metadataArt struct{ backdrop, logo string }

// artworkJob fetches the artwork of a few calendar titles that have none yet.
func (s *Server) artworkJob(ctx context.Context) {
	if !s.TMDB().HasAPIKey() {
		return
	}
	entries, err := s.calendarEntries()
	if err != nil {
		return
	}
	type title struct {
		kind string
		id   int
	}
	seen := map[title]bool{}
	byKind := map[string][]int{}
	var order []title
	for _, e := range entries {
		if (e.Kind != "movie" && e.Kind != "episode") || e.TMDBID <= 0 {
			continue
		}
		t := title{artKind(e.Kind), e.TMDBID}
		if !seen[t] {
			seen[t] = true
			order = append(order, t)
			byKind[t.kind] = append(byKind[t.kind], t.id)
		}
	}
	known := map[string]map[int]string{}
	for kind, list := range byKind {
		found, err := s.MovieRepo.TitleArt(kind, list)
		if err != nil {
			return
		}
		m := map[int]string{}
		for id, a := range found {
			m[id] = a.CheckedAt
		}
		known[kind] = m
	}
	cutoff := time.Now().Add(-artworkAgain)
	fetched := 0
	for _, t := range order {
		if fetched >= artworkBatch || ctx.Err() != nil {
			return
		}
		if at, ok := known[t.kind][t.id]; ok {
			if when, err := time.Parse(time.RFC3339, at); err != nil || when.After(cutoff) {
				continue
			}
		}
		if fetched > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(artworkPause):
			}
		}
		fetched++
		art, err := s.TMDB().TitleArtwork(ctx, t.kind, t.id)
		if err != nil {
			slog.Debug("calendar: couldn't fetch artwork", "kind", t.kind, "tmdb", t.id, "err", err)
			continue // asked again on the next run
		}
		if err := s.MovieRepo.SaveTitleArt(t.kind, t.id, art.Backdrop, art.Logo, time.Now()); err != nil {
			slog.Warn("calendar: couldn't save the artwork", "err", err)
			return
		}
	}
}
