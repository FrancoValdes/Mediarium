package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/music"
	"github.com/rdborg/mediarium/internal/musicbrainz"
)

// The page of an album that is not in the library yet: what Discover shows
// when a card is opened. The album, its artist and tracklist come from the
// music database, the cover from the cover cache.

type musicPreviewTrack struct {
	Disc     int    `json:"disc"`
	Position int    `json:"position"`
	Title    string `json:"title"`
	LengthMs int    `json:"lengthMs,omitempty"`
}

type musicPreview struct {
	musicDiscoverItem
	Tracks []musicPreviewTrack `json:"tracks"`
}

// handleMusicPreview is GET /api/music/preview/{mbid} (any account), the
// details of one release group by its MusicBrainz id. An album already in the
// library says so (inLibrary, artistId, albumId) so the page can send you to
// it. Missing tracks (the tracklist could not be read) leave an empty list.
func (s *Server) handleMusicPreview(w http.ResponseWriter, r *http.Request) {
	mbid := strings.ToLower(r.PathValue("mbid"))
	if !mbidPattern.MatchString(mbid) {
		writeError(w, http.StatusNotFound, "That album could not be found.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	mb := s.MusicBrainz()
	g, err := mb.GetReleaseGroup(ctx, mbid)
	if err != nil {
		if errors.Is(err, musicbrainz.ErrNotFound) {
			writeError(w, http.StatusNotFound, "That album could not be found.")
			return
		}
		slog.Info("music: preview unavailable", "releaseGroup", mbid, "err", err)
		writeError(w, http.StatusBadGateway, "The music database could not be reached. Try again in a moment.")
		return
	}
	typ, ok := discoverType(g.PrimaryType)
	if !ok {
		typ = music.TypeAlbum
	}
	item := musicDiscoverItem{
		MBID:        g.ID,
		Title:       g.Title,
		Type:        typ,
		ReleaseDate: g.FirstReleaseDate,
		Genres:      []string{},
		CoverURL:    "/api/music/covers/release-group/" + g.ID,
	}
	if len(g.ArtistCredit) > 0 {
		item.ArtistName = g.ArtistCredit[0].Name
		item.ArtistMBID = g.ArtistCredit[0].Artist.ID
		if item.ArtistName == "" {
			item.ArtistName = g.ArtistCredit[0].Artist.Name
		}
	}
	sort.SliceStable(g.Genres, func(i, j int) bool { return g.Genres[i].Count > g.Genres[j].Count })
	for i, ge := range g.Genres {
		if i >= 6 {
			break
		}
		item.Genres = append(item.Genres, ge.Name)
	}
	if albums, err := s.MusicRepo.AlbumsByMBID([]string{g.ID}); err == nil {
		if a, ok := albums[g.ID]; ok {
			item.InLibrary, item.ArtistID, item.AlbumID = true, a.ArtistID, a.ID
		}
	}
	out := musicPreview{musicDiscoverItem: item, Tracks: []musicPreviewTrack{}}
	if r.URL.Query().Get("tracks") == "0" {
		// The page shows the album first and asks for the tracks next, so it doesn't wait for them.
		writeJSON(w, http.StatusOK, out)
		return
	}
	// The release group answer already lists the editions, so the canonical one is picked from it and only its tracklist is fetched.
	var rel musicbrainz.Release
	var err2 error
	if pick, ok := musicbrainz.CanonicalRelease(g.Releases); ok {
		rel, err2 = mb.GetRelease(ctx, pick.ID)
	} else {
		rel, err2 = mb.CanonicalTracklist(ctx, g.ID)
	}
	if err := err2; err == nil {
		for _, t := range rel.Tracklist() {
			out.Tracks = append(out.Tracks, musicPreviewTrack{Disc: t.Disc, Position: t.Position, Title: t.Title, LengthMs: t.LengthMs})
		}
	} else {
		slog.Info("music: tracklist unavailable for preview", "releaseGroup", g.ID, "err", err)
	}
	writeJSON(w, http.StatusOK, out)
}
