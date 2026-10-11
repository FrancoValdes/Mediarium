package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rdborg/mediarium/internal/delay"
	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/quality"
	"github.com/rdborg/mediarium/internal/settings"
)

// Settings for how searches treat what they find: the delay profile (wait for
// the protocol you like better) and the size limits per quality. Both are off
// until someone switches them on.

// preferredProto is the protocol that wins between two equally good releases:
// 0 Usenet, 1 torrent. Set from the delay profile.
var preferredProto atomic.Int32

func preferredProtocol() indexers.Protocol {
	if preferredProto.Load() == 1 {
		return indexers.ProtocolTorrent
	}
	return indexers.ProtocolUsenet
}

func setPreferredProtocol(p string) {
	if p == delay.Torrent {
		preferredProto.Store(1)
		return
	}
	preferredProto.Store(0)
}

// delayConfig reads the delay profile; a missing or unreadable one is "no wait".
func (s *Server) delayConfig() delay.Config {
	cfg := delay.Default()
	raw, err := s.Settings.Get(settings.KeySearchDelay)
	if err != nil || raw == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		slog.Warn("api: read the delay profile", "err", err)
		return delay.Default()
	}
	if cfg.Validate() != nil {
		return delay.Default()
	}
	return cfg
}

func (s *Server) handleGetDelay(w http.ResponseWriter, r *http.Request) {
	cfg := s.delayConfig()
	cfg.PreferredProtocol = cfg.Preferred()
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handlePutDelay(w http.ResponseWriter, r *http.Request) {
	var cfg delay.Config
	if err := decodeJSON(r, &cfg); err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid delay profile.")
		return
	}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.PreferredProtocol = cfg.Preferred()
	raw, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Settings.Set(settings.KeySearchDelay, string(raw), false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	setPreferredProtocol(cfg.PreferredProtocol)
	writeJSON(w, http.StatusOK, cfg)
}

// sizeLimits reads the size table; a missing or unreadable one is "off".
func (s *Server) sizeLimits() quality.SizeLimits {
	raw, err := s.Settings.Get(settings.KeySizeLimits)
	if err != nil || raw == "" {
		return quality.SizeLimits{}
	}
	var t quality.SizeLimits
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		slog.Warn("api: read the size limits", "err", err)
		return quality.SizeLimits{}
	}
	if t.Validate() != nil {
		return quality.SizeLimits{}
	}
	return t
}

// sizeRow is one quality's line in the size table.
type sizeRow struct {
	Quality    string  `json:"quality"`
	Min        float64 `json:"min"`
	Max        float64 `json:"max"` // 0 = no upper limit
	DefaultMin float64 `json:"defaultMin"`
	DefaultMax float64 `json:"defaultMax"`
}

type sizeTablePayload struct {
	Enabled bool      `json:"enabled"`
	Rows    []sizeRow `json:"rows"`
}

func sizeTablePayloadOf(t quality.SizeLimits) sizeTablePayload {
	defaults := quality.DefaultSizeLimits()
	out := sizeTablePayload{Enabled: t.Enabled, Rows: []sizeRow{}}
	for _, tier := range quality.SizeTiers() {
		cur, _ := t.Limit(tier)
		def := defaults[tier]
		out.Rows = append(out.Rows, sizeRow{Quality: string(tier), Min: cur.Min, Max: cur.Max, DefaultMin: def.Min, DefaultMax: def.Max})
	}
	return out
}

func (s *Server) handleGetSizeLimits(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sizeTablePayloadOf(s.sizeLimits()))
}

func (s *Server) handlePutSizeLimits(w http.ResponseWriter, r *http.Request) {
	var in sizeTablePayload
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid size table.")
		return
	}
	table := quality.SizeLimits{Enabled: in.Enabled, Limits: map[quality.Tier]quality.SizeLimit{}}
	defaults := quality.DefaultSizeLimits()
	for _, row := range in.Rows {
		tier := quality.Tier(row.Quality)
		l := quality.SizeLimit{Min: row.Min, Max: row.Max}
		if def, ok := defaults[tier]; ok && def == l {
			continue // stays the default, so a later change of the defaults reaches it
		}
		table.Limits[tier] = l
	}
	if err := table.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.saveSizeLimits(w, table)
}

// handleResetSizeLimits puts every quality back to its default. The switch
// keeps its position.
func (s *Server) handleResetSizeLimits(w http.ResponseWriter, r *http.Request) {
	s.saveSizeLimits(w, quality.SizeLimits{Enabled: s.sizeLimits().Enabled})
}

func (s *Server) saveSizeLimits(w http.ResponseWriter, table quality.SizeLimits) {
	raw, err := json.Marshal(table)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Settings.Set(settings.KeySizeLimits, string(raw), false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sizeTablePayloadOf(table))
}

// Runtimes, for judging a size per minute. They come from TMDB and are kept
// for a day; when TMDB can't say, a typical length is used.
const (
	defaultMovieMinutes   = 100.0
	defaultEpisodeMinutes = 45.0
	runtimeKeep           = 24 * time.Hour
	runtimeKeepFallback   = 10 * time.Minute
)

type runtimeEntry struct {
	minutes float64
	until   time.Time
}

var runtimeCache sync.Map // "movie:603" or "tv:1399" -> runtimeEntry

func cachedRuntime(key string, now time.Time, find func() (float64, bool), fallback float64) float64 {
	if v, ok := runtimeCache.Load(key); ok {
		if e := v.(runtimeEntry); now.Before(e.until) {
			return e.minutes
		}
	}
	minutes, ok := find()
	keep := runtimeKeep
	if !ok {
		minutes, keep = fallback, runtimeKeepFallback
	}
	runtimeCache.Store(key, runtimeEntry{minutes: minutes, until: now.Add(keep)})
	return minutes
}

func (s *Server) movieRuntime(ctx context.Context, m library.Movie) float64 {
	if m.TMDBID == 0 {
		return defaultMovieMinutes
	}
	return cachedRuntime("movie:"+strconv.Itoa(m.TMDBID), time.Now(), func() (float64, bool) {
		c := s.TMDB()
		if c == nil || !c.HasAPIKey() {
			return 0, false
		}
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		d, err := c.GetMovieDetail(ctx, m.TMDBID)
		if err != nil || d.Runtime <= 0 {
			return 0, false
		}
		return float64(d.Runtime), true
	}, defaultMovieMinutes)
}

func (s *Server) episodeRuntime(ctx context.Context, series library.Series) float64 {
	if series.TMDBID == 0 {
		return defaultEpisodeMinutes
	}
	return cachedRuntime("tv:"+strconv.Itoa(series.TMDBID), time.Now(), func() (float64, bool) {
		c := s.TMDB()
		if c == nil || !c.HasAPIKey() {
			return 0, false
		}
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		d, err := c.GetShow(ctx, series.TMDBID)
		if err != nil {
			return 0, false
		}
		for _, n := range d.EpisodeRunTime {
			if n > 0 {
				return float64(n), true
			}
		}
		return 0, false
	}, defaultEpisodeMinutes)
}
