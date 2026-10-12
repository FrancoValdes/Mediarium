package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/debrid"
	"github.com/rdborg/mediarium/internal/netguard"
	"github.com/rdborg/mediarium/internal/qbit"
)

// What the Activity page's "Download clients" tab shows: the torrents in the
// download client the person set up (qBittorrent, RDT-Client, Decypharr), with
// pause, resume and remove next to each. Administrators only.

// clientTorrent is one torrent in the client, in the words the page shows.
type clientTorrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Stage       string  `json:"stage"` // downloading, waiting, queued, checking, paused, seeding, done, problem
	StageText   string  `json:"stageText"`
	Progress    float64 `json:"progress"` // 0 to 100
	SizeBytes   int64   `json:"sizeBytes"`
	DownSpeed   int64   `json:"downSpeed"`
	UpSpeed     int64   `json:"upSpeed"`
	Seeds       int     `json:"seeds"`      // connected to us
	Peers       int     `json:"peers"`      // downloaders connected to us
	SwarmSeeds  int     `json:"swarmSeeds"` // in the swarm
	SwarmPeers  int     `json:"swarmPeers"` // in the swarm
	EtaSeconds  int64   `json:"etaSeconds"` // 0 = unknown
	Ratio       float64 `json:"ratio"`
	AddedAt     string  `json:"addedAt,omitempty"`
	IdleSeconds int64   `json:"idleSeconds"` // since it last moved any data
	Category    string  `json:"category,omitempty"`
	Mine        bool    `json:"mine"` // added by Mediarium
	QueueID     int64   `json:"queueId,omitempty"`
	CanPause    bool    `json:"canPause"`
	CanResume   bool    `json:"canResume"`
	Downloaded  int64   `json:"downloadedBytes"`
	Uploaded    int64   `json:"uploadedBytes"`
	SavePath    string  `json:"savePath,omitempty"`
}

type clientTorrentsAnswer struct {
	Configured bool            `json:"configured"`
	Kind       string          `json:"kind,omitempty"`
	Label      string          `json:"label,omitempty"`
	Category   string          `json:"category,omitempty"`
	Error      string          `json:"error,omitempty"`
	Torrents   []clientTorrent `json:"torrents"`
}

// torrentRoute says where torrent downloads go right now, in the order the
// pipeline picks: a connected download client, then a debrid service, then the
// built-in client.
func (s *Server) torrentRoute() (via, label string) {
	switch {
	case s.downloadClientOn():
		return "client", qbit.Label(s.downloadClientConfig().Kind)
	case s.debridOn():
		service, _ := s.debridConfig()
		return "debrid", debrid.Label(service)
	}
	return "builtin", ""
}

// qbitInfinity is what qBittorrent reports as the time left when it is never.
const qbitInfinity = 8640000

var clientHashPattern = regexp.MustCompile(`^[0-9a-fA-F]{32,64}$`)

// clientStage puts a qBittorrent state into a few plain words.
func clientStage(state string) (stage, text string) {
	switch state {
	case "downloading", "forcedDL":
		return "downloading", "Downloading"
	case "stalledDL":
		return "waiting", "Waiting for peers"
	case "metaDL", "forcedMetaDL":
		return "waiting", "Getting the details"
	case "queuedDL", "queuedUP":
		return "queued", "Waiting in line"
	case "checkingDL", "checkingUP", "checkingResumeData":
		return "checking", "Checking the files"
	case "pausedDL", "stoppedDL":
		return "paused", "Paused"
	case "pausedUP", "stoppedUP":
		return "done", "Finished"
	case "uploading", "stalledUP", "forcedUP":
		return "seeding", "Seeding"
	case "moving":
		return "checking", "Moving the files"
	case "allocating":
		return "checking", "Setting up the files"
	case "error", "missingFiles":
		return "problem", "Problem: the client can't use it"
	}
	return "waiting", state
}

// queueIDOf reads the queue number out of the tag Mediarium put on a torrent.
func queueIDOf(tags string) int64 {
	for _, tag := range strings.Split(tags, ",") {
		tag = strings.TrimSpace(tag)
		if strings.HasPrefix(tag, "mediarium-q") {
			if n, err := strconv.ParseInt(strings.TrimPrefix(tag, "mediarium-q"), 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

func toClientTorrent(t qbit.Torrent, category string, now time.Time) clientTorrent {
	stage, text := clientStage(t.State)
	out := clientTorrent{
		Hash: t.Hash, Name: t.Name, Stage: stage, StageText: text, Progress: t.Progress * 100, SizeBytes: t.Size,
		DownSpeed: t.DLSpeed, UpSpeed: t.UPSpeed, Seeds: t.Seeds, Peers: t.Leeches, SwarmSeeds: t.SwarmSeeds, SwarmPeers: t.SwarmLeeches,
		Ratio: t.Ratio, Category: t.Category, QueueID: queueIDOf(t.Tags), Downloaded: t.Downloaded, Uploaded: t.Uploaded, SavePath: t.SavePath,
		CanPause: stage != "paused" && stage != "done", CanResume: stage == "paused" || stage == "done",
	}
	out.Mine = out.QueueID > 0 || (category != "" && t.Category == category)
	if t.ETA > 0 && t.ETA < qbitInfinity {
		out.EtaSeconds = t.ETA
	}
	if t.AddedOn > 0 {
		out.AddedAt = time.Unix(t.AddedOn, 0).UTC().Format(time.RFC3339)
	}
	if t.LastActivity > 0 && stage != "paused" && stage != "done" && stage != "seeding" {
		if idle := now.Unix() - t.LastActivity; idle > 0 {
			out.IdleSeconds = idle
		}
	}
	return out
}

// handleClientTorrents is GET /api/download-client/torrents?scope=mine|all: the
// torrents in the download client, only Mediarium's own (its category) or
// everything in it. Always answers 200: a client that can't be reached says so
// in "error" so the page can show it.
func (s *Server) handleClientTorrents(w http.ResponseWriter, r *http.Request) {
	cfg := s.downloadClientConfig()
	out := clientTorrentsAnswer{Torrents: []clientTorrent{}}
	if !s.downloadClientOn() {
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.Configured, out.Kind, out.Label, out.Category = true, cfg.Kind, qbit.Label(cfg.Kind), cfg.category()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	c := newDownloadClient(cfg.Kind, cfg.URL, cfg.Username, cfg.Password)
	category := cfg.category()
	if r.URL.Query().Get("scope") == "all" {
		category = ""
	}
	list, err := c.List(ctx, category)
	if err != nil {
		out.Error = clientErrorText(out.Label, s.downloadClientError(out.Label, err))
		writeJSON(w, http.StatusOK, out)
		return
	}
	now := time.Now()
	for _, t := range list {
		out.Torrents = append(out.Torrents, toClientTorrent(t, cfg.category(), now))
	}
	sort.SliceStable(out.Torrents, func(i, j int) bool {
		a, b := out.Torrents[i], out.Torrents[j]
		if (a.Stage == "downloading") != (b.Stage == "downloading") {
			return a.Stage == "downloading"
		}
		return a.AddedAt > b.AddedAt
	})
	writeJSON(w, http.StatusOK, out)
}

// clientErrorText is an error in the words the page shows.
func clientErrorText(label string, err error) string {
	var nerr interface{ Timeout() bool }
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &nerr) && nerr.Timeout()):
		return label + " didn't answer in time. Check that it is running and that the address under Settings > Downloading > Torrents is right."
	case errors.Is(err, netguard.ErrBlocked):
		return "Mediarium may not connect to that address."
	}
	return netguard.CleanError(err).Error()
}

// handleClientTorrentAction is POST /api/download-client/torrents/{hash}/{action}
// with action pause, resume, recheck, remove (keep the files) or remove-files.
func (s *Server) handleClientTorrentAction(w http.ResponseWriter, r *http.Request) {
	hash, action := r.PathValue("hash"), r.PathValue("action")
	if !clientHashPattern.MatchString(hash) {
		writeError(w, http.StatusBadRequest, "That isn't a torrent.")
		return
	}
	if !s.downloadClientOn() {
		writeError(w, http.StatusConflict, "No download client is set up. Add one under Settings > Downloading > Torrents.")
		return
	}
	cfg := s.downloadClientConfig()
	label := qbit.Label(cfg.Kind)
	c := newDownloadClient(cfg.Kind, cfg.URL, cfg.Username, cfg.Password)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var err error
	var done string
	switch action {
	case "pause":
		err, done = c.Pause(ctx, hash), "paused"
	case "resume":
		err, done = c.Resume(ctx, hash), "started again"
	case "recheck":
		err, done = c.Recheck(ctx, hash), "being checked again"
	case "remove", "remove-files":
		name := hash
		if t, ok, e := c.Info(ctx, hash); e == nil && ok {
			name = t.Name
		}
		err, done = c.Delete(ctx, hash, action == "remove-files"), "removed"
		if err == nil {
			what := "removed from " + label
			if action == "remove-files" {
				what += ", with its files"
			}
			_ = s.QueueRepo.LogActivity(0, "removed", fmt.Sprintf("%s %s by hand", name, what))
		}
	default:
		writeError(w, http.StatusBadRequest, "That isn't something a torrent can do here.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, clientErrorText(label, s.downloadClientError(label, err)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"done": done})
}

// debridTorrent is one torrent on the debrid service, for the same tab.
type debridTorrent struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Stage     string  `json:"stage"` // downloading, waiting, ready, problem
	StageText string  `json:"stageText"`
	Progress  float64 `json:"progress"` // 0 to 100
	SizeBytes int64   `json:"sizeBytes"`
	DownSpeed int64   `json:"downSpeed"`
	Seeders   int     `json:"seeders"` // -1 when the service doesn't say
	AddedAt   string  `json:"addedAt,omitempty"`
}

type debridTorrentsAnswer struct {
	Configured bool            `json:"configured"`
	Label      string          `json:"label,omitempty"`
	Error      string          `json:"error,omitempty"`
	Torrents   []debridTorrent `json:"torrents"`
}

// handleDebridTorrents is GET /api/debrid/torrents: what is on the Real-Debrid
// or TorBox account. Always 200; a problem is in "error".
func (s *Server) handleDebridTorrents(w http.ResponseWriter, r *http.Request) {
	out := debridTorrentsAnswer{Torrents: []debridTorrent{}}
	if !s.debridOn() {
		writeJSON(w, http.StatusOK, out)
		return
	}
	service, key := s.debridConfig()
	out.Configured, out.Label = true, debrid.Label(service)
	svc, err := s.debridService(service, key)
	if err != nil {
		out.Error = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	list, err := svc.List(ctx)
	if err != nil {
		out.Error = clientErrorText(out.Label, s.debridError(out.Label, err))
		writeJSON(w, http.StatusOK, out)
		return
	}
	for _, it := range list {
		d := debridTorrent{ID: it.ID, Name: it.Name, Progress: it.Progress * 100, SizeBytes: it.Size, DownSpeed: it.Speed, Seeders: it.Seeders}
		switch it.State {
		case debrid.Ready:
			d.Stage, d.StageText = "ready", "Ready on "+out.Label
		case debrid.Downloading:
			d.Stage, d.StageText = "downloading", "Downloading on "+out.Label
		case debrid.Failed:
			d.Stage, d.StageText = "problem", "Problem: "+it.Text
		default:
			d.Stage, d.StageText = "waiting", "Waiting"
		}
		if !it.Added.IsZero() {
			d.AddedAt = it.Added.UTC().Format(time.RFC3339)
		}
		out.Torrents = append(out.Torrents, d)
	}
	sort.SliceStable(out.Torrents, func(i, j int) bool { return out.Torrents[i].AddedAt > out.Torrents[j].AddedAt })
	writeJSON(w, http.StatusOK, out)
}

var debridIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// handleDebridRemove is POST /api/debrid/torrents/{id}/remove: delete a
// torrent from the account.
func (s *Server) handleDebridRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !debridIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "That isn't a torrent.")
		return
	}
	if !s.debridOn() {
		writeError(w, http.StatusConflict, "No debrid service is set up.")
		return
	}
	service, key := s.debridConfig()
	label := debrid.Label(service)
	svc, err := s.debridService(service, key)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := svc.Remove(ctx, id); err != nil {
		writeError(w, http.StatusBadGateway, clientErrorText(label, s.debridError(label, err)))
		return
	}
	_ = s.QueueRepo.LogActivity(0, "removed", fmt.Sprintf("A torrent was removed from %s by hand", label))
	writeJSON(w, http.StatusOK, map[string]string{"done": "removed"})
}
