package api

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/lrclib"
	"github.com/rdborg/mediarium/internal/mediafiles"
	"github.com/rdborg/mediarium/internal/music"
	"github.com/rdborg/mediarium/internal/settings"
)

// Lyrics, ReplayGain and the player settings that go with them.
//
// Lyrics come from, in this order: a .lrc file with the same name next to the
// track, the lyrics written in the file's tags, and then LRCLIB (lrclib.net).
// The online answer is kept in the database so a track is asked about once.
// Synced lyrics (with times) win over plain ones, wherever they come from.

// keyedLocks serialises work per id, so two people opening the lyrics of the
// same track make one request to LRCLIB.
type keyedLocks struct {
	mu sync.Mutex
	m  map[int64]*sync.Mutex
}

func (k *keyedLocks) lock(id int64) func() {
	k.mu.Lock()
	if k.m == nil || len(k.m) > 1000 {
		k.m = map[int64]*sync.Mutex{}
	}
	l := k.m[id]
	if l == nil {
		l = &sync.Mutex{}
		k.m[id] = l
	}
	k.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// lyricsOnline is whether the player may ask LRCLIB for lyrics. On until an
// administrator turns it off.
func (s *Server) lyricsOnline() bool {
	v, _ := s.Settings.Get(settings.KeyMusicLyricsOnline)
	return v != "0"
}

func (s *Server) lyricsClient() *lrclib.Client {
	s.music.playerMu.Lock()
	defer s.music.playerMu.Unlock()
	if s.music.lyrics == nil {
		s.music.lyrics = lrclib.New(s.version)
	}
	return s.music.lyrics
}

// trackFile finds a track's file inside the music folder, answering the
// request itself when it can't.
func (s *Server) trackFile(w http.ResponseWriter, r *http.Request) (music.Track, mediafiles.Folder, string, bool) {
	id, ok := musicID(w, r, "track")
	if !ok {
		return music.Track{}, mediafiles.Folder{}, "", false
	}
	t, err := s.MusicRepo.GetTrack(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "That track isn't in your library.")
		return music.Track{}, mediafiles.Folder{}, "", false
	}
	if err != nil {
		slog.Warn("read track", "track", id, "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't read that track. Check the log for details.")
		return music.Track{}, mediafiles.Folder{}, "", false
	}
	if t.FilePath == "" {
		writeError(w, http.StatusNotFound, "This track hasn't been downloaded yet.")
		return music.Track{}, mediafiles.Folder{}, "", false
	}
	folder, err := mediafiles.ForFile(t.FilePath, []string{s.musicRoot()}, false)
	if err != nil {
		writeStreamFolderError(w, err)
		return music.Track{}, mediafiles.Folder{}, "", false
	}
	rel, ok := folder.Rel(t.FilePath)
	if !ok {
		writeError(w, http.StatusForbidden, "This track's file isn't inside your music folder.")
		return music.Track{}, mediafiles.Folder{}, "", false
	}
	return t, folder, rel, true
}

type lyricsJSON struct {
	Source       string `json:"source"` // "sidecar", "file", "lrclib" or "" when there are none
	Synced       string `json:"synced"` // LRC text, "" when there is none
	Plain        string `json:"plain"`
	Instrumental bool   `json:"instrumental"`
	Online       bool   `json:"online"` // whether the online lookup is switched on
}

// fileLyrics reads the .lrc beside the track, then the tags of the file.
func fileLyrics(folder mediafiles.Folder, rel string) music.Lyrics {
	if f, _, err := folder.Open(music.SidecarName(rel)); err == nil {
		l, rerr := music.SidecarLyrics(f)
		f.Close()
		if rerr != nil {
			slog.Debug("read lyrics file", "err", rerr)
		} else if l.Has() {
			return l
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		slog.Debug("open lyrics file", "err", err)
	}
	f, _, err := folder.Open(rel)
	if err != nil {
		return music.Lyrics{}
	}
	defer f.Close()
	l, err := music.EmbeddedLyrics(f)
	if err != nil {
		slog.Debug("read lyrics tags", "err", err)
	}
	return l
}

// GET /api/music/tracks/{id}/lyrics
func (s *Server) handleTrackLyrics(w http.ResponseWriter, r *http.Request) {
	t, folder, rel, ok := s.trackFile(w, r)
	if !ok {
		return
	}
	online := s.lyricsOnline()
	out := fileLyrics(folder, rel)
	// Words from the file are kept, but synced ones found online are better
	// than plain ones in the file.
	if online && out.Synced == "" {
		if got, ok := s.onlineLyrics(r.Context(), t); ok {
			switch {
			case got.Synced != "":
				if out.Plain == "" {
					out.Plain = got.Plain
				}
				out.Synced, out.Source = got.Synced, got.Source
			case out.Plain == "" && got.Plain != "":
				out.Plain, out.Source = got.Plain, got.Source
			case !out.Has() && got.Instrumental:
				out.Instrumental, out.Source = true, got.Source
			}
		}
	}
	writeJSON(w, http.StatusOK, lyricsJSON{Source: out.Source, Synced: out.Synced, Plain: out.Plain, Instrumental: out.Instrumental, Online: online})
}

// onlineLyrics returns what LRCLIB has for a track, from the database when it
// was asked before. ok is false when nothing was found or the service could
// not be reached (the latter is not remembered, so the next try asks again).
func (s *Server) onlineLyrics(ctx context.Context, t music.Track) (music.Lyrics, bool) {
	unlock := s.music.lyricsAt.lock(t.ID)
	defer unlock()
	if c, ok, err := s.MusicRepo.GetCachedLyrics(t.ID); err != nil {
		slog.Warn("read cached lyrics", "track", t.ID, "err", err)
	} else if ok && c.Fresh(time.Now()) {
		return c.Lyrics, c.Found
	}
	album, err := s.MusicRepo.GetAlbum(t.AlbumID)
	if err != nil {
		return music.Lyrics{}, false
	}
	artist, err := s.MusicRepo.GetArtist(album.ArtistID)
	if err != nil {
		return music.Lyrics{}, false
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := s.lyricsClient().Lookup(cctx, t.Title, artist.Name, album.Title, (t.LengthMs+500)/1000)
	if err != nil {
		slog.Info("online lyrics lookup failed", "track", t.ID, "err", err)
		return music.Lyrics{}, false
	}
	l := music.Lyrics{Source: "lrclib", Synced: res.Synced, Plain: res.Plain, Instrumental: res.Instrumental}
	if err := s.MusicRepo.PutCachedLyrics(t.ID, l, res.Found); err != nil {
		slog.Warn("keep lyrics", "track", t.ID, "err", err)
	}
	return l, res.Found
}

// GET /api/music/tracks/{id}/gain: the ReplayGain tags of a track's file, null
// for the ones it doesn't have.
func (s *Server) handleTrackGain(w http.ResponseWriter, r *http.Request) {
	_, folder, rel, ok := s.trackFile(w, r)
	if !ok {
		return
	}
	f, _, err := folder.Open(rel)
	if !s.openedOK(w, err) {
		return
	}
	defer f.Close()
	g, err := music.ReadReplayGain(f)
	if err != nil {
		// A file whose tags can't be read just plays at its own level.
		slog.Debug("read replaygain tags", "err", err)
		g = music.ReplayGain{}
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, g)
}

// GET /api/music/player-settings
func (s *Server) handleGetPlayerSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"lyricsOnline": s.lyricsOnline()})
}

// PUT /api/music/player-settings {lyricsOnline}
func (s *Server) handlePutPlayerSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LyricsOnline *bool `json:"lyricsOnline"`
	}
	if err := decodeJSON(r, &req); err != nil || req.LyricsOnline == nil {
		writeError(w, http.StatusBadRequest, "Say whether lyrics may be looked up online.")
		return
	}
	v := "1"
	if !*req.LyricsOnline {
		v = "0"
	}
	if err := s.Settings.Set(settings.KeyMusicLyricsOnline, v, false); err != nil {
		slog.Warn("save lyrics setting", "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't save the setting. Check the log for details.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lyricsOnline": *req.LyricsOnline})
}
