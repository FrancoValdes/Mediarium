package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/rdborg/mediarium/internal/settings"
)

// Add-ons are other programs that run next to Mediarium (a channel guide, an
// adult-library manager, a dashboard) which a person wants a link to inside
// Mediarium. Mediarium doesn't run them or load their code: it keeps a name
// and an address, shows the link in the players menu and, when asked, shows
// the program's own page in a frame. Each add-on is its own project with its
// own licence and its own responsibility. What it may do to Mediarium is
// decided by the API key it is given, and a read-only key (Profile) is the
// right one for most.

const (
	maxAppLinks  = 20
	appLinkTab   = "tab"
	appLinkFrame = "frame"
)

type appLink struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Mode    string `json:"mode"` // "tab" opens it in a new tab, "frame" shows it inside Mediarium
	Icon    string `json:"icon"` // the name of one of Mediarium's icons; unknown ones show a plain tile
	Enabled bool   `json:"enabled"`
}

var appLinkIconName = regexp.MustCompile(`^[a-z0-9-]{1,24}$`)

// appHostName is a DNS name: letters, digits and hyphens in dot-separated labels.
var appHostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.?$`)

// appOrigin is the origin (scheme://host[:port]) of an add-on's address,
// rebuilt from its checked parts. ok is false for anything that isn't a plain
// http or https address: spaces, quotes or semicolons anywhere, a user name,
// or a host that isn't a DNS name or an IP address. The origin goes into the
// Content-Security-Policy header, where any of those could add a rule.
func appOrigin(raw string) (string, bool) {
	if raw == "" || strings.ContainsAny(raw, " \t\r\n;'\"`\\") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Opaque != "" {
		return "", false
	}
	host, port := u.Hostname(), u.Port()
	switch addr, err := netip.ParseAddr(host); {
	case err == nil && addr.Zone() == "":
		host = addr.String()
		if addr.Is6() {
			host = "[" + host + "]"
		}
	case err == nil, !appHostName.MatchString(host):
		return "", false
	default:
		host = strings.ToLower(host)
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", false
	}
	origin := u.Scheme + "://" + host
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", false
		}
		origin += ":" + port
	}
	return origin, true
}

func (s *Server) appLinks() []appLink {
	raw, _ := s.Settings.Get(settings.KeyAppLinks)
	var list []appLink
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	return list
}

// appFrameSources are the origins Mediarium may show in a frame: the
// enabled add-ons that are set to open inside Mediarium.
func (s *Server) appFrameSources() []string {
	if v, ok := s.appFrames.Load().([]string); ok {
		return v
	}
	return nil
}

// refreshAppFrames reads the add-ons from the settings into memory. It runs
// at start and after the list is saved: the security headers on every answer
// use the copy in memory, because they must not wait for the database.
func (s *Server) refreshAppFrames() {
	var out []string
	seen := map[string]bool{}
	for _, a := range s.appLinks() {
		if !a.Enabled || a.Mode != appLinkFrame {
			continue
		}
		// Checked again here, not only when saved: the origin goes into the
		// Content-Security-Policy header of every answer.
		origin, ok := appOrigin(a.URL)
		if !ok {
			continue
		}
		if !seen[origin] {
			seen[origin] = true
			out = append(out, origin)
		}
	}
	s.appFrames.Store(out)
}

// handleListAppLinks lists the add-ons: every one for an administrator (who
// edits them), the enabled ones for everybody else.
func (s *Server) handleListAppLinks(w http.ResponseWriter, r *http.Request) {
	list := s.appLinks()
	out := make([]appLink, 0, len(list))
	admin := isAdminRequest(r)
	for _, a := range list {
		if admin || a.Enabled {
			out = append(out, a)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePutAppLinks replaces the whole list (administrators).
func (s *Server) handlePutAppLinks(w http.ResponseWriter, r *http.Request) {
	var req []appLink
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	if len(req) > maxAppLinks {
		writeError(w, http.StatusBadRequest, "That is a lot of add-ons: the most is 20.")
		return
	}
	ids := map[string]bool{}
	for i := range req {
		a := &req[i]
		a.Name, a.URL = strings.TrimSpace(a.Name), strings.TrimSpace(a.URL)
		if rejectBad(w,
			checkRequired(a.Name, "Give each add-on a name."), checkMaxLen(a.Name, "The add-on's name", 40), checkNoControl(a.Name, "The add-on's name"),
			checkRequired(a.URL, "Give each add-on an address, for example http://192.168.1.20:8080."), checkMaxLen(a.URL, "The add-on's address", 300), checkNoControl(a.URL, "The add-on's address"),
		) {
			return
		}
		u, err := url.Parse(a.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			writeError(w, http.StatusBadRequest, "The address of "+a.Name+" must start with http:// or https:// and not contain a name or password.")
			return
		}
		if _, ok := appOrigin(a.URL); !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("The address of %s isn't a plain web address. It should look like http://192.168.1.20:8080, with no spaces, quotes or semicolons.", a.Name))
			return
		}
		if a.Mode != appLinkFrame {
			a.Mode = appLinkTab
		}
		if !appLinkIconName.MatchString(a.Icon) {
			a.Icon = ""
		}
		if a.ID == "" || ids[a.ID] || !regexp.MustCompile(`^[a-f0-9]{8,32}$`).MatchString(a.ID) {
			b := make([]byte, 6)
			_, _ = rand.Read(b)
			a.ID = hex.EncodeToString(b)
		}
		ids[a.ID] = true
	}
	val := ""
	if len(req) > 0 {
		b, _ := json.Marshal(req)
		val = string(b)
	}
	if err := s.Settings.Set(settings.KeyAppLinks, val, false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.refreshAppFrames()
	if req == nil {
		req = []appLink{}
	}
	writeJSON(w, http.StatusOK, req)
}
