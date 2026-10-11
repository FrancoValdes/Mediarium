package api

import (
	"log/slog"
	"net/http"

	"github.com/rdborg/mediarium/internal/auth"
)

// The profile page's "E-readers" card: each account makes (and can remove)
// its own e-reader password for the OPDS catalogue and its own Kobo sync
// address. Nothing exists until the account asks for it.

type ereaderOPDSPayload struct {
	On      bool   `json:"on"`
	Path    string `json:"path"` // "/opds/", the page adds the address it is opened at
	Created string `json:"created,omitempty"`
}

type ereaderKoboPayload struct {
	On      bool   `json:"on"`
	Path    string `json:"path,omitempty"` // "/kobo/<token>"
	Created string `json:"created,omitempty"`
}

type ereaderPayload struct {
	Username string             `json:"username"`
	Ebooks   bool               `json:"ebooks"` // ebooks are switched on
	Allowed  bool               `json:"allowed"`
	OPDS     ereaderOPDSPayload `json:"opds"`
	Kobo     ereaderKoboPayload `json:"kobo"`
}

func (s *Server) ereaderPayloadFor(u *auth.User) (ereaderPayload, error) {
	acc, err := s.EReader.Get(u.ID)
	if err != nil {
		return ereaderPayload{}, err
	}
	allowed, _ := s.ereaderUserAllowed(u.ID)
	p := ereaderPayload{Username: u.Username, Ebooks: s.ebooksEnabled(), Allowed: allowed,
		OPDS: ereaderOPDSPayload{On: acc.OPDS, Path: s.cfg.URLBase + "/opds/", Created: acc.OPDSCreated}}
	if acc.Kobo {
		token, err := s.EReader.KoboToken(u.ID)
		if err != nil {
			return ereaderPayload{}, err
		}
		if token != "" {
			p.Kobo = ereaderKoboPayload{On: true, Path: s.cfg.URLBase + "/kobo/" + token, Created: acc.KoboCreated}
		}
	}
	return p, nil
}

func (s *Server) ereaderUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "You need to sign in first.")
		return nil, false
	}
	return u, true
}

func (s *Server) respondEReader(w http.ResponseWriter, u *auth.User, status int, extra func(*ereaderPayload)) {
	p, err := s.ereaderPayloadFor(u)
	if err != nil {
		slog.Error("ereader: read access", "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't read your e-reader settings.")
		return
	}
	if extra != nil {
		extra(&p)
	}
	writeJSON(w, status, p)
}

// GET /api/auth/ereader
func (s *Server) handleGetEReader(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.ereaderUser(w, r); ok {
		s.respondEReader(w, u, http.StatusOK, nil)
	}
}

type ereaderPasswordPayload struct {
	ereaderPayload
	Password string `json:"password"` // shown once
}

// POST /api/auth/ereader/opds: makes a new e-reader password. It is sent in
// this answer only.
func (s *Server) handleNewOPDSPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := s.ereaderUser(w, r)
	if !ok {
		return
	}
	pw, err := s.EReader.NewOPDSPassword(u.ID)
	if err != nil {
		slog.Error("ereader: new password", "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't make the password. Try again.")
		return
	}
	p, err := s.ereaderPayloadFor(u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't read your e-reader settings.")
		return
	}
	writeJSON(w, http.StatusOK, ereaderPasswordPayload{ereaderPayload: p, Password: pw})
}

// DELETE /api/auth/ereader/opds
func (s *Server) handleRemoveOPDSPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := s.ereaderUser(w, r)
	if !ok {
		return
	}
	if err := s.EReader.RemoveOPDS(u.ID); err != nil {
		slog.Error("ereader: remove password", "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't turn it off. Try again.")
		return
	}
	s.respondEReader(w, u, http.StatusOK, nil)
}

// POST /api/auth/ereader/kobo: makes a new Kobo address; the old one stops
// working and the next sync sends everything again.
func (s *Server) handleNewKoboToken(w http.ResponseWriter, r *http.Request) {
	u, ok := s.ereaderUser(w, r)
	if !ok {
		return
	}
	if _, err := s.EReader.NewKoboToken(u.ID); err != nil {
		slog.Error("ereader: new Kobo token", "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't make the address. Try again.")
		return
	}
	if err := s.EReader.ResetKobo(u.ID); err != nil {
		slog.Error("ereader: reset Kobo", "err", err)
	}
	s.respondEReader(w, u, http.StatusOK, nil)
}

// DELETE /api/auth/ereader/kobo
func (s *Server) handleRemoveKoboToken(w http.ResponseWriter, r *http.Request) {
	u, ok := s.ereaderUser(w, r)
	if !ok {
		return
	}
	if err := s.EReader.RemoveKobo(u.ID); err != nil {
		slog.Error("ereader: remove Kobo token", "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't turn it off. Try again.")
		return
	}
	s.respondEReader(w, u, http.StatusOK, nil)
}

// POST /api/auth/ereader/kobo/resync: forgets what was sent to the Kobo, so
// the next sync sends every book again.
func (s *Server) handleKoboResync(w http.ResponseWriter, r *http.Request) {
	u, ok := s.ereaderUser(w, r)
	if !ok {
		return
	}
	if err := s.EReader.ResetKobo(u.ID); err != nil {
		slog.Error("ereader: reset Kobo", "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't start over. Try again.")
		return
	}
	s.respondEReader(w, u, http.StatusOK, nil)
}
