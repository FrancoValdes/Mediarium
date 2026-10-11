package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/netguard"
)

// Subtitle add-ons are a way to bring in a site Mediarium has no code for. An
// add-on is a small web service that follows the Stremio add-on protocol: you
// ask it for the subtitles of one title and it answers with a list of links.
// A lot of sites have one (subs.ro, Hebrew and Arabic sites among them), so one
// client covers them all. Nothing here is specific to a site.
//
//	GET <address>/manifest.json                          what it is and what it can do
//	GET <address>/subtitles/<type>/<id>[/<extra>].json   {"subtitles":[{"id","url","lang"}]}
//
// <type> is "movie" or "series", <id> the IMDb id ("tt0133093", or
// "tt0944947:1:2" for season 1 episode 2) and <extra> optional hints such as
// the file name and the video's hash.

const (
	// SourceAddon marks a result that came from a subtitle add-on.
	SourceAddon = "addon"

	maxAddonResponse = 4 << 20
	maxAddonFile     = 20 << 20
)

// Addon is a subtitle add-on that has been added.
type Addon struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// URL is the add-on's address without /manifest.json. It can hold the
	// person's own key for the site, so it is stored encrypted and never shown.
	URL string `json:"url"`
}

// AddonManifest is what an add-on says about itself.
type AddonManifest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	// Resources is a list of names ("subtitles") or of objects with a name.
	Resources []json.RawMessage `json:"resources"`
	Types     []string          `json:"types"`
}

// ProvidesSubtitles reports whether the add-on lists subtitles among what it does.
func (m AddonManifest) ProvidesSubtitles() bool {
	for _, r := range m.Resources {
		var name string
		if json.Unmarshal(r, &name) == nil && name == "subtitles" {
			return true
		}
		var obj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(r, &obj) == nil && obj.Name == "subtitles" {
			return true
		}
	}
	return false
}

// ParseAddonURL turns what a person pasted into the add-on's address: the
// "stremio://" form becomes https, a trailing /manifest.json and slashes go.
func ParseAddonURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(strings.ToLower(raw), "stremio://"):
		raw = "https://" + raw[len("stremio://"):]
	case !strings.Contains(raw, "://"):
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("that isn't a web address. An add-on's address looks like https://example.com/manifest.json")
	}
	u.Fragment = ""
	p := strings.TrimRight(u.Path, "/")
	p = strings.TrimSuffix(p, "/manifest.json")
	u.Path = strings.TrimRight(p, "/")
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// AddonClient asks subtitle add-ons for subtitles.
type AddonClient struct {
	httpClient *http.Client
}

func NewAddonClient() *AddonClient {
	return &AddonClient{httpClient: netguard.Client(20 * time.Second)}
}

func (c *AddonClient) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return netguard.CleanError(err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return netguard.CleanError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the add-on answered with HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAddonResponse)).Decode(out); err != nil {
		return errors.New("the add-on's answer isn't what a subtitle add-on sends")
	}
	return nil
}

// Manifest reads and checks an add-on's manifest.
func (c *AddonClient) Manifest(ctx context.Context, base string) (AddonManifest, error) {
	var m AddonManifest
	if err := c.getJSON(ctx, base+"/manifest.json", &m); err != nil {
		return m, fmt.Errorf("couldn't read the add-on: %w", err)
	}
	if strings.TrimSpace(m.Name) == "" {
		return m, errors.New("that address isn't a subtitle add-on (it has no name)")
	}
	if !m.ProvidesSubtitles() {
		return m, fmt.Errorf("%q doesn't provide subtitles. Only subtitle add-ons can be added here", m.Name)
	}
	return m, nil
}

// AddonQuery is one title, as an add-on is asked about it.
type AddonQuery struct {
	IMDBID    string // "tt0133093"
	Series    bool
	Season    int
	Episode   int
	Language  string // a Mediarium code like "ro" or "pt-BR"
	Filename  string // the video's file name, when known
	VideoHash string
	VideoSize int64
}

// AddonSubtitle is one subtitle an add-on offers.
type AddonSubtitle struct {
	ID       string
	URL      string
	Language string // as Mediarium names it
	Release  string
	// HearingImpaired and Forced are read from the words in the add-on's names
	// for it: the protocol has no field for them.
	HearingImpaired bool
	Forced          bool
}

type addonResponse struct {
	Subtitles []struct {
		ID       any    `json:"id"`
		URL      string `json:"url"`
		Lang     string `json:"lang"`
		Title    string `json:"title"`
		Name     string `json:"name"`
		Release  string `json:"release"`
		Filename string `json:"filename"`
	} `json:"subtitles"`
}

// Find asks one add-on for the subtitles of a title and keeps those in the
// wanted language.
func (c *AddonClient) Find(ctx context.Context, base string, q AddonQuery) ([]AddonSubtitle, error) {
	if !imdbID.MatchString(q.IMDBID) {
		return nil, nil // no IMDb id to ask with
	}
	kind, id := "movie", q.IMDBID
	if q.Series {
		kind, id = "series", fmt.Sprintf("%s:%d:%d", q.IMDBID, q.Season, q.Episode)
	}
	extra := url.Values{}
	if q.Filename != "" {
		extra.Set("filename", q.Filename)
	}
	if q.VideoHash != "" {
		extra.Set("videoHash", q.VideoHash)
	}
	if q.VideoSize > 0 {
		extra.Set("videoSize", strconv.FormatInt(q.VideoSize, 10))
	}
	target := fmt.Sprintf("%s/subtitles/%s/%s", base, kind, url.PathEscape(id))
	if len(extra) > 0 {
		target += "/" + extra.Encode()
	}
	var resp addonResponse
	if err := c.getJSON(ctx, target+".json", &resp); err != nil {
		return nil, err
	}
	var out []AddonSubtitle
	for _, s := range resp.Subtitles {
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		lang := canonicalLanguage(s.Lang)
		if !addonLanguageMatches(q.Language, lang) {
			continue
		}
		release := firstNonEmpty(s.Release, s.Filename, s.Title, s.Name)
		if release == "" {
			if str, ok := s.ID.(string); ok && !isNumber(str) {
				release = str
			}
		}
		hi, forced := ClassifyName(strings.Join([]string{s.Release, s.Filename, s.Title, s.Name}, " "))
		out = append(out, AddonSubtitle{ID: fmt.Sprint(s.ID), URL: s.URL, Language: lang, Release: release, HearingImpaired: hi, Forced: forced})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Language == q.Language && out[j].Language != q.Language })
	return out, nil
}

var imdbID = regexp.MustCompile(`^tt\d{5,12}$`)

func firstNonEmpty(list ...string) string {
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// canonicalLanguage turns an add-on's language ("ron", "pob", "Romanian") into a
// Mediarium code ("ro", "pt-BR"). An unknown one comes back lower-cased.
func canonicalLanguage(lang string) string {
	l := strings.TrimSpace(lang)
	if c, ok := languageByName[fold(l)]; ok {
		return c
	}
	return strings.ToLower(l)
}

// addonLanguageMatches: the same language, and a plain "pt" fits a wanted
// "pt-BR" or "pt-PT" while a different region does not.
func addonLanguageMatches(wanted, got string) bool {
	w, g := strings.ToLower(wanted), strings.ToLower(got)
	if g == "" {
		return false
	}
	if w == g {
		return true
	}
	return baseLanguage(w) == g || (baseLanguage(g) == g && baseLanguage(w) == g)
}

// Download fetches one subtitle an add-on offered and returns it as UTF-8 SRT
// text. Add-ons usually give a plain .srt; a .vtt is converted and a zip is
// opened. For an episode in a zip with several files, the one named for the
// episode is taken.
func (c *AddonClient) Download(ctx context.Context, rawURL, language string, season, episode int) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("download from the add-on: not a web address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, netguard.CleanError(err)
	}
	req.Header.Set("Accept", "*/*")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download from the add-on: %w", netguard.CleanError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download from the add-on: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAddonFile+1))
	if err != nil {
		return nil, fmt.Errorf("download from the add-on: %w", err)
	}
	if len(data) > maxAddonFile {
		return nil, errors.New("download from the add-on: the file is too big to be a subtitle")
	}
	if bytes.HasPrefix(data, []byte("PK")) {
		if data, err = srtFromZip(data, season, episode); err != nil {
			return nil, err
		}
	}
	data = ToUTF8(data, language)
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("WEBVTT")) {
		data = VTTToSRT(data)
	}
	if len(data) > MaxSubtitleBytes || !looksLikeSRT(data) {
		return nil, errors.New("download from the add-on: that isn't an SRT subtitle")
	}
	return data, nil
}
