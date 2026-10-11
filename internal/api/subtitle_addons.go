package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/settings"
	"github.com/rdborg/mediarium/internal/subtitles"
)

// Subtitle add-ons: sites Mediarium has no code of its own for, reached through
// small web services that follow the Stremio add-on protocol (see
// subtitles/addon.go). They are kept as one encrypted setting, because an
// add-on's address can carry the person's own key for the site.

const maxSubtitleAddons = 8

var addonMu sync.Mutex // one change to the list at a time

func (s *Server) loadAddons() []subtitles.Addon {
	raw, _ := s.Settings.Get(settings.KeySubtitleAddons)
	if raw == "" {
		return nil
	}
	var list []subtitles.Addon
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		log.Printf("subtitles: the add-on list can't be read: %v", err)
		return nil
	}
	return list
}

func (s *Server) saveAddons(list []subtitles.Addon) error {
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.Settings.Set(settings.KeySubtitleAddons, string(b), true)
}

type addonPayload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"` // where it lives; the rest of the address can hold a key and is not shown
}

func addonView(a subtitles.Addon) addonPayload {
	host := a.URL
	if u, err := url.Parse(a.URL); err == nil && u.Host != "" {
		host = u.Host
	}
	return addonPayload{ID: a.ID, Name: a.Name, Host: host}
}

func (s *Server) handleListSubtitleAddons(w http.ResponseWriter, r *http.Request) {
	out := []addonPayload{}
	for _, a := range s.loadAddons() {
		out = append(out, addonView(a))
	}
	writeJSON(w, http.StatusOK, out)
}

type addAddonRequest struct {
	URL string `json:"url"`
}

func (s *Server) handleAddSubtitleAddon(w http.ResponseWriter, r *http.Request) {
	var req addAddonRequest
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.URL) == "" {
		writeError(w, http.StatusBadRequest, "Paste the add-on's address first. It looks like https://example.com/manifest.json.")
		return
	}
	base, err := subtitles.ParseAddonURL(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That doesn't look right: "+err.Error()+".")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	m, err := s.addons.Manifest(ctx, base)
	if err != nil {
		writeError(w, http.StatusBadRequest, capitalize(err.Error())+".")
		return
	}
	addonMu.Lock()
	defer addonMu.Unlock()
	list := s.loadAddons()
	for _, a := range list {
		if a.URL == base {
			writeError(w, http.StatusConflict, "That add-on is already added.")
			return
		}
	}
	if len(list) >= maxSubtitleAddons {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("You can add up to %d add-ons. Remove one first.", maxSubtitleAddons))
		return
	}
	id := make([]byte, 5)
	if _, err := rand.Read(id); err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't save the add-on.")
		return
	}
	added := subtitles.Addon{ID: hex.EncodeToString(id), Name: strings.TrimSpace(m.Name), URL: base}
	if err := s.saveAddons(append(list, added)); err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't save the add-on: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, addonView(added))
}

func (s *Server) handleDeleteSubtitleAddon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	addonMu.Lock()
	defer addonMu.Unlock()
	list := s.loadAddons()
	kept := list[:0:0]
	for _, a := range list {
		if a.ID != id {
			kept = append(kept, a)
		}
	}
	if len(kept) == len(list) {
		writeError(w, http.StatusNotFound, "That add-on isn't in the list.")
		return
	}
	if err := s.saveAddons(kept); err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't remove the add-on: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleTestSubtitleAddon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, a := range s.loadAddons() {
		if a.ID != id {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		m, err := s.addons.Manifest(ctx, a.URL)
		if err != nil {
			writeJSON(w, http.StatusOK, connTestResult{Message: capitalize(err.Error()) + "."})
			return
		}
		writeJSON(w, http.StatusOK, connTestResult{OK: true, Message: m.Name + " answered."})
		return
	}
	writeError(w, http.StatusNotFound, "That add-on isn't in the list.")
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ---- searching and downloading

// addonImdbID is the IMDb id the add-ons are asked with, read from TMDB (an
// English-language client: the id does not depend on the language).
func (s *Server) addonImdbID(ctx context.Context, q subtitles.Query) string {
	if q.ParentTMDBID > 0 {
		if sr, ok, err := s.MovieRepo.GetSeriesByTMDBID(q.ParentTMDBID); err == nil && ok && sr.IMDBID != "" {
			return sr.IMDBID
		}
		if d, err := s.TMDB().GetShowFull(ctx, q.ParentTMDBID); err == nil {
			return d.ExternalIDs.IMDBID
		}
		return ""
	}
	if q.TMDBID > 0 {
		if d, err := s.TMDB().GetMovieDetail(ctx, q.TMDBID); err == nil {
			return d.IMDBID
		}
	}
	return ""
}

// findAddonSubtitles asks every add-on about a title and returns what they
// offer as results. The link of each is the add-on's own download address,
// sealed with the server key so that only addresses an add-on really offered
// can be fetched later.
func (s *Server) findAddonSubtitles(ctx context.Context, q subtitles.Query) (results []subtitles.Result, asked int, errs []error) {
	list := s.loadAddons()
	if len(list) == 0 {
		return nil, 0, nil
	}
	imdb := s.addonImdbID(ctx, q)
	if imdb == "" {
		return nil, len(list), nil // nothing to ask them with: asked, found nothing, not a failure
	}
	aq := subtitles.AddonQuery{
		IMDBID: imdb, Series: q.ParentTMDBID > 0, Season: q.Season, Episode: q.Episode,
		Language: q.Language, Filename: q.Filename, VideoHash: q.MovieHash, VideoSize: q.FileSize,
	}
	for _, a := range list {
		asked++
		found, err := s.addons.Find(ctx, a.URL, aq)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Name, err))
			continue
		}
		for _, f := range found {
			sealed, err := s.box.Encrypt(f.URL)
			if err != nil {
				continue
			}
			release := f.Release
			if release == "" {
				release = a.Name
			}
			results = append(results, subtitles.Result{Language: f.Language, Release: release, Source: subtitles.SourceAddon, Link: sealed, HearingImpaired: f.HearingImpaired, ForeignPartsOnly: f.Forced && !f.HearingImpaired})
		}
	}
	return results, asked, errs
}

// openAddonLink gives back the address sealed into a result's link.
func (s *Server) openAddonLink(link string) (string, bool) {
	if link == "" {
		return "", false
	}
	u, err := s.box.Decrypt(link)
	if err != nil || !strings.HasPrefix(u, "http") {
		return "", false
	}
	return u, true
}

// queryFileInfo adds what an add-on can use to pick a subtitle that fits this
// exact file: its name and size.
func queryFileInfo(q subtitles.Query, filePath string) subtitles.Query {
	if filePath == "" {
		return q
	}
	q.Filename = filepath.Base(filePath)
	if info, err := os.Stat(filePath); err == nil {
		q.FileSize = info.Size()
	}
	return q
}
