package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/rdborg/mediarium/internal/settings"
	"github.com/rdborg/mediarium/internal/usagestats"
)

// Anonymous usage numbers (docs/usage-stats.md). Nothing is ever sent until an
// administrator has pressed "Yes", and MEDIARIUM_DISABLE_STATS=1 stops it for
// good whatever the setting says. What can be sent is only what
// usagestats.Payload holds; the settings page shows the exact message.

const (
	statsTickEvery  = 6 * time.Hour
	statsFirstAfter = 10 * time.Minute
	statsTimeout    = 15 * time.Second
)

// statsChoice is the saved answer: "yes", "no" or "" (not asked yet).
func (s *Server) statsChoice() string {
	v, _ := s.Settings.Get(settings.KeyStatsChoice)
	if v == "yes" || v == "no" {
		return v
	}
	return ""
}

// statsEnabled is true only when the administrator said yes and the
// environment does not forbid it.
func (s *Server) statsEnabled() bool {
	return !s.cfg.DisableStats && !s.cfg.PauseAutomation && s.statsChoice() == "yes"
}

func (s *Server) statsID() string {
	v, _ := s.Settings.Get(settings.KeyStatsInstallID)
	if usagestats.IsID(v) {
		return v
	}
	return ""
}

// newStatsID makes and saves a fresh random install ID.
func (s *Server) newStatsID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := usagestats.NewID(b)
	if err := s.Settings.Set(settings.KeyStatsInstallID, id, false); err != nil {
		return "", err
	}
	_ = s.Settings.Set(settings.KeyStatsLastSent, "", false)
	return id, nil
}

// count runs a single-number query; a failure counts as zero.
func (s *Server) count(query string) int {
	if s.db == nil {
		return 0
	}
	var n int
	if err := s.db.QueryRow(query).Scan(&n); err != nil {
		return 0
	}
	return n
}

func (s *Server) statsFacts(id string) usagestats.Facts {
	f := usagestats.Facts{ID: id, Version: s.version, Arch: runtime.GOARCH, Docker: s.installKind() == "docker", Cores: runtime.NumCPU(), Now: time.Now()}
	if snap := s.stats.Snapshot(); snap.Memory != nil {
		f.MemoryMB = snap.Memory.TotalBytes >> 20
	}
	if s.db != nil {
		var first string
		if err := s.db.QueryRow(`SELECT COALESCE(MIN(created_at), '') FROM users`).Scan(&first); err == nil && first != "" {
			if t, err := time.Parse(time.RFC3339Nano, first); err == nil {
				f.Installed = t
			}
		}
	}
	f.Titles = s.count(`SELECT (SELECT COUNT(*) FROM movies) + (SELECT COUNT(*) FROM series) + (SELECT COUNT(*) FROM albums) + (SELECT COUNT(*) FROM books)`)
	f.Accounts = s.count(`SELECT COUNT(*) FROM users`)
	f.Indexers = s.count(`SELECT COUNT(*) FROM indexers WHERE enabled = 1`)

	for key, kind := range map[string]string{settings.KeyModulesMovies: "movies", settings.KeyModulesTV: "tv"} {
		if s.settingOn(key, true) {
			f.Media = append(f.Media, kind)
		}
	}
	for key, kind := range map[string]string{settings.KeyModulesMusic: "music", settings.KeyModulesEbooks: "ebooks", settings.KeyModulesAudiobooks: "audiobooks"} {
		if s.settingOn(key, false) {
			f.Media = append(f.Media, kind)
		}
	}

	feature := func(on bool, name string) {
		if on {
			f.Features = append(f.Features, name)
		}
	}
	has := func(key string) bool { v, _ := s.Settings.Get(key); return v != "" }
	feature(s.count(`SELECT COUNT(*) FROM download_clients WHERE enabled = 1`) > 0, "usenet")
	feature(s.settingOn(settings.KeyTorrentEnabled, true), "torrents")
	feature(s.count(`SELECT COUNT(*) FROM vpn_configs`) > 0, "vpn")
	feature(has(settings.KeyProxy), "proxy")
	feature(has(settings.KeyDebridService), "debrid")
	feature(has(settings.KeyDownloadClientKind), "download-client")
	feature(s.count(`SELECT COUNT(*) FROM irc_connections`) > 0, "irc")
	feature(s.count(`SELECT COUNT(*) FROM import_lists`) > 0, "import-lists")
	feature(s.count(`SELECT COUNT(*) FROM requests`) > 0, "requests")
	feature(s.count(`SELECT COUNT(*) FROM user_identities`) > 0, "sso")
	feature(s.count(`SELECT COUNT(*) FROM passkeys`) > 0, "passkeys")
	feature(s.count(`SELECT COUNT(*) FROM kobo_library`) > 0, "kobo")
	feature(s.count(`SELECT COUNT(*) FROM notification_targets`) > 0, "notifications")
	feature(s.count(`SELECT COUNT(*) FROM subtitle_files`) > 0, "subtitles")

	if s.db != nil {
		if rows, err := s.db.Query(`SELECT DISTINCT kind FROM media_servers WHERE enabled = 1`); err == nil {
			defer rows.Close()
			for rows.Next() {
				var kind string
				if rows.Scan(&kind) == nil {
					f.Servers = append(f.Servers, kind)
				}
			}
		}
	}
	return f
}

// statsPayload is the message as it would be sent now. Before anyone said yes there
// is no ID yet, so the preview shows a placeholder shaped like one.
func (s *Server) statsPayload() usagestats.Payload {
	id := s.statsID()
	if id == "" {
		id = "00000000000000000000000000000000"
	}
	return usagestats.Build(s.statsFacts(id))
}

type statsState struct {
	Choice   string             `json:"choice"`   // "yes", "no" or "" when not asked yet
	Disabled bool               `json:"disabled"` // MEDIARIUM_DISABLE_STATS is set
	URL      string             `json:"url"`
	LastSent string             `json:"lastSent"`
	HasID    bool               `json:"hasId"`
	Preview  usagestats.Payload `json:"preview"`
}

func (s *Server) statsState() statsState {
	last, _ := s.Settings.Get(settings.KeyStatsLastSent)
	return statsState{Choice: s.statsChoice(), Disabled: s.cfg.DisableStats, URL: usagestats.URL, LastSent: last, HasID: s.statsID() != "", Preview: s.statsPayload()}
}

func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.statsState())
}

func (s *Server) handlePutStats(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Choice string `json:"choice"`
	}
	if err := decodeJSON(r, &req); err != nil || (req.Choice != "yes" && req.Choice != "no") {
		writeError(w, http.StatusBadRequest, "Choose Yes or No.")
		return
	}
	if err := s.Settings.Set(settings.KeyStatsChoice, req.Choice, false); err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't save your choice.")
		return
	}
	if req.Choice == "yes" {
		if s.statsID() == "" {
			if _, err := s.newStatsID(); err != nil {
				writeError(w, http.StatusInternalServerError, "Couldn't save your choice.")
				return
			}
		}
		go s.sendStats(context.Background(), true)
	} else {
		// No: the ID is forgotten, so nothing links later numbers to the earlier ones.
		_ = s.Settings.Set(settings.KeyStatsInstallID, "", false)
		_ = s.Settings.Set(settings.KeyStatsLastSent, "", false)
	}
	writeJSON(w, http.StatusOK, s.statsState())
}

func (s *Server) handleResetStatsID(w http.ResponseWriter, r *http.Request) {
	if s.statsChoice() == "yes" {
		if _, err := s.newStatsID(); err != nil {
			writeError(w, http.StatusInternalServerError, "Couldn't make a new ID.")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.statsState())
}

// StartUsageStats starts the daily send. It does nothing until the answer is yes.
func (s *Server) StartUsageStats() (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(statsFirstAfter)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			s.sendStats(ctx, false)
			timer.Reset(statsTickEvery)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// sendStats sends the message once per day (or at once when now is true, right
// after a Yes). A failure is only logged and tried again later.
func (s *Server) sendStats(ctx context.Context, now bool) {
	if !s.statsEnabled() {
		return
	}
	today := time.Now().UTC().Format("2006-01-02")
	if last, _ := s.Settings.Get(settings.KeyStatsLastSent); last == today {
		return
	}
	id := s.statsID()
	if id == "" {
		var err error
		if id, err = s.newStatsID(); err != nil {
			return
		}
	}
	body, err := json.Marshal(usagestats.Build(s.statsFacts(id)))
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, statsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.statsURL(), bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mediarium/"+s.version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Debug("usage stats: not sent", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		slog.Debug("usage stats: not accepted", "status", resp.StatusCode)
		return
	}
	_ = s.Settings.Set(settings.KeyStatsLastSent, today, false)
}

// statsURL is where the message goes; tests point it at a local server.
func (s *Server) statsURL() string {
	if s.statsEndpoint != "" {
		return s.statsEndpoint
	}
	return usagestats.URL
}
