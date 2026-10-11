package api

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/rdborg/mediarium/internal/indexers"
)

// Definition files an administrator adds themselves, for a site the shared
// list doesn't have (such as a helper running on their own network).

// handleAddCustomDefinition takes the text of a Cardigann (.yml) definition.
func (s *Server) handleAddCustomDefinition(w http.ResponseWriter, r *http.Request) {
	// A definition decides where an indexer's saved login is sent, so it is not something an API key may change.
	if !requireSession(w, r) {
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that file. It must be a definition file (.yml) of 1 MB at most.")
		return
	}
	// A file with the id of a definition an indexer already runs on would change where that indexer's saved login goes.
	if def, perr := indexers.ParseDefinition([]byte(req.Content)); perr == nil {
		if list, lerr := s.IndexerRepo.List(); lerr == nil {
			for _, inst := range list {
				if inst.Kind == indexers.KindCardigann && inst.DefinitionID == def.ID {
					writeError(w, http.StatusConflict, "The indexer "+inst.Name+" already uses a definition with this id. Remove that indexer first, then add the file.")
					return
				}
			}
		}
	}
	sum, err := s.Definitions.AddCustom([]byte(req.Content))
	if errors.Is(err, indexers.ErrBadCustomDefinition) {
		writeError(w, http.StatusBadRequest, "This file can't be used: "+problemText(err, indexers.ErrBadCustomDefinition))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("indexers: definition file added id=%s name=%q", sum.ID, sum.Name)
	writeJSON(w, http.StatusCreated, sum)
}

// handleRemoveCustomDefinition deletes an added definition file, unless an
// indexer still uses it.
func (s *Server) handleRemoveCustomDefinition(w http.ResponseWriter, r *http.Request) {
	if !requireSession(w, r) {
		return
	}
	id := r.PathValue("id")
	list, err := s.IndexerRepo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var using []string
	for _, inst := range list {
		if inst.Kind == indexers.KindCardigann && inst.DefinitionID == id {
			using = append(using, inst.Name)
		}
	}
	if len(using) > 0 {
		writeError(w, http.StatusConflict, "Remove the indexer that uses this file first: "+strings.Join(using, ", ")+".")
		return
	}
	err = s.Definitions.RemoveCustom(id)
	if errors.Is(err, indexers.ErrDefinitionNotFound) {
		writeError(w, http.StatusNotFound, "There's no added definition file with that id.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("indexers: definition file removed id=%s", id)
	w.WriteHeader(http.StatusNoContent)
}
