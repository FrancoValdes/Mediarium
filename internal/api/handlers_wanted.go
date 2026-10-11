package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rdborg/mediarium/internal/blocklist"
	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/metadata"
	"github.com/rdborg/mediarium/internal/parser"
	"github.com/rdborg/mediarium/internal/quality"
)

// rejectionsFor explains why automation would not pick a release: quality
// outside the profile, or (for something already downloaded) no better than
// what is on disk. It only informs the interactive search list; grabbing a
// rejected release by hand is always allowed.
// currentRelease is the release the file came from ("" when unknown).
func rejectionsFor(title string, profile quality.Profile, downloaded bool, currentQuality, currentRelease string) []string {
	var out []string
	rel := parser.Parse(title)
	tier := quality.Classify(rel)
	if ok, why := profile.TitleAllowed(title); !ok {
		out = append(out, why)
	}
	if reason := profile.LanguageReason(rel); reason != "" {
		out = append(out, reason)
	}
	if !profile.AllowsTier(tier) {
		out = append(out, fmt.Sprintf("%s isn't allowed by the %q profile", tier, profile.Name))
		if by, fallback, ok := profile.AcceptedBy(title); ok && fallback {
			if downloaded {
				out = append(out, fmt.Sprintf("allowed only as a fallback (%q), which is used only when nothing is downloaded yet", by.Name))
			} else {
				out = append(out, fmt.Sprintf("allowed only as a fallback (%q): taken only when no release the %q profile accepts is found", by.Name, profile.Name))
			}
		}
	}
	if downloaded && tierKnown(currentQuality) && !profile.OnFallback(quality.Tier(currentQuality)) {
		switch {
		case profile.WantsFormatUpgrade(currentRelease) && profile.IsUpgradeOver(quality.Tier(currentQuality), currentRelease, title):
			// same or better quality with a better custom-format score
		case profile.ReachedCutoffTier(quality.Tier(currentQuality)) && profile.WantsFormatUpgrade(currentRelease):
			out = append(out, "doesn't score higher with custom formats than the file you have")
		case profile.ReachedCutoffTier(quality.Tier(currentQuality)):
			out = append(out, fmt.Sprintf("already at the profile cutoff (%s)", currentQuality))
		case quality.Rank(tier) <= quality.Rank(quality.Tier(currentQuality)):
			out = append(out, fmt.Sprintf("not an upgrade over %s", currentQuality))
		}
	}
	return out
}

// setFormatScore adds a release's custom-format score when the profile
// scores formats.
func setFormatScore(p *searchResultPayload, profile quality.Profile, title string) {
	if len(profile.Formats) == 0 {
		return
	}
	score, names := profile.FormatScore(title)
	p.FormatScore = &score
	p.Formats = names
}

// handleMovieSearch is the interactive search for one library movie: every
// matching release across the indexers, with the reasons automation would
// skip it.
func (s *Server) handleMovieSearch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid movie ID.")
		return
	}
	m, err := s.MovieRepo.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "That movie isn't in your library.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	instances, err := s.searchableIndexers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(instances) == 0 {
		writeSearch(w, r, []searchResultPayload{}, 0, nil)
		return
	}
	profiles, err := s.loadProfiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	profile := s.profileForMovie(profiles, m)
	blocked := s.blockedKeys()

	_, beforeHome := s.homeReleaseHold(r.Context(), m, profile)
	sizeReasons := s.movieSizeRejections(r.Context(), m, profile)
	outcomes := indexers.SearchAll(r.Context(), instances, m.Title, movieCategory)
	merged := indexers.MergeResults(outcomes)
	out := []searchResultPayload{}
	for _, res := range merged {
		if !matchesMovie(res.Title, m) {
			continue
		}
		p := s.toSearchResultPayload(res)
		p.Blocklisted = blocked[blocklist.Key(res.Title)]
		p.Rejections = append(p.Rejections, rejectionsFor(res.Title, profile, m.Status == library.StatusDownloaded, m.Quality, m.ReleaseTitle)...)
		p.Rejections = append(p.Rejections, sizeReasons(res.Title, res.SizeBytes)...)
		if beforeHome && looksLikeHomeCopy(res.Title) {
			p.Rejections = append(p.Rejections, cinemaCopyWarning)
		}
		setFormatScore(&p, profile, res.Title)
		p.AcceptedBy = acceptedBy(profile, res.Title)
		out = append(out, p)
	}
	s.markCachedPayloads(r.Context(), out)
	writeSearch(w, r, out, len(instances), outcomes)
}

type searchNowResponse struct {
	Grabbed int    `json:"grabbed"`
	Message string `json:"message"`
}

func (s *Server) hasIndexers(w http.ResponseWriter) ([]indexers.Instance, bool) {
	instances, err := s.searchableIndexers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if len(instances) == 0 {
		writeError(w, http.StatusPreconditionFailed, "There are no indexers yet. Add one in Settings > Indexers & Search.")
		return nil, false
	}
	return instances, true
}

// handleMovieSearchNow runs the automatic search for one movie right now,
// even if it is unmonitored, grabbing the best acceptable release.
func (s *Server) handleMovieSearchNow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid movie ID.")
		return
	}
	m, err := s.MovieRepo.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "That movie isn't in your library.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if m.Status == library.StatusDownloading {
		writeError(w, http.StatusConflict, "This movie is already downloading.")
		return
	}
	instances, ok := s.hasIndexers(w)
	if !ok {
		return
	}
	profiles, err := s.loadProfiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.huntMovie(r.Context(), m, instances, profiles, s.blockedKeys(), true) {
		writeJSON(w, http.StatusOK, searchNowResponse{Grabbed: 1, Message: "Started a download. Follow it in Activity."})
		return
	}
	writeJSON(w, http.StatusOK, searchNowResponse{Message: "Nothing suitable was found right now."})
}

type seriesSearchNowRequest struct {
	Season  *int `json:"season"` // 0 is the specials; leave it out for the whole show
	Episode int  `json:"episode"`
}

// searchNowScope turns a search-now request into what to look for.
func searchNowScope(seriesID int64, req seriesSearchNowRequest) tvScope {
	sc := tvScope{seriesID: seriesID, episode: req.Episode, force: true}
	if req.Season != nil {
		sc.season = *req.Season
		sc.specials = *req.Season == 0
	}
	return sc
}

// handleSeriesSearchNow searches for everything wanted in a series (or one
// season, or one episode) right now, even if unmonitored.
func (s *Server) handleSeriesSearchNow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid show ID.")
		return
	}
	var req seriesSearchNowRequest
	_ = decodeJSON(r, &req) // an empty body means the whole series
	if _, err := s.MovieRepo.GetSeries(id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "That show isn't in your library.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	instances, ok := s.hasIndexers(w)
	if !ok {
		return
	}
	profiles, err := s.loadProfiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n := s.autoGrabTV(s.targetedTVSearcher(r.Context(), instances, maxTVSearchesPerHunt, true), "search-now", profiles,
		searchNowScope(id, req))
	msg := "Nothing suitable was found right now."
	if n > 0 {
		msg = fmt.Sprintf("Started %s. You can follow the progress in Activity.", plural(n, "download"))
	}
	writeJSON(w, http.StatusOK, searchNowResponse{Grabbed: n, Message: msg})
}

type wantedItemPayload struct {
	Kind        string `json:"kind"` // "movie" or "episode"
	ID          int64  `json:"id"`
	MovieID     int64  `json:"movieId,omitempty"`
	TMDBID      int    `json:"tmdbId,omitempty"`
	SeriesID    int64  `json:"seriesId,omitempty"`
	Season      int    `json:"season,omitempty"`
	Episode     int    `json:"episode,omitempty"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle,omitempty"`
	Date        string `json:"date,omitempty"` // release / air date
	Quality     string `json:"quality,omitempty"`
	Cutoff      string `json:"cutoff,omitempty"`
	ProfileName string `json:"profileName,omitempty"`
	Copy        bool   `json:"copy,omitempty"` // a second copy of the title
	Year        int    `json:"year,omitempty"`
	PosterURL   string `json:"posterUrl,omitempty"`
	// The latest automatic search for this title, in plain words, so a
	// title waiting for a release says why ("100 releases, none acceptable: …").
	LastSearch   string `json:"lastSearch,omitempty"`
	LastSearchAt string `json:"lastSearchAt,omitempty"`
}

// wantedPoster is a poster's address, empty when the title has none.
func wantedPoster(posterPath string) string {
	if posterPath == "" {
		return ""
	}
	return metadata.PosterURL(posterPath)
}

// handleWanted lists what automation is still looking for: "missing"
// (monitored, nothing downloaded; episodes only once aired) or "cutoff"
// (downloaded but below the profile cutoff, upgrades allowed).
func (s *Server) handleWanted(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "missing"
	}
	if kind != "missing" && kind != "cutoff" {
		writeError(w, http.StatusBadRequest, `kind must be "missing" or "cutoff"`)
		return
	}
	profiles, err := s.loadProfiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []wantedItemPayload{}

	// Ask the database only for the monitored titles that could be wanted
	// (missing ones, or downloaded ones for the cutoff list), then apply the
	// rules that need the profile or the disk.
	status := library.StatusMissing
	if kind == "cutoff" {
		status = library.StatusDownloaded
	}
	movies, err := s.MovieRepo.ListMonitoredWithStatus(status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var movieIDs []int64
	var movieAt []int // where in out each movie sits
	for _, m := range movies {
		p := profiles.resolve(m.ProfileID)
		switch {
		case kind == "missing" && movieWantedMissing(m),
			kind == "cutoff" && m.Status == library.StatusDownloaded && !m.NoUpgrade && wantsUpgrade(p, m.Quality, m.ReleaseTitle):
			item := wantedItemPayload{Kind: "movie", ID: m.ID, MovieID: m.ID, TMDBID: m.TMDBID, Title: m.Title, Date: m.ReleaseDate, ProfileName: p.Name, Copy: m.CopyOf != 0, Year: m.Year, PosterURL: wantedPoster(m.PosterPath)}
			if item.Copy {
				item.Subtitle = "Second copy"
			}
			if kind == "cutoff" {
				item.Quality, item.Cutoff = m.Quality, string(p.Cutoff)
			}
			movieIDs = append(movieIDs, m.ID)
			movieAt = append(movieAt, len(out))
			out = append(out, item)
		}
	}
	if searches, err := s.QueueRepo.LastMovieSearches(movieIDs); err == nil {
		for i, id := range movieIDs {
			if e, ok := searches[id]; ok {
				out[movieAt[i]].LastSearch, out[movieAt[i]].LastSearchAt = e.Message, e.At
			}
		}
	}

	today := time.Now().UTC().Format("2006-01-02")
	episodes, err := s.MovieRepo.ListWantedEpisodes(status, today)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var seriesIDs []int64
	seenSeries := map[int64]bool{}
	var episodeAt []int
	for _, ep := range episodes {
		p := profiles.resolve(ep.ProfileID)
		if kind == "missing" && fileExists(ep.FilePath) {
			continue // marked missing, but the file is there
		}
		if kind == "cutoff" && !wantsUpgrade(p, ep.Quality, ep.ReleaseTitle) {
			continue
		}
		label := fmt.Sprintf("S%02dE%02d", ep.Season, ep.Episode.Episode)
		if ep.Title != "" {
			label += " · " + ep.Title
		}
		if ep.CopyOf != 0 {
			label = "Second copy · " + label
		}
		item := wantedItemPayload{
			Kind: "episode", ID: ep.ID, SeriesID: ep.SeriesID, Season: ep.Season, Episode: ep.Episode.Episode,
			Title: ep.SeriesTitle, Subtitle: label, Date: ep.AirDate, ProfileName: p.Name, Copy: ep.CopyOf != 0, Year: ep.SeriesYear, PosterURL: wantedPoster(ep.SeriesPoster),
		}
		if kind == "cutoff" {
			item.Quality, item.Cutoff = ep.Quality, string(p.Cutoff)
		}
		if !seenSeries[ep.SeriesID] {
			seenSeries[ep.SeriesID] = true
			seriesIDs = append(seriesIDs, ep.SeriesID)
		}
		episodeAt = append(episodeAt, len(out))
		out = append(out, item)
	}
	if searches, err := s.QueueRepo.LastSeriesSearches(seriesIDs); err == nil {
		for _, at := range episodeAt {
			if e, ok := searches[out[at].SeriesID]; ok {
				out[at].LastSearch, out[at].LastSearchAt = e.Message, e.At
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type monitoredRequest struct {
	Monitored bool `json:"monitored"`
}

// stoppedMonitoringNote is how a removed waiting download is recorded when a
// title stops being monitored.
const stoppedMonitoringNote = "Removed because the title is no longer monitored"

func (s *Server) handleSetMovieMonitored(w http.ResponseWriter, r *http.Request) {
	s.setMonitored(w, r, "movie", func(ctx context.Context, id int64, v bool) error {
		if err := s.MovieRepo.SetMonitored(id, v); err != nil {
			return err
		}
		if !v {
			s.clearWaitingDownloads(id, 0, false, stoppedMonitoringNote)
		}
		return nil
	})
}

func (s *Server) handleSetSeriesMonitored(w http.ResponseWriter, r *http.Request) {
	s.setMonitored(w, r, "series", func(ctx context.Context, id int64, v bool) error {
		if err := s.MovieRepo.SetSeriesMonitored(id, v); err != nil {
			return err
		}
		if !v {
			s.clearWaitingDownloads(0, id, false, stoppedMonitoringNote)
		}
		return nil
	})
}

func (s *Server) handleSetEpisodeMonitored(w http.ResponseWriter, r *http.Request) {
	s.setMonitored(w, r, "episode", func(ctx context.Context, id int64, v bool) error { return s.MovieRepo.SetEpisodeMonitored(id, v) })
}

func (s *Server) handleSetSeasonMonitored(w http.ResponseWriter, r *http.Request) {
	season, err := strconv.Atoi(r.PathValue("season"))
	if err != nil || season < 0 {
		writeError(w, http.StatusBadRequest, "That isn't a valid season number.")
		return
	}
	s.setMonitored(w, r, "series", func(ctx context.Context, id int64, v bool) error {
		return s.MovieRepo.SetSeasonMonitored(id, season, v)
	})
}

func (s *Server) setMonitored(w http.ResponseWriter, r *http.Request, what string, set func(ctx context.Context, id int64, v bool) error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid "+what+" ID.")
		return
	}
	var req monitoredRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	if err := set(r.Context(), id, req.Monitored); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nil)
}
