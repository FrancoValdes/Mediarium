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
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/netguard"
)

// Wizdom (wizdom.xyz) is a Hebrew subtitle site. Its public JSON API needs no
// account and no key. It is Hebrew only, and it is looked up by IMDb id:
//
//	GET /api/releases/<imdb id>   the title, with every subtitle it has
//	GET /api/files/sub/<id>       one subtitle, as a zip
//
// For a film, "subs" in the answer is a list. For a series it is an object
// of seasons, each an object of episodes, each a list: subs["1"]["2"] is season
// 1 episode 2. A title the site doesn't know answers HTTP 500.
//
// Ktuvit, the other big Hebrew site, has no such API (see docs/subtitles.md),
// so it is only reachable through a subtitle add-on.

const (
	// SourceWizdom marks a result that came from Wizdom.
	SourceWizdom = "wizdom"

	wizdomAPI       = "https://wizdom.xyz/api"
	wizdomUserAgent = "Mediarium (+https://mediarium.app)"
	maxWizdomZip    = 20 << 20
)

// wizdomLink is the form of a Wizdom subtitle link: its numeric id.
var wizdomLink = regexp.MustCompile(`^\d{1,10}$`)

// ValidWizdomLink reports whether link is a Wizdom subtitle id.
func ValidWizdomLink(link string) bool { return wizdomLink.MatchString(link) }

// Wizdom asks wizdom.xyz for Hebrew subtitles.
type Wizdom struct {
	baseURL    string
	httpClient *http.Client
}

func NewWizdom() *Wizdom {
	return &Wizdom{baseURL: wizdomAPI, httpClient: netguard.Client(20 * time.Second)}
}

// NewWizdomWithBaseURL points the client at a test server.
func NewWizdomWithBaseURL(baseURL string) *Wizdom {
	c := NewWizdom()
	c.baseURL = strings.TrimRight(baseURL, "/")
	return c
}

// WizdomQuery is one title, as Wizdom is asked about it.
type WizdomQuery struct {
	IMDBID   string // "tt0133093": for a series, the series' id
	Series   bool
	Season   int
	Episode  int
	Language string // a Mediarium code; anything but Hebrew gets no results
}

// WizdomLanguage reports whether Wizdom has subtitles in a language: Hebrew
// only.
func WizdomLanguage(code string) bool { return baseLanguage(strings.ToLower(code)) == "he" }

type wizdomEntry struct {
	ID      flexInt `json:"id"`
	Version string  `json:"version"`
}

type wizdomTitle struct {
	Subs json.RawMessage `json:"subs"`
}

func (c *Wizdom) newRequest(ctx context.Context, target string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, netguard.CleanError(err)
	}
	req.Header.Set("User-Agent", wizdomUserAgent)
	return req, nil
}

// releases reads the answer for one IMDb id. A title Wizdom doesn't have (HTTP
// 500, 404) is no answer, not an error.
func (c *Wizdom) releases(ctx context.Context, imdb string) (*wizdomTitle, error) {
	req, err := c.newRequest(ctx, c.baseURL+"/releases/"+url.PathEscape(imdb))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search Wizdom: %w", netguard.CleanError(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusInternalServerError, resp.StatusCode == http.StatusNotFound:
		return nil, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("Wizdom says too many searches; it tries again later")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("search Wizdom: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
	if err != nil {
		return nil, fmt.Errorf("search Wizdom: %w", err)
	}
	var t wizdomTitle
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, errors.New("search Wizdom: its answer isn't what the site sends")
	}
	return &t, nil
}

// wizdomStep goes one level down: an object by key, or a list by position.
func wizdomStep(node any, key int) any {
	switch n := node.(type) {
	case map[string]any:
		return n[strconv.Itoa(key)]
	case []any:
		if key >= 0 && key < len(n) {
			return n[key]
		}
	}
	return nil
}

// wizdomEntries picks the subtitle list out of "subs": the list itself for a
// film, season then episode for a series.
func wizdomEntries(subs json.RawMessage, series bool, season, episode int) []wizdomEntry {
	var node any
	if len(subs) == 0 || json.Unmarshal(subs, &node) != nil {
		return nil
	}
	if series {
		node = wizdomStep(wizdomStep(node, season), episode)
	}
	list, ok := node.([]any)
	if !ok {
		return nil
	}
	var out []wizdomEntry
	for _, item := range list {
		b, err := json.Marshal(item)
		if err != nil {
			continue
		}
		var e wizdomEntry
		if json.Unmarshal(b, &e) == nil && e.ID > 0 {
			out = append(out, e)
		}
	}
	return out
}

// Find lists Wizdom's Hebrew subtitles for a title. Any other language, or a
// title without an IMDb id, gets no results.
func (c *Wizdom) Find(ctx context.Context, q WizdomQuery) ([]Result, error) {
	if !WizdomLanguage(q.Language) || !imdbID.MatchString(q.IMDBID) {
		return nil, nil
	}
	t, err := c.releases(ctx, q.IMDBID)
	if err != nil || t == nil {
		return nil, err
	}
	var out []Result
	seen := map[flexInt]bool{}
	for _, e := range wizdomEntries(t.Subs, q.Series, q.Season, q.Episode) {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		release := strings.TrimLeft(strings.TrimSpace(e.Version), ":. ")
		hi, forced := ClassifyName(release)
		out = append(out, Result{
			Language: "he", Release: release, Source: SourceWizdom, Link: strconv.Itoa(int(e.ID)),
			HearingImpaired: hi, ForeignPartsOnly: forced && !hi,
		})
	}
	return out, nil
}

// Test checks that Wizdom answers, with a search for a film everyone has.
func (c *Wizdom) Test(ctx context.Context) (string, error) {
	t, err := c.releases(ctx, "tt0133093")
	if err != nil {
		return "", err
	}
	if t == nil {
		return "", errors.New("Wizdom answered, but without the subtitles it should have")
	}
	return "Wizdom answered.", nil
}

// Download fetches one subtitle and returns it as UTF-8 SRT text. Wizdom sends
// a zip; its files are windows-1255 (the old Hebrew code page) or UTF-8, and
// some zips hold both, in which case the bigger one (UTF-8 is, for Hebrew) is
// taken. The text itself, right to left marks included, is left as it is.
func (c *Wizdom) Download(ctx context.Context, link string, season, episode int) ([]byte, error) {
	if !ValidWizdomLink(link) {
		return nil, errors.New("download from Wizdom: not a Wizdom subtitle")
	}
	req, err := c.newRequest(ctx, c.baseURL+"/files/sub/"+link)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download from Wizdom: %w", netguard.CleanError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download from Wizdom: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWizdomZip+1))
	if err != nil {
		return nil, fmt.Errorf("download from Wizdom: %w", err)
	}
	if len(data) > maxWizdomZip {
		return nil, errors.New("download from Wizdom: the file is too big to be a subtitle")
	}
	if bytes.HasPrefix(data, []byte("PK")) {
		if data, err = srtFromZip(data, season, episode); err != nil {
			return nil, errors.New(strings.Replace(err.Error(), "SubDL", "Wizdom", 1))
		}
	}
	data = ToUTF8(data, "he")
	if len(data) > MaxSubtitleBytes || !looksLikeSRT(data) {
		return nil, errors.New("download from Wizdom: that isn't an SRT subtitle")
	}
	return data, nil
}
