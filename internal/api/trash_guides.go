package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/netguard"
	"github.com/rdborg/mediarium/internal/quality"
)

// The TRaSH Guides publish their custom formats and quality profiles as JSON
// in their GitHub repository (MIT licence). Mediarium reads them from there
// when someone opens the import picker: one GitHub API call lists a folder,
// and the files themselves come from raw.githubusercontent.com, which has no
// rate limit. What was read is kept for six hours.

const (
	trashAPIBase = "https://api.github.com/repos/TRaSH-Guides/Guides/contents/docs/json"
	trashRawBase = "https://raw.githubusercontent.com/TRaSH-Guides/Guides/master/docs/json"
	trashKeep    = 6 * time.Hour
)

type trashSource struct {
	apiBase, rawBase string
	client           *http.Client

	mu    sync.Mutex
	cache map[string]trashFolder // "radarr/cf", "sonarr/quality-profiles"…
	busy  map[string]*sync.Mutex // one download per folder at a time
}

type trashFolder struct {
	files   map[string][]byte // file name -> contents
	fetched time.Time
}

func newTrashSource(apiBase, rawBase string) *trashSource {
	return &trashSource{
		apiBase: strings.TrimRight(apiBase, "/"),
		rawBase: strings.TrimRight(rawBase, "/"),
		client:  netguard.Client(30 * time.Second),
		cache:   map[string]trashFolder{},
		busy:    map[string]*sync.Mutex{},
	}
}

// SetTrashGuidesSource points the TRaSH Guides import somewhere else (tests).
func (s *Server) SetTrashGuidesSource(apiBase, rawBase string) {
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	s.trash = newTrashSource(apiBase, rawBase)
}

func (s *Server) trashGuides() *trashSource {
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	if s.trash == nil {
		s.trash = newTrashSource(trashAPIBase, trashRawBase)
	}
	return s.trash
}

// folder returns every JSON file in docs/json/<app>/<kind>, from the cache
// when it is fresh. When GitHub can't be reached, an older copy is used if
// there is one.
func (t *trashSource) folder(ctx context.Context, app quality.FormatApp, kind string) (map[string][]byte, error) {
	key := string(app) + "/" + kind
	t.mu.Lock()
	lock, ok := t.busy[key]
	if !ok {
		lock = &sync.Mutex{}
		t.busy[key] = lock
	}
	t.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()

	t.mu.Lock()
	cached, have := t.cache[key]
	t.mu.Unlock()
	if have && time.Since(cached.fetched) < trashKeep {
		return cached.files, nil
	}
	files, err := t.download(ctx, key)
	if err != nil {
		if have {
			return cached.files, nil
		}
		return nil, err
	}
	t.mu.Lock()
	t.cache[key] = trashFolder{files: files, fetched: time.Now()}
	t.mu.Unlock()
	return files, nil
}

func (t *trashSource) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mediarium")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, netguard.CleanError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("GitHub is limiting requests from this server for now; try again in an hour")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered with status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (t *trashSource) download(ctx context.Context, key string) (map[string][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	body, err := t.get(ctx, t.apiBase+"/"+key, 4<<20)
	if err != nil {
		return nil, fmt.Errorf("couldn't read the TRaSH Guides list: %w", err)
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("couldn't read the TRaSH Guides list: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.Type == "file" && strings.HasSuffix(e.Name, ".json") && !strings.ContainsAny(e.Name, "/\\?#") {
			names = append(names, e.Name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("the TRaSH Guides list for %s is empty", key)
	}

	files := map[string][]byte{}
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	work := make(chan string)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range work {
				b, err := t.get(ctx, t.rawBase+"/"+key+"/"+name, 1<<20)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("couldn't read %s from the TRaSH Guides: %w", name, err)
				} else if err == nil {
					files[name] = b
				}
				mu.Unlock()
			}
		}()
	}
	for _, n := range names {
		work <- n
	}
	close(work)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return files, nil
}

// trashFormats reads every TRaSH custom format for an app, keyed by TRaSH id.
func (s *Server) trashFormats(ctx context.Context, app quality.FormatApp) (map[string]quality.ImportedFormat, error) {
	files, err := s.trashGuides().folder(ctx, app, "cf")
	if err != nil {
		return nil, err
	}
	out := map[string]quality.ImportedFormat{}
	for _, b := range files {
		fs, err := quality.ParseFormats(b, app)
		if err != nil || len(fs) != 1 || fs[0].Format.TrashID == "" {
			continue // one odd file shouldn't hide the rest
		}
		out[fs[0].Format.TrashID] = fs[0]
	}
	return out, nil
}

// trashProfiles reads every TRaSH quality profile for an app.
func (s *Server) trashProfiles(ctx context.Context, app quality.FormatApp) ([]quality.TrashProfile, error) {
	files, err := s.trashGuides().folder(ctx, app, "quality-profiles")
	if err != nil {
		return nil, err
	}
	var out []quality.TrashProfile
	for _, b := range files {
		tp, err := quality.ParseTrashProfile(b)
		if err != nil || tp.TrashID == "" {
			continue
		}
		if _, _, err := tp.Tiers(); err != nil {
			continue
		}
		out = append(out, tp)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func trashApp(w http.ResponseWriter, r *http.Request) (quality.FormatApp, bool) {
	switch app := quality.FormatApp(r.PathValue("app")); app {
	case quality.AppRadarr, quality.AppSonarr:
		return app, true
	}
	writeError(w, http.StatusBadRequest, `Pick "radarr" (movies) or "sonarr" (TV).`)
	return "", false
}

type trashFormatPayload struct {
	TrashID      string `json:"trashId"`
	Name         string `json:"name"`
	DefaultScore int    `json:"defaultScore"`
	// ImportedID is the id of the format already imported from it (0: none).
	ImportedID int64 `json:"importedId"`
}

type trashProfilePayload struct {
	TrashID           string   `json:"trashId"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Qualities         []string `json:"qualities"`
	Cutoff            string   `json:"cutoff"`
	Formats           int      `json:"formats"`
	MinFormatScore    int      `json:"minFormatScore"`
	CutoffFormatScore int      `json:"cutoffFormatScore"`
}

// handleTrashList lists the TRaSH custom formats and quality profiles for
// one app.
func (s *Server) handleTrashList(w http.ResponseWriter, r *http.Request) {
	app, ok := trashApp(w, r)
	if !ok {
		return
	}
	formats, err := s.trashFormats(r.Context(), app)
	if err != nil {
		writeError(w, http.StatusBadGateway, sentence(err.Error()))
		return
	}
	profiles, err := s.trashProfiles(r.Context(), app)
	if err != nil {
		writeError(w, http.StatusBadGateway, sentence(err.Error()))
		return
	}
	imported, err := s.importedTrashIDs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fl := make([]trashFormatPayload, 0, len(formats))
	for id, f := range formats {
		fl = append(fl, trashFormatPayload{TrashID: id, Name: f.Format.Name, DefaultScore: f.DefaultScore, ImportedID: imported[id]})
	}
	sort.Slice(fl, func(i, j int) bool { return strings.ToLower(fl[i].Name) < strings.ToLower(fl[j].Name) })
	pl := make([]trashProfilePayload, 0, len(profiles))
	for _, tp := range profiles {
		allowed, cutoff, _ := tp.Tiers()
		qs := make([]string, len(allowed))
		for i, t := range allowed {
			qs[i] = string(t)
		}
		pl = append(pl, trashProfilePayload{TrashID: tp.TrashID, Name: tp.Name, Description: plainTrashText(tp.Description),
			Qualities: qs, Cutoff: string(cutoff), Formats: len(tp.FormatItems), MinFormatScore: tp.MinFormatScore, CutoffFormatScore: tp.CutoffFormatScore})
	}
	writeJSON(w, http.StatusOK, map[string]any{"formats": fl, "profiles": pl})
}

// plainTrashText turns TRaSH's descriptions ("covers:<br>- WEBDL: 1080p")
// into plain lines.
func plainTrashText(s string) string {
	s = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "<b>", "", "</b>", "").Replace(s)
	return strings.TrimSpace(s)
}

// sentence starts a message with a capital letter and ends it with a full stop.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func (s *Server) importedTrashIDs() (map[string]int64, error) {
	list, err := s.FormatRepo.List()
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, f := range list {
		if f.TrashID != "" {
			out[f.TrashID] = f.ID
		}
	}
	return out, nil
}

type trashImportRequest struct {
	TrashIDs []string `json:"trashIds"`
	// ProfileID, when set, gives each imported format TRaSH's default score
	// in that quality profile.
	ProfileID int64 `json:"profileId"`
}

// handleTrashImportFormats imports chosen TRaSH custom formats (again, to
// update them).
func (s *Server) handleTrashImportFormats(w http.ResponseWriter, r *http.Request) {
	app, ok := trashApp(w, r)
	if !ok {
		return
	}
	var req trashImportRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	if len(req.TrashIDs) == 0 {
		writeError(w, http.StatusBadRequest, "Pick at least one custom format.")
		return
	}
	var profile quality.Profile
	if req.ProfileID != 0 {
		p, err := s.QualityRepo.Get(req.ProfileID)
		if err != nil {
			writeProfileError(w, err)
			return
		}
		profile = p
	}
	all, err := s.trashFormats(r.Context(), app)
	if err != nil {
		writeError(w, http.StatusBadGateway, sentence(err.Error()))
		return
	}
	res := formatImportResult{Formats: []customFormatPayload{}}
	scores := map[int64]int{}
	for _, id := range req.TrashIDs {
		f, ok := all[id]
		if !ok {
			res.Missing = append(res.Missing, id)
			continue
		}
		saved, err := s.saveFormat(f.Format, &res)
		if err != nil {
			writeFormatError(w, err)
			return
		}
		if f.DefaultScore != 0 {
			scores[saved.ID] = f.DefaultScore
		}
	}
	if req.ProfileID != 0 && len(scores) > 0 {
		if profile.FormatScores == nil {
			profile.FormatScores = map[int64]int{}
		}
		for id, sc := range scores {
			profile.FormatScores[id] = sc
		}
		if _, err := s.QualityRepo.Update(profile); err != nil {
			writeProfileError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, res)
}

type trashProfileImportRequest struct {
	TrashID string `json:"trashId"`
}

// handleTrashImportProfile makes a Mediarium quality profile from a TRaSH
// one: its qualities and cutoff, its custom formats with TRaSH's scores, and
// its minimum and upgrade-until scores.
func (s *Server) handleTrashImportProfile(w http.ResponseWriter, r *http.Request) {
	app, ok := trashApp(w, r)
	if !ok {
		return
	}
	var req trashProfileImportRequest
	if err := decodeJSON(r, &req); err != nil || req.TrashID == "" {
		writeError(w, http.StatusBadRequest, "Pick a TRaSH quality profile.")
		return
	}
	profiles, err := s.trashProfiles(r.Context(), app)
	if err != nil {
		writeError(w, http.StatusBadGateway, sentence(err.Error()))
		return
	}
	var tp *quality.TrashProfile
	for i := range profiles {
		if profiles[i].TrashID == req.TrashID {
			tp = &profiles[i]
		}
	}
	if tp == nil {
		writeError(w, http.StatusNotFound, "That TRaSH quality profile wasn't found. Reload the list and try again.")
		return
	}
	all, err := s.trashFormats(r.Context(), app)
	if err != nil {
		writeError(w, http.StatusBadGateway, sentence(err.Error()))
		return
	}
	allowed, cutoff, err := tp.Tiers()
	if err != nil {
		writeProfileError(w, err)
		return
	}
	name, err := s.freeProfileName(tp.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res := formatImportResult{Formats: []customFormatPayload{}}
	p := quality.Profile{Name: name, Allowed: allowed, Cutoff: cutoff, UpgradeAllowed: tp.UpgradeAllowed,
		MinFormatScore: tp.MinFormatScore, CutoffFormatScore: max(tp.CutoffFormatScore, 0), FormatScores: map[int64]int{}, SkipBadReleases: true}
	for _, id := range tp.FormatTrashIDs() {
		f, ok := all[id]
		if !ok {
			res.Missing = append(res.Missing, id)
			continue
		}
		saved, err := s.saveFormat(f.Format, &res)
		if err != nil {
			writeFormatError(w, err)
			return
		}
		if sc := tp.ScoreFor(f); sc != 0 {
			p.FormatScores[saved.ID] = sc
		}
	}
	created, err := s.QualityRepo.Create(p)
	if err != nil {
		writeProfileError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"profile": toProfilePayload(created, 0), "formats": res})
}

// freeProfileName is name, or name with " (2)", " (3)"… when a profile with
// that name exists.
func (s *Server) freeProfileName(name string) (string, error) {
	list, err := s.QualityRepo.List()
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, p := range list {
		taken[strings.ToLower(p.Name)] = true
	}
	name = strings.TrimSpace(name)
	if len(name) > 56 {
		name = strings.TrimSpace(name[:56])
	}
	try := name
	for n := 2; taken[strings.ToLower(try)]; n++ {
		try = fmt.Sprintf("%s (%d)", name, n)
	}
	return try, nil
}
