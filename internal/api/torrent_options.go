package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/queue"
	"github.com/rdborg/mediarium/internal/settings"
	"github.com/rdborg/mediarium/internal/torrentclient"
	"github.com/rdborg/mediarium/internal/torrentopts"
)

// Controls for the built-in torrent client: the global options, what a person
// chose for one torrent, the categories and the seeding rules of an indexer.

// maxTorrentLimitKB is the largest speed limit that can be set, in KB/s.
const maxTorrentLimitKB = 10_000_000

func (s *Server) settingIsOne(key string) bool {
	v, _ := s.Settings.Get(key)
	return v == "1"
}

// torrentSequentialDefault: new torrents download in order.
func (s *Server) torrentSequentialDefault() bool {
	return s.settingIsOne(settings.KeyTorrentSequential)
}

// torrentSkipJunk: samples, notes and programs are left out of a torrent. On
// unless it was switched off.
func (s *Server) torrentSkipJunk() bool {
	v, _ := s.Settings.Get(settings.KeyTorrentSkipJunk)
	return v != "0"
}

// torrentPackOnlyWanted: a pack is cut down to the wanted season.
func (s *Server) torrentPackOnlyWanted() bool {
	return s.settingIsOne(settings.KeyTorrentPackOnlyWanted)
}

// torrentPortMapping: the engine asks the router to open its port. On unless it
// was switched off, as it always was.
func (s *Server) torrentPortMapping() bool {
	v, _ := s.Settings.Get(settings.KeyTorrentPortMapping)
	return v != "0"
}

// torrentAllowLocal: trackers, web seeds and magnet metadata may be on the home
// network. Off unless switched on.
func (s *Server) torrentAllowLocal() bool {
	return s.settingIsOne(settings.KeyTorrentAllowLocal)
}

// torrentUploadLimitKB is the upload limit in KB/s, 0 for none.
func (s *Server) torrentUploadLimitKB() int {
	v, _ := s.Settings.Get(settings.KeyTorrentUploadLimitKB)
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// torrentCategoryConfig is the saved category list.
func (s *Server) torrentCategoryConfig() torrentopts.CategoryConfig {
	list, _ := s.Settings.Get(settings.KeyTorrentCategories)
	byKind, _ := s.Settings.Get(settings.KeyTorrentCategoryMap)
	return torrentopts.ParseCategories(list, byKind)
}

// queueKind is the kind of grab a queue item is, as categories name them.
func queueKind(it queue.Item) string {
	switch {
	case it.BookID > 0:
		return torrentopts.KindBook
	case it.AlbumID > 0:
		return torrentopts.KindMusic
	case it.SeriesID > 0:
		return torrentopts.KindTV
	}
	return torrentopts.KindMovie
}

// workDirFor is the working folder of queue item id: directly in the working
// folder, or inside its category's folder when the built-in torrent client
// filed it under one.
func (s *Server) workDirFor(id int64) string {
	root := s.downloadsIncompleteDir()
	if s.TorrentOpts != nil {
		if o, ok, _ := s.TorrentOpts.Get(id); ok && o.Subfolder != "" {
			return filepath.Join(root, o.Subfolder, fmt.Sprintf("queue-%d", id))
		}
	}
	return filepath.Join(root, fmt.Sprintf("queue-%d", id))
}

// categoryFolders lists the folders inside the working folder that hold
// download folders: those of the categories now and of downloads filed earlier.
func (s *Server) categoryFolders() []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[strings.ToLower(name)] {
			seen[strings.ToLower(name)] = true
			out = append(out, name)
		}
	}
	for _, c := range s.torrentCategoryConfig().Categories {
		add(c.Subfolder)
	}
	if s.TorrentOpts != nil {
		if used, err := s.TorrentOpts.Subfolders(); err == nil {
			for _, u := range used {
				add(u)
			}
		}
	}
	return out
}

// isWorkDirPath reports whether dir is a download's own folder: queue-N directly
// in the working folder, or inside one of the category folders.
func (s *Server) isWorkDirPath(dir, work string) bool {
	dir, work = filepath.Clean(dir), filepath.Clean(work)
	if _, ok := queueDirID(filepath.Base(dir)); !ok {
		return false
	}
	parent := filepath.Dir(dir)
	if parent == work {
		return true
	}
	if filepath.Dir(parent) != work {
		return false
	}
	for _, f := range s.categoryFolders() {
		if strings.EqualFold(f, filepath.Base(parent)) {
			return true
		}
	}
	return false
}

// assignTorrentDir chooses the working folder of a torrent for the built-in
// client when it starts, files it under the category of its kind, and remembers
// the choice: a paused download comes back to the same folder even if the
// categories change meanwhile.
func (s *Server) assignTorrentDir(queueID int64) string {
	if s.TorrentOpts == nil {
		return s.workDirFor(queueID)
	}
	o, _, err := s.TorrentOpts.Get(queueID)
	if err != nil || o.DirDecided {
		return s.workDirFor(queueID)
	}
	name, sub := "", ""
	plain := filepath.Join(s.downloadsIncompleteDir(), fmt.Sprintf("queue-%d", queueID))
	if !fileExists(plain) { // files an earlier version left stay where they are
		if item, err := s.QueueRepo.Get(queueID); err == nil {
			if cat, ok := s.torrentCategoryConfig().For(queueKind(item)); ok {
				name, sub = cat.Name, cat.Subfolder
			}
		}
	}
	if err := s.TorrentOpts.SetCategory(queueID, name, sub); err != nil {
		slog.Warn("torrent: remember the category", "queue", queueID, "err", err)
	}
	return s.workDirFor(queueID)
}

// torrentSeedGoal is when a finished torrent stops seeding: the global limits,
// except where the indexer it came from has rules of its own.
func (s *Server) torrentSeedGoal(queueID int64) torrentclient.SeedGoal {
	ratio, hours := s.torrentSeedRatioLimit(), s.torrentSeedTimeLimit().Hours()
	if s.TorrentOpts != nil {
		if item, err := s.QueueRepo.Get(queueID); err == nil && item.IndexerID.Valid {
			if rule, err := s.TorrentOpts.SeedRule(item.IndexerID.Int64); err == nil {
				ratio, hours = rule.Goal(ratio, hours)
			}
		}
	}
	return torrentclient.SeedGoal{Ratio: ratio, Time: time.Duration(hours * float64(time.Hour))}
}

// setUpDownloadControl gives a torrent that has just been added the choices
// made for it: the files left out (by the rules the first time, then as they
// were left), the reading order and the speed limits.
func (s *Server) setUpDownloadControl(queueID int64, ctl *torrentclient.Control) {
	if s.TorrentOpts == nil {
		return
	}
	o, _, err := s.TorrentOpts.Get(queueID)
	if err != nil {
		slog.Warn("torrent: read the options", "queue", queueID, "err", err)
	}
	if !o.FilesDecided {
		var skip []string
		if item, err := s.QueueRepo.Get(queueID); err == nil {
			skip = s.autoSkips(item, ctl.Files())
		}
		if err := ctl.SetSkipped(skip); err != nil {
			skip = nil
		}
		if err := s.TorrentOpts.SetSkipped(queueID, skip); err != nil {
			slog.Warn("torrent: remember the files left out", "queue", queueID, "err", err)
		}
	} else if err := ctl.SetSkipped(o.SkipFiles); err != nil {
		_ = ctl.SetSkipped(nil)
	}
	ctl.SetSequential(effectiveSequential(o, s.torrentSequentialDefault()))
	ctl.SetLimits(int64(o.DownKB)<<10, int64(o.UpKB)<<10)
}

func effectiveSequential(o torrentopts.Options, global bool) bool {
	switch o.Sequential {
	case torrentopts.On:
		return true
	case torrentopts.Off:
		return false
	}
	return global
}

// ----- the global options -----

type portMapPayload struct {
	Enabled    bool   `json:"enabled"`
	State      string `json:"state"` // off, tunnel, idle, working, mapped or none
	Method     string `json:"method,omitempty"`
	Port       int    `json:"port,omitempty"`
	ExternalIP string `json:"externalIp,omitempty"`
}

type torrentOptionsPayload struct {
	SequentialDefault bool                   `json:"sequentialDefault"`
	SkipJunk          bool                   `json:"skipJunk"`
	PackOnlyWanted    bool                   `json:"packOnlyWanted"`
	UploadLimitKB     int                    `json:"uploadLimitKB"`
	PortMapping       portMapPayload         `json:"portMapping"`
	AllowLocal        bool                   `json:"allowLocalAddresses"`
	Categories        []torrentopts.Category `json:"categories"`
	CategoryByKind    map[string]string      `json:"categoryByKind"`
}

func (s *Server) portMapStatus() portMapPayload {
	p := portMapPayload{Enabled: s.torrentPortMapping()}
	if !p.Enabled {
		p.State = "off"
		return p
	}
	// Through a VPN or a proxy nothing listens on the home connection, so there
	// is nothing to open.
	if tunnel, err := s.torrentTunnel(); err != nil || tunnel != nil {
		p.State = "tunnel"
		return p
	}
	st := s.torrents.portMapStatus()
	p.State, p.Method, p.Port, p.ExternalIP = st.State, st.Method, st.Port, st.ExternalIP
	return p
}

func (s *Server) currentTorrentOptions() torrentOptionsPayload {
	cfg := s.torrentCategoryConfig()
	out := torrentOptionsPayload{
		SequentialDefault: s.torrentSequentialDefault(),
		SkipJunk:          s.torrentSkipJunk(),
		PackOnlyWanted:    s.torrentPackOnlyWanted(),
		UploadLimitKB:     s.torrentUploadLimitKB(),
		PortMapping:       s.portMapStatus(),
		AllowLocal:        s.torrentAllowLocal(),
		Categories:        cfg.Categories,
		CategoryByKind:    cfg.ByKind,
	}
	if out.Categories == nil {
		out.Categories = []torrentopts.Category{}
	}
	return out
}

func (s *Server) handleGetTorrentOptions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentTorrentOptions())
}

func boolSetting(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

func (s *Server) handlePutTorrentOptions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SequentialDefault *bool                   `json:"sequentialDefault"`
		SkipJunk          *bool                   `json:"skipJunk"`
		PackOnlyWanted    *bool                   `json:"packOnlyWanted"`
		UploadLimitKB     *int                    `json:"uploadLimitKB"`
		PortMapping       *bool                   `json:"portMapping"`
		AllowLocal        *bool                   `json:"allowLocalAddresses"`
		Categories        *[]torrentopts.Category `json:"categories"`
		CategoryByKind    *map[string]string      `json:"categoryByKind"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	if req.UploadLimitKB != nil && (*req.UploadLimitKB < 0 || *req.UploadLimitKB > maxTorrentLimitKB) {
		writeError(w, http.StatusBadRequest, "The upload limit is 0 (no limit) to 10000000 KB/s.")
		return
	}
	var cats torrentopts.CategoryConfig
	if req.Categories != nil || req.CategoryByKind != nil {
		cats = s.torrentCategoryConfig()
		if req.Categories != nil {
			cats.Categories = *req.Categories
		}
		if req.CategoryByKind != nil {
			cats.ByKind = *req.CategoryByKind
		}
		var err error
		if cats, err = cats.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "Categories: "+err.Error()+".")
			return
		}
	}

	set := func(key, value string) bool {
		if err := s.Settings.Set(key, value, false); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return false
		}
		return true
	}
	if req.SequentialDefault != nil && !set(settings.KeyTorrentSequential, boolSetting(*req.SequentialDefault)) {
		return
	}
	if req.SkipJunk != nil && !set(settings.KeyTorrentSkipJunk, boolSetting(*req.SkipJunk)) {
		return
	}
	if req.PackOnlyWanted != nil && !set(settings.KeyTorrentPackOnlyWanted, boolSetting(*req.PackOnlyWanted)) {
		return
	}
	if req.UploadLimitKB != nil {
		if !set(settings.KeyTorrentUploadLimitKB, strconv.Itoa(*req.UploadLimitKB)) {
			return
		}
		s.applySpeedLimit(time.Now())
	}
	if req.PortMapping != nil {
		if !set(settings.KeyTorrentPortMapping, boolSetting(*req.PortMapping)) {
			return
		}
		s.torrents.setPortMapping(*req.PortMapping)
	}
	if req.AllowLocal != nil {
		if !set(settings.KeyTorrentAllowLocal, boolSetting(*req.AllowLocal)) {
			return
		}
		s.torrents.setAllowLocal(*req.AllowLocal)
	}
	if req.Categories != nil || req.CategoryByKind != nil {
		list, _ := json.Marshal(append([]torrentopts.Category{}, cats.Categories...))
		byKind, _ := json.Marshal(cats.ByKind)
		if !set(settings.KeyTorrentCategories, string(list)) || !set(settings.KeyTorrentCategoryMap, string(byKind)) {
			return
		}
	}
	writeJSON(w, http.StatusOK, s.currentTorrentOptions())
}

// ----- one torrent -----

type torrentDetailPayload struct {
	// Live is true while the built-in client runs this torrent: only then are
	// its files known and can be changed.
	Live              bool                      `json:"live"`
	Files             []torrentclient.FileState `json:"files"`
	Sequential        string                    `json:"sequential"` // default, on or off
	SequentialDefault bool                      `json:"sequentialDefault"`
	DownLimitKB       int                       `json:"downLimitKB"`
	UpLimitKB         int                       `json:"upLimitKB"`
	Category          string                    `json:"category,omitempty"`
	// FilesOpen is true while the download is running, when files can still be
	// ticked on and off.
	FilesOpen bool `json:"filesOpen"`
}

func sequentialWord(v int) string {
	switch v {
	case torrentopts.On:
		return "on"
	case torrentopts.Off:
		return "off"
	}
	return "default"
}

// torrentItem finds the torrent download a request is about.
func (s *Server) torrentItem(w http.ResponseWriter, r *http.Request) (queue.Item, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid download ID.")
		return queue.Item{}, false
	}
	item, err := s.QueueRepo.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "That download is no longer in the queue.")
		return queue.Item{}, false
	}
	if item.Protocol != queue.ProtocolTorrent {
		writeError(w, http.StatusConflict, "This isn't a torrent download.")
		return queue.Item{}, false
	}
	return item, true
}

func (s *Server) torrentDetail(item queue.Item) (torrentDetailPayload, error) {
	o, _, err := s.TorrentOpts.Get(item.ID)
	if err != nil {
		return torrentDetailPayload{}, err
	}
	out := torrentDetailPayload{
		Files:             []torrentclient.FileState{},
		Sequential:        sequentialWord(o.Sequential),
		SequentialDefault: s.torrentSequentialDefault(),
		DownLimitKB:       o.DownKB,
		UpLimitKB:         o.UpKB,
		Category:          o.Category,
	}
	if ctl := s.torrents.control(item.ID); ctl != nil {
		out.Live = true
		out.Files = ctl.Files()
		out.FilesOpen = item.Status == queue.StatusDownloading
	}
	return out, nil
}

func (s *Server) handleGetTorrentDetail(w http.ResponseWriter, r *http.Request) {
	item, ok := s.torrentItem(w, r)
	if !ok {
		return
	}
	out, err := s.torrentDetail(item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutTorrentDetail(w http.ResponseWriter, r *http.Request) {
	item, ok := s.torrentItem(w, r)
	if !ok {
		return
	}
	var req struct {
		Sequential  *string `json:"sequential"`
		DownLimitKB *int    `json:"downLimitKB"`
		UpLimitKB   *int    `json:"upLimitKB"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	o, _, err := s.TorrentOpts.Get(item.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Sequential != nil {
		switch *req.Sequential {
		case "default":
			o.Sequential = torrentopts.Follow
		case "on":
			o.Sequential = torrentopts.On
		case "off":
			o.Sequential = torrentopts.Off
		default:
			writeError(w, http.StatusBadRequest, `Choose "default", "on" or "off".`)
			return
		}
		if err := s.TorrentOpts.SetSequential(item.ID, o.Sequential); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.DownLimitKB != nil || req.UpLimitKB != nil {
		if req.DownLimitKB != nil {
			o.DownKB = *req.DownLimitKB
		}
		if req.UpLimitKB != nil {
			o.UpKB = *req.UpLimitKB
		}
		if o.DownKB < 0 || o.DownKB > maxTorrentLimitKB || o.UpKB < 0 || o.UpKB > maxTorrentLimitKB {
			writeError(w, http.StatusBadRequest, "A speed limit is 0 (no limit) to 10000000 KB/s.")
			return
		}
		if err := s.TorrentOpts.SetLimits(item.ID, o.DownKB, o.UpKB); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if ctl := s.torrents.control(item.ID); ctl != nil {
		ctl.SetSequential(effectiveSequential(o, s.torrentSequentialDefault()))
		ctl.SetLimits(int64(o.DownKB)<<10, int64(o.UpKB)<<10)
	}
	out, err := s.torrentDetail(item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutTorrentFiles(w http.ResponseWriter, r *http.Request) {
	item, ok := s.torrentItem(w, r)
	if !ok {
		return
	}
	var req struct {
		Skip []string `json:"skip"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	ctl := s.torrents.control(item.ID)
	if ctl == nil || item.Status != queue.StatusDownloading {
		writeError(w, http.StatusConflict, "Files can be chosen while the built-in client is downloading this torrent.")
		return
	}
	if err := ctl.SetSkipped(req.Skip); err != nil {
		if errors.Is(err, torrentclient.ErrNoFileWanted) {
			writeError(w, http.StatusBadRequest, "Keep at least one file ticked.")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.TorrentOpts.SetSkipped(item.ID, ctl.Skipped()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := s.torrentDetail(item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ----- seeding rules of an indexer -----

func (s *Server) handleListSeedRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.TorrentOpts.SeedRules()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make(map[string]torrentopts.SeedRule, len(rules))
	for id, rule := range rules {
		out[strconv.FormatInt(id, 10)] = rule
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutSeedRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid indexer ID.")
		return
	}
	var rule torrentopts.SeedRule
	if err := decodeJSON(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	if err := rule.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, capitalFirst(err.Error())+".")
		return
	}
	if _, err := s.IndexerRepo.Get(id); err != nil {
		if errors.Is(err, indexers.ErrNotFound) {
			writeError(w, http.StatusNotFound, "That indexer no longer exists.")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.TorrentOpts.SetSeedRule(id, rule); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func capitalFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
