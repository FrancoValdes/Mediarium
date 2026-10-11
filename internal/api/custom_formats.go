package api

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/rdborg/mediarium/internal/quality"
	"github.com/rdborg/mediarium/internal/settings"
)

// Custom formats: conditions on a release name (patterns, source,
// resolution, language, size…) that quality profiles give a score to. They
// are imported from Radarr/Sonarr JSON or from the TRaSH Guides (trash.go).

type customFormatPayload struct {
	ID         int64                `json:"id"`
	Name       string               `json:"name"`
	App        string               `json:"app"`
	TrashID    string               `json:"trashId,omitempty"`
	Conditions []quality.FormatSpec `json:"conditions"`
	// Problems lists conditions that can't work (a pattern that doesn't
	// compile, indexer flags Mediarium doesn't get).
	Problems []string `json:"problems,omitempty"`
	// UsedBy names the quality profiles that give it a score.
	UsedBy []string `json:"usedBy"`
}

func toCustomFormatPayload(f quality.CustomFormat, usedBy []string) customFormatPayload {
	if usedBy == nil {
		usedBy = []string{}
	}
	specs := f.Specs
	if specs == nil {
		specs = []quality.FormatSpec{}
	}
	return customFormatPayload{ID: f.ID, Name: f.Name, App: string(f.App), TrashID: f.TrashID, Conditions: specs, Problems: f.Problems(), UsedBy: usedBy}
}

// showAdvanced reports whether the Quality page shows custom formats and
// format scores: as set, or, until it's set, whether any format exists (so
// people who already use them keep seeing them).
func (s *Server) showAdvanced() bool {
	if v, _ := s.Settings.Get(settings.KeyShowAdvanced); v != "" {
		return v == "1"
	}
	list, err := s.FormatRepo.List()
	return err == nil && len(list) > 0
}

// formatUsage is the names of the profiles scoring each format.
func (s *Server) formatUsage() (map[int64][]string, error) {
	profiles, err := s.QualityRepo.List()
	if err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for _, p := range profiles {
		for id := range p.FormatScores {
			out[id] = append(out[id], p.Name)
		}
	}
	for id := range out {
		sort.Strings(out[id])
	}
	return out, nil
}

func (s *Server) handleListCustomFormats(w http.ResponseWriter, r *http.Request) {
	list, err := s.FormatRepo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	usage, err := s.formatUsage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]customFormatPayload, 0, len(list))
	for _, f := range list {
		out = append(out, toCustomFormatPayload(f, usage[f.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"formats": out})
}

type formatImportResult struct {
	Added   int                   `json:"added"`
	Updated int                   `json:"updated"`
	Formats []customFormatPayload `json:"formats"`
	// Missing lists TRaSH ids that weren't found (the guides changed).
	Missing []string `json:"missing,omitempty"`
}

func (s *Server) saveFormat(f quality.CustomFormat, res *formatImportResult) (quality.CustomFormat, error) {
	saved, isNew, err := s.FormatRepo.Save(f)
	if err != nil {
		return saved, err
	}
	if isNew {
		res.Added++
	} else {
		res.Updated++
	}
	res.Formats = append(res.Formats, toCustomFormatPayload(saved, nil))
	return saved, nil
}

func writeFormatError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, quality.ErrFormatName):
		writeError(w, http.StatusConflict, "A custom format with that name already exists.")
	case errors.Is(err, quality.ErrInvalid):
		writeError(w, http.StatusBadRequest, problemText(err, quality.ErrInvalid))
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "That custom format no longer exists.")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

type formatPasteRequest struct {
	// JSON is one format or a list, as Radarr's or Sonarr's export button,
	// their API or the TRaSH Guides write it.
	JSON string `json:"json"`
	// App is whose numbering the conditions use: "radarr" or "sonarr" (""
	// guesses).
	App string `json:"app"`
}

// handleImportCustomFormats imports pasted JSON. A format with the same name
// (or TRaSH id) as one already saved replaces it.
func (s *Server) handleImportCustomFormats(w http.ResponseWriter, r *http.Request) {
	var req formatPasteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	app := quality.FormatApp(req.App)
	if app != "" && app != quality.AppRadarr && app != quality.AppSonarr {
		writeError(w, http.StatusBadRequest, `Pick "radarr" (movies) or "sonarr" (TV).`)
		return
	}
	if strings.TrimSpace(req.JSON) == "" {
		writeError(w, http.StatusBadRequest, "Paste the JSON of one or more custom formats.")
		return
	}
	parsed, err := quality.ParseFormats([]byte(req.JSON), app)
	if err != nil {
		writeError(w, http.StatusBadRequest, sentence(err.Error()))
		return
	}
	res := formatImportResult{Formats: []customFormatPayload{}}
	for _, f := range parsed {
		if _, err := s.saveFormat(f.Format, &res); err != nil {
			writeFormatError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, res)
}

func formatID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "That isn't a valid custom format ID.")
		return 0, false
	}
	return id, true
}

type formatRenameRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleRenameCustomFormat(w http.ResponseWriter, r *http.Request) {
	id, ok := formatID(w, r)
	if !ok {
		return
	}
	var req formatRenameRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	if err := s.FormatRepo.Rename(id, req.Name); err != nil {
		writeFormatError(w, err)
		return
	}
	f, err := s.FormatRepo.Get(id)
	if err != nil {
		writeFormatError(w, err)
		return
	}
	usage, _ := s.formatUsage()
	writeJSON(w, http.StatusOK, toCustomFormatPayload(f, usage[id]))
}

func (s *Server) handleDeleteCustomFormat(w http.ResponseWriter, r *http.Request) {
	id, ok := formatID(w, r)
	if !ok {
		return
	}
	if err := s.FormatRepo.Delete(id); err != nil {
		writeFormatError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

type formatTestRequest struct {
	Title string `json:"title"`
	// ProfileID, when set, also gives the profile's score and whether it
	// would take the release.
	ProfileID int64 `json:"profileId"`
}

type formatTestMatch struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Score *int   `json:"score,omitempty"`
}

// handleTestCustomFormats says which formats a release name matches.
func (s *Server) handleTestCustomFormats(w http.ResponseWriter, r *http.Request) {
	var req formatTestRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "Type a release name to test.")
		return
	}
	list, err := s.FormatRepo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var profile *quality.Profile
	if req.ProfileID != 0 {
		profiles, err := s.loadProfiles()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, ok := profiles.byID[req.ProfileID]; !ok {
			writeError(w, http.StatusNotFound, "That quality profile no longer exists.")
			return
		}
		p := profiles.resolve(req.ProfileID)
		profile = &p
	}
	in := quality.InputFor(req.Title)
	matched := []formatTestMatch{}
	for i := range list {
		if !list[i].Matches(in) {
			continue
		}
		m := formatTestMatch{ID: list[i].ID, Name: list[i].Name}
		if profile != nil {
			sc := profile.FormatScores[list[i].ID]
			m.Score = &sc
		}
		matched = append(matched, m)
	}
	out := map[string]any{"matched": matched, "quality": string(quality.Classify(in.Release))}
	if profile != nil {
		score, _ := profile.FormatScore(req.Title)
		ok, why := profile.TitleAllowed(req.Title)
		if ok && !profile.AllowsTier(quality.Classify(in.Release)) {
			ok, why = false, string(quality.Classify(in.Release))+" isn't one of the profile's qualities"
		}
		out["score"] = score
		out["accepted"] = ok
		if why != "" {
			out["reason"] = sentence(why)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
