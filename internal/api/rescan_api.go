package api

import (
	"net/http"
	"strconv"
)

// handleRescanMovie looks at a movie's file again, for the Rescan files button.
func (s *Server) handleRescanMovie(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid movie ID.")
		return
	}
	if _, err := s.MovieRepo.Get(id); err != nil {
		writeError(w, http.StatusNotFound, "That movie isn't in your library.")
		return
	}
	writeJSON(w, http.StatusOK, s.rescanMovieFiles(id, true))
}

// handleRescanSeries looks at every downloaded file of a show again.
func (s *Server) handleRescanSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid show ID.")
		return
	}
	if _, err := s.MovieRepo.GetSeries(id); err != nil {
		writeError(w, http.StatusNotFound, "That show isn't in your library.")
		return
	}
	writeJSON(w, http.StatusOK, s.rescanSeriesFiles(id, true))
}
