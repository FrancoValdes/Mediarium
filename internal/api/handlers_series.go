package api

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/metadata"
)

type seriesPayload struct {
	ID              int64    `json:"id"`
	TMDBID          int      `json:"tmdbId"`
	Title           string   `json:"title"`
	Year            int      `json:"year"`
	Overview        string   `json:"overview,omitempty"`
	PosterURL       string   `json:"posterUrl,omitempty"`
	Monitored       bool     `json:"monitored"`
	EpisodeCount    int      `json:"episodeCount"`
	DownloadedCount int      `json:"downloadedCount"`
	ProfileID       int64    `json:"profileId"`
	Sources         string   `json:"sources"`
	Genres          []string `json:"genres"`  // TMDB genre names; empty until fetched
	Tags            []string `json:"tags"`    // the person's own tags ("Kids", "4K")
	AddedBy         *userRef `json:"addedBy"` // null when unknown
	// For sorting the Library: when the show started, and the size of its
	// downloaded episodes on disk.
	FirstAirDate string `json:"firstAirDate,omitempty"`
	SizeBytes    int64  `json:"sizeBytes,omitempty"`
	// NoUpgrade: automation does not look for better versions of the episodes.
	NoUpgrade bool `json:"noUpgrade"`
	// DetailsState is "pending" while an import is still fetching details and
	// "problem" when that failed (DetailsNote says why); empty otherwise.
	DetailsState string `json:"detailsState,omitempty"`
	DetailsNote  string `json:"detailsNote,omitempty"`
	// SeriesType is how releases number the episodes: standard, anime or daily.
	SeriesType string `json:"seriesType"`
	// CopyOf and Copies work as on a movie.
	CopyOf int64 `json:"copyOf,omitempty"`
	Copies int   `json:"copies,omitempty"`
	// RootPath is the library folder chosen for the show ("" = the main TV folder).
	RootPath string `json:"rootPath,omitempty"`
	// ShowStatus is where the show stands: "ended", "continuing" or "upcoming"
	// (from TMDB); left out until the show has been refreshed once.
	ShowStatus string `json:"showStatus,omitempty"`
}

// toSeriesPayload builds a library show's JSON; who resolves the account
// that added it (see accountNames).
func toSeriesPayload(s library.Series, who func(int64) *userRef) seriesPayload {
	genres := s.Genres
	if genres == nil {
		genres = []string{}
	}
	return seriesPayload{
		ID: s.ID, TMDBID: s.TMDBID, Title: s.Title, Year: s.Year, Overview: s.Overview,
		PosterURL: metadata.PosterURL(s.PosterPath), Monitored: s.Monitored,
		EpisodeCount: s.EpisodeCount, DownloadedCount: s.DownloadedCount, ProfileID: s.ProfileID, Sources: s.SourcePref,
		Genres: genres, Tags: []string{}, AddedBy: who(s.AddedBy),
		NoUpgrade: s.NoUpgrade, DetailsState: s.DetailsState, DetailsNote: s.DetailsNote, SeriesType: library.ValidSeriesType(s.SeriesType),
		FirstAirDate: s.FirstAirDate, CopyOf: s.CopyOf, RootPath: s.RootPath,
		ShowStatus: showStatusOf(s.TMDBStatus),
	}
}

// showStatusOf is the show's status in the words the screens use, or "" when
// TMDB's status isn't known yet.
func showStatusOf(tmdbStatus string) string {
	if strings.TrimSpace(tmdbStatus) == "" {
		return ""
	}
	status, _ := showEndedStatus(tmdbStatus)
	return status
}

type episodePayload struct {
	ID        int64  `json:"id"`
	Season    int    `json:"season"`
	Episode   int    `json:"episode"`
	Title     string `json:"title,omitempty"`
	Overview  string `json:"overview,omitempty"`
	AirDate   string `json:"airDate,omitempty"`
	Status    string `json:"status"`
	Quality   string `json:"quality,omitempty"`
	FilePath  string `json:"filePath,omitempty"`
	Monitored bool   `json:"monitored"`
	// SizeBytes is the size of the episode's file on disk (downloaded episodes only).
	SizeBytes int64 `json:"sizeBytes,omitempty"`
}

func toEpisodePayload(e library.Episode) episodePayload {
	return episodePayload{
		ID: e.ID, Season: e.Season, Episode: e.Episode, Title: e.Title, Overview: e.Overview,
		AirDate: e.AirDate, Status: string(e.Status), Quality: e.Quality, FilePath: e.FilePath, Monitored: e.Monitored,
	}
}

type seriesDetailPayload struct {
	seriesPayload
	Episodes []episodePayload `json:"episodes"`
}

// handleSearchTV searches for shows by title, to find something to add. It
// answers with an empty list (not an error) when there is no TMDB key, the
// same as Discover.
func (s *Server) handleSearchTV(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	if !s.TMDB().HasAPIKey() {
		writeJSON(w, http.StatusOK, []discoverPayload{})
		return
	}
	shows, err := s.displayTMDB().SearchTV(r.Context(), query)
	if err != nil {
		writeUpstreamError(w, "search", err)
		return
	}
	newestFirst(shows, query, func(sh metadata.Show) string { return sh.Name }, func(sh metadata.Show) int { return sh.Year() })
	out := make([]discoverPayload, len(shows))
	for i, sh := range shows {
		out[i] = s.showDiscover(r.Context(), sh)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleListSeries lists the library's shows. With ?brief=1 each show's
// overview is left out, as for movies.
func (s *Server) handleListSeries(w http.ResponseWriter, r *http.Request) {
	brief := r.URL.Query().Get("brief") == "1"
	list, err := s.MovieRepo.ListSeries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	who := s.accountNames()
	tags, _ := s.MovieRepo.AllTitleTags(library.TagSeries)
	copies, _ := s.MovieRepo.CopyCounts("series")
	files, err := s.MovieRepo.EpisodeFilesBySeries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]seriesPayload, len(list))
	now := time.Now()
	for i, sr := range list {
		out[i] = toSeriesPayload(sr, who)
		if brief {
			out[i].Overview = ""
		}
		out[i].Copies = copies[sr.ID]
		for _, f := range files[sr.ID] {
			out[i].SizeBytes += cachedFileSize(f, now)
		}
		if t := tags[sr.ID]; t != nil {
			out[i].Tags = t
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid show ID.")
		return
	}
	sr, err := s.MovieRepo.GetSeries(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "That show isn't in your library.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	eps, err := s.MovieRepo.ListEpisodes(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := seriesDetailPayload{seriesPayload: toSeriesPayload(sr, s.accountNames()), Episodes: make([]episodePayload, len(eps))}
	if t, err := s.MovieRepo.TitleTags(library.TagSeries, sr.ID); err == nil {
		out.Tags = t
	}
	now := time.Now()
	for i, e := range eps {
		out.Episodes[i] = toEpisodePayload(e)
		if e.Status == library.StatusDownloaded {
			out.Episodes[i].SizeBytes = cachedFileSize(e.FilePath, now)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// addSeriesRequest is what the "Add to library" dialog sends for a show.
// Monitor decides which episodes automation goes after: "all" (default),
// "future" (only episodes that have not aired yet) or "none".
type addSeriesRequest struct {
	TMDBID    int    `json:"tmdbId"`
	ProfileID int64  `json:"profileId"`
	Monitor   string `json:"monitor"`
	Sources   string `json:"sources"`
	SearchNow bool   `json:"searchNow"`
	// NoUpgrade leaves the episodes alone once they are downloaded. On when
	// left out: better versions are something the person asks for.
	NoUpgrade *bool `json:"noUpgrade"`
	// RootPath is the TV folder to keep it in; left out = the main one.
	RootPath string `json:"rootPath"`
}

// handleAddSeries adds a show and its complete episode list (every real
// season, specials left out) to the library. Every episode starts monitored
// and missing until the automatic search or a manual grab finds it.
func (s *Server) handleAddSeries(w http.ResponseWriter, r *http.Request) {
	if !s.requireModule(w, moduleTV) {
		return
	}
	if s.mustRequest(r) {
		s.fileRequest(w, r, "tv")
		return
	}
	var req addSeriesRequest
	if err := decodeJSON(r, &req); err != nil || req.TMDBID == 0 {
		writeError(w, http.StatusBadRequest, "tmdbId is required")
		return
	}
	if !s.TMDB().HasAPIKey() {
		writeError(w, http.StatusPreconditionFailed, "Add your TMDB API key first (Settings > Info, lists and subtitles > Movie and show info).")
		return
	}
	if existing, ok, err := s.MovieRepo.GetSeriesByTMDBID(req.TMDBID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if ok {
		writeError(w, http.StatusConflict, existing.Title+" is already in your library")
		return
	}

	if !validSourcePref(req.Sources) {
		writeError(w, http.StatusBadRequest, `sources must be "usenet", "torrent" or "both"`)
		return
	}
	if req.Monitor != "" && req.Monitor != "all" && req.Monitor != "future" && req.Monitor != "none" {
		writeError(w, http.StatusBadRequest, `monitor must be "all", "future" or "none"`)
		return
	}
	if err := s.checkProfileChoice(req.ProfileID); err != nil {
		writeProfileError(w, err)
		return
	}
	rootPath, problem := s.checkFolderChoice(req.RootPath, s.tvFolders(), "show")
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}

	userID, byline := requester(r)
	created, err := s.addSeriesFromTMDB(r.Context(), req.TMDBID, userID, boolOr(req.NoUpgrade, true))
	if err != nil {
		writeUpstreamError(w, "add that show", err)
		return
	}
	if rootPath != "" {
		if err := s.MovieRepo.SetSeriesRoot(created.ID, rootPath); err == nil {
			created.RootPath = rootPath
		}
	}
	switch req.Monitor {
	case "future":
		_ = s.MovieRepo.MonitorFromDate(created.ID, time.Now().UTC().Format("2006-01-02"))
	case "none":
		_ = s.MovieRepo.SetAllEpisodesMonitored(created.ID, false)
	}
	if req.ProfileID != 0 {
		if err := s.MovieRepo.SetSeriesProfile(created.ID, req.ProfileID); err == nil {
			created.ProfileID = req.ProfileID
		}
	}
	if req.Sources != "" {
		if err := s.MovieRepo.SetSeriesSourcePref(created.ID, req.Sources); err == nil {
			created.SourcePref = req.Sources
		}
	}
	_ = s.QueueRepo.LogSeriesActivity(created.ID, "added", created.Title+" (series) added to library"+byline)
	if req.SearchNow {
		go s.searchSeriesInBackground(created.ID)
	}
	writeJSON(w, http.StatusCreated, toSeriesPayload(created, s.accountNames()))
}

// addSeriesFromTMDB adds a show with its episode list and genres, recording
// userID (0 = unknown) as the account that added it. noUpgrade leaves the
// episodes alone once they are downloaded.
func (s *Server) addSeriesFromTMDB(ctx context.Context, tmdbID int, userID int64, noUpgrade bool) (library.Series, error) {
	detail, infos, err := s.TMDB().GetShowEpisodes(ctx, tmdbID)
	if err != nil {
		return library.Series{}, err
	}
	sr, err := s.MovieRepo.AddSeries(library.Series{
		TMDBID: detail.TMDBID, Title: detail.Name, Year: detail.Year(), Overview: detail.Overview,
		PosterPath: detail.PosterPath, FirstAirDate: detail.FirstAirDate, Monitored: true,
		NoUpgrade: noUpgrade, AddedBy: userID, Genres: s.TMDB().ShowGenres(ctx, detail.Show),
		SeriesType: guessSeriesType(detail.Show),
	}, toLibraryEpisodes(infos))
	if err != nil {
		return sr, err
	}
	s.saveSeriesIDs(&sr, detail)
	s.noteSeriesNumbering(sr)
	return sr, nil
}

// saveSeriesIDs stores the TVDB and IMDb ids and the status TMDB gave for a
// show.
func (s *Server) saveSeriesIDs(sr *library.Series, detail *metadata.ShowDetail) {
	tvdb, imdb, status := detail.ExternalIDs.TVDBID, detail.ExternalIDs.IMDBID, detail.Status
	if (tvdb == 0 || tvdb == sr.TVDBID) && (imdb == "" || imdb == sr.IMDBID) && (status == "" || status == sr.TMDBStatus) {
		return
	}
	if err := s.MovieRepo.SetSeriesExternalIDs(sr.ID, tvdb, imdb, status); err != nil {
		log.Printf("api: %v", err)
		return
	}
	if tvdb > 0 {
		sr.TVDBID = tvdb
	}
	if imdb != "" {
		sr.IMDBID = imdb
	}
	if status != "" {
		sr.TMDBStatus = status
	}
}

func toLibraryEpisodes(infos []metadata.EpisodeInfo) []library.Episode {
	eps := make([]library.Episode, len(infos))
	for i, e := range infos {
		eps[i] = library.Episode{Season: e.Season, Episode: e.Episode, Title: e.Name, Overview: e.Overview, AirDate: e.AirDate}
	}
	return eps
}

// handleRefreshSeries re-fetches a show's episode list from TMDB — new
// seasons appear and air dates get announced or moved over a show's life.
// Only adds/updates episode metadata; never touches download state.
func (s *Server) handleRefreshSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid show ID.")
		return
	}
	sr, err := s.MovieRepo.GetSeries(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "That show isn't in your library.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.refreshSeries(r.Context(), sr); err != nil {
		writeUpstreamError(w, "refresh that show", err)
		return
	}
	updated, err := s.MovieRepo.GetSeries(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toSeriesPayload(updated, s.accountNames()))
}

// refreshSeries brings a show's episode list and genres up to date from TMDB.
func (s *Server) refreshSeries(ctx context.Context, sr library.Series) error {
	detail, infos, err := s.TMDB().GetShowEpisodes(ctx, sr.TMDBID)
	if err != nil {
		return err
	}
	if len(detail.Genres) > 0 || sr.Genres == nil {
		if err := s.MovieRepo.SetSeriesGenres(sr.ID, s.TMDB().ShowGenres(ctx, detail.Show)); err != nil {
			log.Printf("api: refresh %q genres: %v", sr.Title, err)
		}
	}
	eps := toLibraryEpisodes(infos)
	if err := s.MovieRepo.UpsertEpisodes(sr.ID, eps, sr.Monitored); err != nil {
		return err
	}
	s.saveSeriesIDs(&sr, detail)
	s.noteSeriesNumbering(sr)
	s.stopMonitoringIfFinished(sr.ID, detail.Status)
	// Second copies get the same new episodes.
	copies, err := s.MovieRepo.ListSeriesCopies(sr.ID)
	if err != nil {
		return err
	}
	for _, c := range copies {
		if err := s.MovieRepo.UpsertEpisodes(c.ID, eps, c.Monitored); err != nil {
			return err
		}
	}
	return nil
}

type tvGrabRequest struct {
	grabRequest
	// Season/Episode are hints for releases whose title carries no S01E02
	// marker; when it does, the title wins (see resolveTVTarget).
	Season  int `json:"season,omitempty"`
	Episode int `json:"episode,omitempty"`
}

// handleGrabSeries grabs a release for a series — a single episode, a
// multi-episode release, or a whole-season pack, decided by the release's
// own title. Returns immediately; progress shows up in the queue.
func (s *Server) handleGrabSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid show ID.")
		return
	}
	sr, err := s.MovieRepo.GetSeries(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "That show isn't in your library.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req tvGrabRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	if req.ReleaseTitle == "" || req.DownloadURL == "" {
		writeError(w, http.StatusBadRequest, "releaseTitle and downloadUrl are required")
		return
	}
	if !s.checkGrabURL(w, r, &req.DownloadURL) {
		return
	}
	queueID, err := s.grabTVRelease(sr, req.Season, req.Episode, req.ReleaseTitle, req.DownloadURL, req.SizeBytes, req.protocol())
	if err != nil {
		writeGrabError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int64{"queueId": queueID})
}

// handleDeleteSeries removes a show from the library. Its downloads are
// always cancelled and their files in the downloads folder deleted. With
// ?deleteFiles=true its folder in the TV library goes too, with every
// season, subtitle, .nfo and artwork (or, when the episodes do not sit in a
// folder of the show's own, each episode file with the files named after
// it).
func (s *Server) handleDeleteSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid show ID.")
		return
	}
	if _, err := s.removeSeries(id, r.URL.Query().Get("deleteFiles") == "true"); err != nil {
		writeRemoveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// removeSeries takes one show out of the library, like removeMovie does for
// a movie. It returns the show's title.
func (s *Server) removeSeries(id int64, deleteFiles bool) (string, error) {
	series, err := s.MovieRepo.GetSeries(id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &removeProblem{http.StatusNotFound, "That show isn't in your library."}
	}
	if err != nil {
		return "", &removeProblem{http.StatusInternalServerError, err.Error()}
	}
	// A main show's second copies go first, the same way.
	if series.CopyOf == 0 {
		copies, err := s.MovieRepo.ListSeriesCopies(series.ID)
		if err != nil {
			return series.Title, &removeProblem{http.StatusInternalServerError, err.Error()}
		}
		for _, c := range copies {
			if _, err := s.removeSeries(c.ID, deleteFiles); err != nil {
				return series.Title, err
			}
		}
	}
	files, err := s.seriesFiles(id)
	if err != nil {
		return series.Title, &removeProblem{http.StatusInternalServerError, err.Error()}
	}
	if deleteFiles {
		if err := s.checkFilesRemovable(s.seriesRootFor(series), files); err != nil {
			return series.Title, &removeProblem{http.StatusConflict, err.Error()}
		}
	}
	// Downloads are always cancelled and their working folders deleted.
	if _, err := s.removeDownloadsFor(0, series.ID, series.Title); err != nil {
		return series.Title, &removeProblem{http.StatusInternalServerError, err.Error()}
	}
	// A download that was importing when it was cancelled finishes its import
	// first, so episodes may have files now that they did not have a moment
	// ago. With "delete files" asked for, those go too.
	if now, err := s.seriesFiles(id); err == nil && !slices.Equal(now, files) {
		files = now
		if deleteFiles {
			if err := s.checkFilesRemovable(s.seriesRootFor(series), files); err != nil {
				return series.Title, &removeProblem{http.StatusConflict, err.Error()}
			}
		}
	}
	var deleted removedFiles
	if deleteFiles {
		var err error
		if deleted, err = s.removeSeriesFiles(series, files); err != nil {
			return series.Title, &removeProblem{http.StatusInternalServerError, err.Error()}
		}
	}
	if err := s.MovieRepo.DeleteSeries(id); err != nil {
		return series.Title, &removeProblem{http.StatusInternalServerError, err.Error()}
	}
	s.forgetHeldSeries(id)
	_ = s.QueueRepo.LogActivity(0, "removed", removedLogLine(series.Title+" (series)", deleteFiles && len(files) > 0, deleted, countDistinct(files)))
	return series.Title, nil
}

// seriesFiles lists the files the show's episodes are saved as.
func (s *Server) seriesFiles(seriesID int64) ([]string, error) {
	eps, err := s.MovieRepo.ListEpisodes(seriesID)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, ep := range eps {
		if ep.FilePath != "" {
			files = append(files, ep.FilePath)
		}
	}
	return files, nil
}

func (s *Server) handleTrendingTV(w http.ResponseWriter, r *http.Request) {
	s.discoverShows(w, r, s.displayTMDB().TrendingTV)
}

func (s *Server) handlePopularTV(w http.ResponseWriter, r *http.Request) {
	s.discoverShows(w, r, s.displayTMDB().PopularTV)
}

func (s *Server) discoverShows(w http.ResponseWriter, r *http.Request, fetch func(context.Context) ([]metadata.Show, error)) {
	if !s.TMDB().HasAPIKey() {
		writeJSON(w, http.StatusOK, []discoverPayload{})
		return
	}
	shows, err := fetch(r.Context())
	if err != nil {
		writeUpstreamError(w, "load the titles", err)
		return
	}
	out := make([]discoverPayload, len(shows))
	for i, sh := range shows {
		out[i] = s.showDiscover(r.Context(), sh)
	}
	writeJSON(w, http.StatusOK, out)
}
