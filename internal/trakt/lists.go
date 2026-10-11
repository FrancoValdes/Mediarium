package trakt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// What the scheduled import lists read from Trakt, all of it public: another
// person's list, a public watchlist, and the trending, popular and
// anticipated charts. They need only the client ID, never a sign-in, so a
// private list or a private profile can't be read (Trakt answers 401 or 403).
//
// The calls follow Trakt's API documentation (trakt.docs.apiary.io): the
// headers trakt-api-version and trakt-api-key, optional paging on lists and
// watchlists, required paging on the charts, and 429 with Retry-After when
// too many calls come in.

// UserAgent is sent with every call; the app sets it to include its version.
var UserAgent = "Mediarium (+https://mediarium.app)"

// ErrNotFound is returned when Trakt has no such list or user.
var ErrNotFound = fmt.Errorf("Trakt has no list or profile at that address")

// ErrPrivate is returned when the list or profile is private.
var ErrPrivate = fmt.Errorf("That Trakt list or profile is private. Make it public on Trakt, or use another list")

// RateLimitError is returned when Trakt asks to slow down.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("Trakt asked to slow down. Try again in %d seconds", int(e.RetryAfter.Seconds()))
	}
	return "Trakt asked to slow down. Try again in a few minutes"
}

// Media is one movie or show from a Trakt list or chart.
type Media struct {
	Kind   string // "movie" or "tv"
	Title  string
	Year   int
	TMDBID int
	IMDBID string
}

type mediaBody struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	IDs   struct {
		TMDB int    `json:"tmdb"`
		IMDB string `json:"imdb"`
	} `json:"ids"`
}

func (b *mediaBody) media(kind string) (Media, bool) {
	if b == nil || (b.IDs.TMDB == 0 && b.IDs.IMDB == "") {
		return Media{}, false
	}
	return Media{Kind: kind, Title: b.Title, Year: b.Year, TMDBID: b.IDs.TMDB, IMDBID: b.IDs.IMDB}, true
}

// entry reads both shapes Trakt uses: a wrapper with the movie or show inside
// (lists, watchlists, trending, anticipated) and the bare movie (popular).
type entry struct {
	Type  string     `json:"type"`
	Movie *mediaBody `json:"movie"`
	Show  *mediaBody `json:"show"`
	mediaBody
}

func (e entry) media(chartKind string) (Media, bool) {
	if e.Type != "" && e.Type != "movie" && e.Type != "show" {
		return Media{}, false // a season or an episode (they carry their show too), or a person
	}
	switch {
	case e.Movie != nil:
		return e.Movie.media("movie")
	case e.Show != nil:
		return e.Show.media("tv")
	case chartKind != "":
		return e.mediaBody.media(chartKind)
	}
	return Media{}, false
}

// maxPages stops a runaway answer when a list is paged.
const maxPages = 20

// fetch makes one call and decodes the JSON array it answers with.
func (c *Client) fetch(ctx context.Context, path string, q url.Values) ([]entry, http.Header, error) {
	if c.clientID == "" {
		return nil, nil, ErrNoClientID
	}
	target := c.baseURL + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build trakt request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", c.clientID)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("trakt request %s: %w", path, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, nil, ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		if strings.Contains(path, "/users/") || strings.HasPrefix(path, "/lists/") {
			return nil, nil, ErrPrivate
		}
		return nil, nil, ErrInvalidClientID
	case http.StatusTooManyRequests:
		secs, _ := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
		return nil, nil, &RateLimitError{RetryAfter: time.Duration(secs) * time.Second}
	default:
		return nil, nil, fmt.Errorf("trakt request %s: unexpected status %d", path, resp.StatusCode)
	}
	var items []entry
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIResponse)).Decode(&items); err != nil {
		return nil, nil, fmt.Errorf("decode trakt response %s: %w", path, err)
	}
	return items, resp.Header, nil
}

func collect(items []entry, chartKind string) []Media {
	out := make([]Media, 0, len(items))
	for _, it := range items {
		if m, ok := it.media(chartKind); ok {
			out = append(out, m)
		}
	}
	return out
}

// UserListMedia fetches every movie and show on a user's public list, or on
// a public list by its Trakt number or name when user is empty.
func (c *Client) UserListMedia(ctx context.Context, user, listID string) ([]Media, error) {
	path := fmt.Sprintf("/users/%s/lists/%s/items", url.PathEscape(user), url.PathEscape(listID))
	if user == "" {
		path = fmt.Sprintf("/lists/%s/items", url.PathEscape(listID))
	}
	items, _, err := c.fetch(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	return collect(items, ""), nil
}

// WatchlistMedia fetches the movies and shows on a user's public watchlist.
func (c *Client) WatchlistMedia(ctx context.Context, user string) ([]Media, error) {
	items, _, err := c.fetch(ctx, fmt.Sprintf("/users/%s/watchlist", url.PathEscape(user)), nil)
	if err != nil {
		return nil, err
	}
	return collect(items, ""), nil
}

// Charts Trakt offers for both movies and shows.
const (
	ChartTrending    = "trending"
	ChartPopular     = "popular"
	ChartAnticipated = "anticipated"
)

// ValidChart reports whether chart is one of the Chart* names.
func ValidChart(chart string) bool {
	switch chart {
	case ChartTrending, ChartPopular, ChartAnticipated:
		return true
	}
	return false
}

// ChartMedia fetches the first limit entries (at most 100) of a chart for
// kind "movie" or "tv".
func (c *Client) ChartMedia(ctx context.Context, kind, chart string, limit int) ([]Media, error) {
	if !ValidChart(chart) {
		return nil, fmt.Errorf("trakt chart %q is not one Mediarium knows", chart)
	}
	segment := "movies"
	if kind == "tv" {
		segment = "shows"
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items, _, err := c.fetch(ctx, "/"+segment+"/"+chart, url.Values{"page": {"1"}, "limit": {strconv.Itoa(limit)}})
	if err != nil {
		return nil, err
	}
	return collect(items, kind), nil
}

// ParseWatchlistAddress reads the user name from a Trakt watchlist address
// (https://trakt.tv/users/someone/watchlist) or a bare user name.
func ParseWatchlistAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("Type the Trakt user name or the address of the watchlist")
	}
	if !strings.ContainsAny(raw, "/.") {
		if !slugRe.MatchString(raw) {
			return "", fmt.Errorf("That isn't a Trakt user name")
		}
		return raw, nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("That isn't a Trakt address")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if !isTraktHost(u.Hostname()) || len(parts) < 2 || parts[0] != "users" || !slugRe.MatchString(parts[1]) {
		return "", fmt.Errorf("That isn't a Trakt watchlist address. It looks like https://trakt.tv/users/name/watchlist")
	}
	return parts[1], nil
}

func isTraktHost(h string) bool {
	h = strings.ToLower(h)
	return h == "trakt.tv" || h == "www.trakt.tv" || h == "app.trakt.tv"
}

// ParseAnyListAddress reads a Trakt list address of either kind: a person's
// list (trakt.tv/users/name/lists/slug) or a list by number
// (trakt.tv/lists/2142754). user is empty for the second kind.
func ParseAnyListAddress(raw string) (user, listID string, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", fmt.Errorf("Type the address of the Trakt list")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	if u, perr := url.Parse(trimmed); perr == nil {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if isTraktHost(u.Hostname()) && len(parts) >= 2 && parts[0] == "lists" && slugRe.MatchString(parts[1]) && parts[1] != "official" && parts[1] != "trending" && parts[1] != "popular" {
			return "", parts[1], nil
		}
	}
	user, listID, err = ParseListURL(raw)
	if err != nil {
		return "", "", fmt.Errorf("That isn't a Trakt list address. It looks like https://trakt.tv/users/name/lists/list-name")
	}
	return user, listID, nil
}
