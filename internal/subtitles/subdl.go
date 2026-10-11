package subtitles

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/netguard"
)

// SubDL (subdl.com) is the second place subtitles come from, next to
// OpenSubtitles. It needs a free API key (about 2,000 searches a day) and its
// downloads are zip files, with a limit of a few hundred a day per address.
// It has no file hash, so its subtitles are picked by release name only.

const (
	SourceOpenSubtitles = "" // the zero value: results without a source are OpenSubtitles'
	SourceSubDL         = "subdl"

	subdlAPI      = "https://api.subdl.com/api/v1"
	subdlDownload = "https://dl.subdl.com"
	maxSubDLZip   = 20 << 20
)

// ErrSubDLKey means SubDL turned the API key down.
var ErrSubDLKey = errors.New("SubDL rejected the API key")

// subdlLink is the form of a SubDL download path: "/subtitle/3197651-3213944.zip",
// or one file of a pack, "/subtitle/<id>/<file id>".
var subdlLink = regexp.MustCompile(`^/subtitle/[A-Za-z0-9_-]+(?:\.zip|/[A-Za-z0-9_-]+)$`)

// ValidSubDLLink reports whether link is a SubDL download path.
func ValidSubDLLink(link string) bool { return subdlLink.MatchString(link) }

type SubDL struct {
	apiKey     string
	baseURL    string
	dlURL      string
	httpClient *http.Client
}

func NewSubDL(apiKey string) *SubDL {
	return &SubDL{apiKey: strings.TrimSpace(apiKey), baseURL: subdlAPI, dlURL: subdlDownload, httpClient: netguard.Client(20 * time.Second)}
}

// NewSubDLWithBaseURL points the client at a test server.
func NewSubDLWithBaseURL(apiKey, baseURL, dlURL string) *SubDL {
	c := NewSubDL(apiKey)
	c.baseURL, c.dlURL = baseURL, dlURL
	return c
}

func (c *SubDL) HasAPIKey() bool { return c != nil && c.apiKey != "" }

// subdlLanguage is SubDL's code for one of Mediarium's.
func subdlLanguage(code string) string {
	switch strings.ToLower(code) {
	case "pt-br":
		return "BR_PT"
	case "pt-pt":
		return "PT"
	case "zh-cn", "zh-tw":
		return "ZH"
	}
	base, _, _ := strings.Cut(code, "-")
	return strings.ToUpper(base)
}

// flexInt reads a number SubDL may send as a number, a string or null.
type flexInt int

func (n *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		*n = 0
		return nil
	}
	*n = flexInt(v)
	return nil
}

type subdlResponse struct {
	Status    bool   `json:"status"`
	Error     string `json:"error"`
	Message   string `json:"message"`
	Subtitles []struct {
		ReleaseName string   `json:"release_name"`
		Name        string   `json:"name"`
		URL         string   `json:"url"`
		Season      flexInt  `json:"season"`
		Episode     flexInt  `json:"episode"`
		EpisodeFrom flexInt  `json:"episode_from"`
		EpisodeEnd  flexInt  `json:"episode_end"`
		FullSeason  bool     `json:"full_season"`
		Releases    []string `json:"releases"`
		HI          bool     `json:"hi"`      // asked for with hi=1
		Comment     string   `json:"comment"` // asked for with comment=1
	} `json:"subtitles"`
}

func (c *SubDL) get(ctx context.Context, q url.Values) (subdlResponse, error) {
	var out subdlResponse
	if !c.HasAPIKey() {
		return out, ErrNoAPIKey
	}
	q.Set("api_key", c.apiKey)
	q.Set("client", "custom_integration")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/subtitles?"+q.Encode(), nil)
	if err != nil {
		return out, netguard.CleanError(err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return out, fmt.Errorf("search SubDL: %w", netguard.CleanError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
	if err != nil {
		return out, fmt.Errorf("search SubDL: %w", err)
	}
	_ = json.Unmarshal(body, &out)
	msg := strings.ToLower(out.Error + " " + out.Message)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
		strings.Contains(msg, "api key") || strings.Contains(msg, "api_key") || strings.Contains(msg, "not_authorized") || strings.Contains(msg, "not authorized"):
		return out, ErrSubDLKey
	case resp.StatusCode == http.StatusTooManyRequests:
		return out, errors.New("SubDL says too many searches today; it tries again later")
	case resp.StatusCode != http.StatusOK && !out.Status:
		return out, fmt.Errorf("search SubDL: HTTP %d", resp.StatusCode)
	}
	return out, nil
}

// Find searches SubDL for q. A title SubDL doesn't know is no results, not
// an error.
func (c *SubDL) Find(ctx context.Context, q Query) ([]Result, error) {
	// hi=1 and comment=1 ask SubDL to include the hearing-impaired flag and the
	// uploader's comment, which is where a "forced" track is usually said so.
	v := url.Values{"subs_per_page": {"30"}, "releases": {"1"}, "hi": {"1"}, "comment": {"1"}}
	switch {
	case q.ParentTMDBID > 0:
		v.Set("tmdb_id", strconv.Itoa(q.ParentTMDBID))
		v.Set("type", "tv")
		v.Set("season_number", strconv.Itoa(q.Season))
		v.Set("episode_number", strconv.Itoa(q.Episode))
	case q.TMDBID > 0:
		v.Set("tmdb_id", strconv.Itoa(q.TMDBID))
		v.Set("type", "movie")
	case q.Text != "":
		v.Set("film_name", q.Text)
	default:
		return nil, nil
	}
	if q.Language != "" {
		v.Set("languages", subdlLanguage(q.Language))
	}
	resp, err := c.get(ctx, v)
	if err != nil {
		return nil, err
	}
	if !resp.Status {
		return nil, nil // "can't find movie or tv" and the like
	}
	var out []Result
	for _, s := range resp.Subtitles {
		if !ValidSubDLLink(s.URL) {
			continue
		}
		if q.ParentTMDBID > 0 {
			// Only subtitles for this one episode: a whole-season pack or a
			// different episode isn't what was asked for.
			switch {
			case s.FullSeason, s.EpisodeFrom > 0 && s.EpisodeEnd > s.EpisodeFrom:
				continue
			case s.Season > 0 && int(s.Season) != q.Season, s.Episode > 0 && int(s.Episode) != q.Episode:
				continue
			}
		}
		release := s.ReleaseName
		if release == "" {
			release = strings.TrimSuffix(s.Name, path.Ext(s.Name))
		}
		nameHI, forced := ClassifyName(s.Name + " " + s.ReleaseName + " " + s.Comment)
		out = append(out, Result{
			Language: q.Language, Release: release, Source: SourceSubDL, Link: s.URL, Releases: s.Releases,
			HearingImpaired: s.HI || nameHI, ForeignPartsOnly: forced && !s.HI,
		})
	}
	return out, nil
}

// Test checks the key with a search for a film everyone has.
func (c *SubDL) Test(ctx context.Context) (string, error) {
	if _, err := c.get(ctx, url.Values{"tmdb_id": {"603"}, "type": {"movie"}, "languages": {"EN"}, "subs_per_page": {"1"}}); err != nil {
		return "", err
	}
	return "SubDL accepted the key.", nil
}

// Download fetches a subtitle and returns the SRT inside it. For an episode
// (season > 0) in a zip holding several files, the one named for that
// episode is taken; otherwise the biggest.
func (c *SubDL) Download(ctx context.Context, link string, season, episode int) ([]byte, error) {
	if !ValidSubDLLink(link) {
		return nil, errors.New("download from SubDL: not a SubDL download link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.dlURL+link, nil)
	if err != nil {
		return nil, netguard.CleanError(err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download from SubDL: %w", netguard.CleanError(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("download from SubDL: today's download limit is used up")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("download from SubDL: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubDLZip+1))
	if err != nil {
		return nil, fmt.Errorf("download from SubDL: %w", err)
	}
	if len(data) > maxSubDLZip {
		return nil, errors.New("download from SubDL: the file is too big to be a subtitle")
	}
	if !bytes.HasPrefix(data, []byte("PK")) {
		// One file of a pack comes as it is.
		if len(data) > MaxSubtitleBytes || !looksLikeSRT(data) {
			return nil, errors.New("download from SubDL: that isn't an SRT subtitle")
		}
		return data, nil
	}
	return srtFromZip(data, season, episode)
}

// srtFromZip picks the SRT out of a downloaded zip.
func srtFromZip(data []byte, season, episode int) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("download from SubDL: the zip can't be opened: %w", err)
	}
	var best *zip.File
	bestMatch := false
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() || !strings.EqualFold(path.Ext(name), ".srt") || isSamplePath(name) {
			continue
		}
		if f.UncompressedSize64 > MaxSubtitleBytes {
			continue
		}
		match := false
		if season > 0 {
			if s, eps := episodeOf(name); s == season && containsInt(eps, episode) {
				match = true
			}
		}
		switch {
		case best == nil,
			match && !bestMatch,
			match == bestMatch && f.UncompressedSize64 > best.UncompressedSize64:
			best, bestMatch = f, match
		}
	}
	if best == nil {
		return nil, errors.New("download from SubDL: no SRT subtitle in the zip")
	}
	rc, err := best.Open()
	if err != nil {
		return nil, fmt.Errorf("download from SubDL: %w", err)
	}
	defer rc.Close()
	out, err := io.ReadAll(io.LimitReader(rc, MaxSubtitleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download from SubDL: %w", err)
	}
	if len(out) > MaxSubtitleBytes {
		return nil, errors.New("download from SubDL: the subtitle is too big")
	}
	return out, nil
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// looksLikeSRT checks for the "1 / 00:00:01,000 --> ..." shape near the start.
func looksLikeSRT(b []byte) bool {
	head := b[:min(len(b), 4096)]
	return bytes.Contains(head, []byte("-->"))
}
