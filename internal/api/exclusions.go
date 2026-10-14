package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/rdborg/mediarium/internal/auth"
)

// "Not interested": movies and shows you never want to see again on Discover.
// The list is shared by everyone using this Mediarium, kept in the database,
// and can be undone from Settings or the API.

type exclusionPayload struct {
	Kind      string `json:"kind"` // "movie" or "tv"
	TMDBID    int    `json:"tmdbId"`
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

func validExclusionKind(k string) bool { return k == "movie" || k == "tv" }

// mayChangeExclusions reports whether the caller may change the shared list
// for kind: administrators always, anyone else only when they may add that
// kind of title to the library themselves (the list decides what import
// lists add). Otherwise it answers 403.
func (s *Server) mayChangeExclusions(w http.ResponseWriter, r *http.Request, kind string) bool {
	u := auth.UserFromContext(r.Context())
	if u == nil || u.IsAdmin {
		return true
	}
	perm := auth.PermMovies
	if kind == "tv" {
		perm = auth.PermTV
	}
	p, err := s.Auth.PermissionsOf(u.ID)
	if err != nil || !p.AddDirect || !p.Allows(perm) {
		writeError(w, http.StatusForbidden, notAllowedMessage)
		return false
	}
	return true
}

func (s *Server) handleListExclusions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT kind, tmdb_id, title, year, created_at FROM exclusions ORDER BY created_at DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []exclusionPayload{}
	for rows.Next() {
		var e exclusionPayload
		if err := rows.Scan(&e.Kind, &e.TMDBID, &e.Title, &e.Year, &e.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddExclusion(w http.ResponseWriter, r *http.Request) {
	var req exclusionPayload
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if !validExclusionKind(req.Kind) || req.TMDBID <= 0 || req.Title == "" || len(req.Title) > 300 {
		writeError(w, http.StatusBadRequest, `Send the kind ("movie" or "tv"), the TMDB id and the title.`)
		return
	}
	if !s.mayChangeExclusions(w, r, req.Kind) {
		return
	}
	if _, err := s.db.Exec(`INSERT INTO exclusions (kind, tmdb_id, title, year) VALUES (?, ?, ?, ?)
		ON CONFLICT(kind, tmdb_id) DO NOTHING`, req.Kind, req.TMDBID, req.Title, req.Year); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

func (s *Server) handleRemoveExclusion(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	id, err := strconv.Atoi(r.PathValue("tmdbId"))
	if !validExclusionKind(kind) || err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "That isn't on the Not interested list.")
		return
	}
	if !s.mayChangeExclusions(w, r, kind) {
		return
	}
	if _, err := s.db.Exec(`DELETE FROM exclusions WHERE kind = ? AND tmdb_id = ?`, kind, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nil)
}
