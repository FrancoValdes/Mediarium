package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/debrid"
	"github.com/rdborg/mediarium/internal/netproxy"
	"github.com/rdborg/mediarium/internal/plainerror"
	"github.com/rdborg/mediarium/internal/queue"
	"github.com/rdborg/mediarium/internal/settings"
)

// Debrid: torrent releases go to Real-Debrid or TorBox instead of the
// built-in torrent client. The service downloads the torrent; Mediarium then
// downloads the finished files over HTTPS into the download's own folder,
// and the usual unpacking and import take over. See internal/debrid.

// debridPoll is how often the service is asked how a torrent is doing.
var debridPoll = 15 * time.Second

const (
	// A torrent that sits on the service without any progress is given up on
	// after torrentStall (6 hours unless the setting says otherwise).
	// debridIDFile remembers the service's id for a download, so a restart
	// or a retry picks it up instead of adding the torrent again.
	debridIDFile = ".debrid-id"
)

// debridTestURL lets tests point the services at fakes.
var debridTestURL string

func (s *Server) debridConfig() (service, key string) {
	service, _ = s.Settings.Get(settings.KeyDebridService)
	key, _ = s.Settings.Get(settings.KeyDebridAPIKey)
	return service, key
}

// debridOn reports whether torrent releases go to a debrid service.
func (s *Server) debridOn() bool {
	service, key := s.debridConfig()
	return (service == debrid.RealDebrid || service == debrid.TorBox) && key != ""
}

func debridHTTP() *http.Client {
	// Plain web traffic: through the proxy set for indexers, if any. Long
	// timeout for big files; cancelling the download stops it at once.
	return netproxy.Client(12*time.Hour, netproxy.Indexers)
}

func (s *Server) debridService(service, key string) (debrid.Service, error) {
	svc, err := debrid.New(service, key, debridHTTP())
	if err != nil {
		return nil, err
	}
	if debridTestURL != "" {
		svc = debrid.WithBaseURL(svc, debridTestURL)
	}
	return svc, nil
}

// downloadDebrid is downloadTorrent's counterpart when a debrid service is
// set: the service's part counts as the first half of the progress bar,
// bringing the files home as the second.
func (s *Server) downloadDebrid(ctx context.Context, queueID int64, downloadURL, incompleteDir string) error {
	service, key := s.debridConfig()
	svc, err := s.debridService(service, key)
	if err != nil {
		return err
	}
	label := debrid.Label(service)
	if err := os.MkdirAll(incompleteDir, 0o755); err != nil {
		return fmt.Errorf("create download folder: %w", err)
	}
	idPath := filepath.Join(incompleteDir, debridIDFile)
	id := ""
	if b, err := os.ReadFile(idPath); err == nil {
		id = strings.TrimSpace(string(b))
	}
	if id == "" {
		magnet, torrentFile, err := s.torrentSource(ctx, downloadURL, incompleteDir)
		if err != nil {
			return fmt.Errorf("couldn't get the torrent: %w", err)
		}
		var data []byte
		if torrentFile != "" {
			if data, err = os.ReadFile(torrentFile); err != nil {
				return fmt.Errorf("read the torrent file: %w", err)
			}
			_ = os.Remove(torrentFile) // it's on the service now; keep the folder for the files
		}
		if id, err = svc.Add(ctx, magnet, data); err != nil {
			return s.debridError(label, err)
		}
		_ = os.WriteFile(idPath, []byte(id), 0o600)
		s.queueEvent(queueID, "download", queue.LevelInfo, "Sent to "+label)
	}

	progress := newProgressSaver(func(pct float64) error { return s.QueueRepo.SetProgress(queueID, pct) })
	defer progress.Flush()
	report := func(pct float64) { progress.Report(int64(pct*100), 10000) }
	lastProgress, lastMove := -1.0, time.Now()
	for {
		st, err := svc.Status(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.debridError(label, err)
		}
		if st.State == debrid.Failed {
			s.debridForget(svc, id, idPath)
			return badRelease(errors.New(st.Message))
		}
		if st.State == debrid.Ready {
			break
		}
		if st.Progress != lastProgress {
			lastProgress, lastMove = st.Progress, time.Now()
			report(st.Progress * 50)
		} else if stall := s.torrentStall(true); stall > 0 && time.Since(lastMove) > stall {
			s.debridForget(svc, id, idPath)
			return badRelease(fmt.Errorf("%s couldn't download it: nothing moved for %s, so probably no one is sharing it", label, stall))
		}
		select {
		case <-ctx.Done():
			return ctx.Err() // paused or stopped: the torrent stays on the service for later
		case <-time.After(debridPoll):
		}
	}

	files, err := svc.Files(ctx, id)
	if err != nil {
		return s.debridError(label, err)
	}
	var total, done int64
	for _, f := range files {
		total += f.Size
	}
	s.queueEvent(queueID, "download", queue.LevelInfo, fmt.Sprintf("%s has it ready; bringing %s home", label, plural(len(files), "file")))
	hc := debridHTTP()
	for _, f := range files {
		base := done
		err := debrid.Fetch(ctx, hc, f, incompleteDir, func(n int64) {
			if total > 0 {
				report(50 + 50*float64(base+n)/float64(total))
			}
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		done += f.Size
	}
	report(100)
	s.debridForget(svc, id, idPath)
	return nil
}

// debridForget removes the torrent from the service (it has served its
// purpose, and it uses up the account's slots) and the id file.
func (s *Server) debridForget(svc debrid.Service, id, idPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := svc.Remove(ctx, id); err != nil {
		slog.Warn("debrid: couldn't remove a finished torrent from the service", "service", svc.Name(), "err", err)
	}
	_ = os.Remove(idPath)
}

func (s *Server) debridError(label string, err error) error {
	if errors.Is(err, debrid.ErrAuth) {
		return fmt.Errorf("%s refused the API key. Check it under Settings > Downloading > Torrents", label)
	}
	return err
}

type debridPayload struct {
	Service string `json:"service"` // "" (off), realdebrid or torbox
	HasKey  bool   `json:"hasKey"`
	// CanCheck is true when the service can say which releases it already has
	// (TorBox; Real-Debrid has no such check). PreferCached is the setting that
	// asks it, and ranks a cached release ahead of an equal one that is not;
	// CachedLowerScore lets the cached one win even when its score is lower.
	CanCheck         bool `json:"canCheck"`
	PreferCached     bool `json:"preferCached"`
	CachedLowerScore bool `json:"cachedLowerScore"`
}

// GET /api/debrid
func (s *Server) handleGetDebrid(w http.ResponseWriter, r *http.Request) {
	service, key := s.debridConfig()
	p := debridPayload{Service: service, HasKey: key != "", CanCheck: service == debrid.TorBox}
	prefer, _ := s.Settings.Get(settings.KeyDebridPreferCached)
	p.PreferCached = prefer != "0" // on unless switched off
	lower, _ := s.Settings.Get(settings.KeyDebridCachedLower)
	p.CachedLowerScore = lower == "1"
	writeJSON(w, http.StatusOK, p)
}

type debridRequest struct {
	Service string `json:"service"`
	APIKey  string `json:"apiKey"` // blank keeps the saved one
	// PreferCached and CachedLowerScore are left alone when absent.
	PreferCached     *bool `json:"preferCached"`
	CachedLowerScore *bool `json:"cachedLowerScore"`
}

func (req debridRequest) check() string {
	switch req.Service {
	case "", debrid.RealDebrid, debrid.TorBox:
	default:
		return "Choose Real-Debrid, TorBox or none."
	}
	return firstProblem(checkAPIKey(req.APIKey), checkMaxLen(req.APIKey, "API key", 200))
}

// PUT /api/debrid {"service": "realdebrid", "apiKey": "..."}
func (s *Server) handlePutDebrid(w http.ResponseWriter, r *http.Request) {
	var req debridRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that.")
		return
	}
	if msg := req.check(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	oldService, _ := s.debridConfig()
	if req.Service == "" || (req.APIKey == "" && req.Service != oldService) {
		// Turning it off, or switching service without a new key: the old key
		// belongs to the old service.
		if err := s.Settings.Set(settings.KeyDebridAPIKey, "", true); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.APIKey != "" {
		if err := s.Settings.Set(settings.KeyDebridAPIKey, req.APIKey, true); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.Settings.Set(settings.KeyDebridService, req.Service, false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Service != "" && s.downloadClientOn() {
		// Torrent releases go to one place only.
		if err := s.Settings.Set(settings.KeyDownloadClientKind, "", false); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		slog.Info("download client turned off: torrent releases now go to the debrid service")
	}
	for key, v := range map[string]*bool{settings.KeyDebridPreferCached: req.PreferCached, settings.KeyDebridCachedLower: req.CachedLowerScore} {
		if v == nil {
			continue
		}
		val := "0"
		if *v {
			val = "1"
		}
		if err := s.Settings.Set(key, val, false); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.setCachedPolicy()
	slog.Info("debrid service saved", "service", req.Service)
	s.handleGetDebrid(w, r)
}

// POST /api/debrid/test {"service": ..., "apiKey": ...}: a blank key tests
// the saved one.
func (s *Server) handleTestDebrid(w http.ResponseWriter, r *http.Request) {
	var req debridRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that.")
		return
	}
	if msg := req.check(); msg != "" || req.Service == "" {
		if msg == "" {
			msg = "Choose Real-Debrid or TorBox."
		}
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	key := req.APIKey
	if key == "" {
		if service, saved := s.debridConfig(); service == req.Service {
			key = saved
		}
	}
	svc, err := s.debridService(req.Service, key)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Add your API key first."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	acc, err := svc.Account(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": plainerror.Message(s.debridError(debrid.Label(req.Service), err))})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "account": acc})
}
