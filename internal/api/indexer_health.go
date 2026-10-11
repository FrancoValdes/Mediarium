package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/rdborg/mediarium/internal/indexers"
)

// What the indexer list, Health and the Statistics page say about indexers
// that keep failing (the back-off in internal/indexers/backoff.go) and how
// each one has been doing.

// addPauseInfo fills in the pause fields of an indexer's payload while its
// searches are being skipped.
func addPauseInfo(p *indexerPayload, inst indexers.Instance) {
	h, ok := inst.Tracker.Health(inst.ID)
	if !ok {
		return
	}
	p.Failures = h.Failures
	if h.Paused(time.Now()) {
		p.PausedUntil = h.PausedUntil.UTC().Format(time.RFC3339)
		p.PausedNote = indexers.PausedMessage(inst.Name, h, nil)
		p.PausedWhy = h.Reason
	}
}

// handleResumeIndexer is "Try again now": the indexer is asked by the next
// search, and its failures are forgotten.
func (s *Server) handleResumeIndexer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid indexer ID.")
		return
	}
	if _, err := s.IndexerRepo.Get(id); errors.Is(err, indexers.ErrNotFound) {
		writeError(w, http.StatusNotFound, "That indexer no longer exists.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.IndexerRepo.Tracker().Resume(id)
	writeJSON(w, http.StatusOK, nil)
}

// pausedIndexerProblems adds a Health line for every enabled indexer that is
// being skipped.
func (s *Server) pausedIndexerProblems(instances []indexers.Instance, add func(id, level, title, detail, action, link string)) {
	now := time.Now()
	for _, inst := range instances {
		if !inst.Enabled {
			continue
		}
		h, ok := inst.Tracker.Health(inst.ID)
		if !ok || !h.Paused(now) {
			continue
		}
		add(fmt.Sprintf("indexer-paused-%d", inst.ID), "warn", inst.Name+" is paused",
			indexers.PausedMessage(inst.Name, h, nil)+" Searches skip it until then. Press Try again now once it is fixed.",
			"Open indexers", "/settings/indexers")
	}
}

// indexerStatPayload is one indexer's line on the Statistics page.
type indexerStatPayload struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Protocol    string  `json:"protocol"`
	Enabled     bool    `json:"enabled"`
	Queries     int     `json:"queries"`
	Failures    int     `json:"failures"`
	Results     int     `json:"results"`
	Grabs       int     `json:"grabs"`
	SuccessRate float64 `json:"successRate"` // 0 to 1, -1 when it was not searched
	PausedUntil string  `json:"pausedUntil,omitempty"`
}

type indexerStatsPayload struct {
	Days     int                  `json:"days"`
	Indexers []indexerStatPayload `json:"indexers"`
}

// statsDays is the period the Statistics page adds up.
const indexerStatsDays = 30

// handleIndexerStats gives each indexer's searches, failures, releases found
// and grabs over the last 30 days.
func (s *Server) handleIndexerStats(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	tr := s.IndexerRepo.Tracker()
	stats, err := tr.Stats(now.AddDate(0, 0, -(indexerStatsDays - 1)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	list, err := s.IndexerRepo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	byID := map[int64]indexers.Instance{}
	for _, inst := range list {
		byID[inst.ID] = inst
	}
	out := indexerStatsPayload{Days: indexerStatsDays, Indexers: []indexerStatPayload{}}
	for _, st := range stats {
		inst := byID[st.IndexerID]
		p := indexerStatPayload{
			ID: st.IndexerID, Name: st.Name, Protocol: string(inst.Protocol), Enabled: inst.Enabled,
			Queries: st.Queries, Failures: st.Failures, Results: st.Results, Grabs: st.Grabs, SuccessRate: st.SuccessRate(),
		}
		if until, paused := tr.PausedUntil(st.IndexerID); paused {
			p.PausedUntil = until.UTC().Format(time.RFC3339)
		}
		out.Indexers = append(out.Indexers, p)
	}
	// The busiest first.
	sort.SliceStable(out.Indexers, func(i, j int) bool { return out.Indexers[i].Queries > out.Indexers[j].Queries })
	writeJSON(w, http.StatusOK, out)
}
