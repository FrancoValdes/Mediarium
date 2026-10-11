// Package lrclib looks up song lyrics on LRCLIB (https://lrclib.net), a free
// public lyrics service that needs no account or key. A request names the
// track, artist, album and length; the answer has the words with times (LRC)
// and/or without. Requests say who Mediarium is in the User-Agent, as the
// service asks, and go through the netguard dialer.
//
// The package depends on nothing else in Mediarium except netguard.
package lrclib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/netguard"
)

// DefaultBaseURL is the public LRCLIB server.
const DefaultBaseURL = "https://lrclib.net"

const (
	requestTimeout = 15 * time.Second
	maxBodyBytes   = 2 << 20
	// durationSlack is how far apart (seconds) two lengths of the same song may
	// be. LRCLIB itself matches within two seconds.
	durationSlack = 3
)

// Client talks to one LRCLIB server. It is safe for concurrent use.
type Client struct {
	baseURL   string
	userAgent string
	http      *http.Client
}

// Option changes a Client made by New.
type Option func(*Client)

// WithBaseURL points the client at another server (a mirror, or a test fake).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient replaces the HTTP client (tests).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// UserAgent is the identifying User-Agent sent with every request.
func UserAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return fmt.Sprintf("Mediarium/%s ( https://github.com/rdborg/Mediarium )", version)
}

// New returns a client for the public server that identifies itself as this
// version of Mediarium.
func New(version string, opts ...Option) *Client {
	c := &Client{baseURL: DefaultBaseURL, userAgent: UserAgent(version), http: netguard.Client(requestTimeout)}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Result is what the service has for a track. Found is false when it has
// nothing; Instrumental when the track is known to have no words.
type Result struct {
	Found        bool
	Instrumental bool
	Synced       string // LRC text
	Plain        string
}

type record struct {
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  *string `json:"plainLyrics"`
	SyncedLyrics *string `json:"syncedLyrics"`
}

func (r record) result() Result {
	out := Result{Instrumental: r.Instrumental}
	if r.SyncedLyrics != nil {
		out.Synced = strings.TrimSpace(*r.SyncedLyrics)
	}
	if r.PlainLyrics != nil {
		out.Plain = strings.TrimSpace(*r.PlainLyrics)
	}
	out.Found = out.Synced != "" || out.Plain != "" || out.Instrumental
	return out
}

var errNotFound = errors.New("not found")

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("lrclib: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("lrclib: %w", netguard.CleanError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("lrclib: read answer: %w", err)
	}
	if len(body) > maxBodyBytes {
		return fmt.Errorf("lrclib: answer is larger than %d bytes", maxBodyBytes)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("lrclib %s: decode answer: %w", path, err)
		}
		return nil
	case http.StatusNotFound:
		return errNotFound
	default:
		return fmt.Errorf("lrclib: unexpected status %d", resp.StatusCode)
	}
}

// Lookup asks for the lyrics of one track. durationSec is the track's length
// in seconds (0 when unknown). It first asks for an exact match
// (GET /api/get with the track, artist, album and length); when that finds
// nothing it searches by track and artist and takes the closest length.
// A track the service doesn't have is not an error: Found is false.
func (c *Client) Lookup(ctx context.Context, track, artist, album string, durationSec int) (Result, error) {
	track, artist = strings.TrimSpace(track), strings.TrimSpace(artist)
	if track == "" || artist == "" {
		return Result{}, nil
	}
	q := url.Values{"track_name": {track}, "artist_name": {artist}}
	if a := strings.TrimSpace(album); a != "" {
		q.Set("album_name", a)
	}
	if durationSec > 0 {
		q.Set("duration", strconv.Itoa(durationSec))
	}
	var rec record
	switch err := c.get(ctx, "/api/get", q, &rec); {
	case err == nil:
		if r := rec.result(); r.Found {
			return r, nil
		}
	case !errors.Is(err, errNotFound):
		return Result{}, err
	}

	var list []record
	sq := url.Values{"track_name": {track}, "artist_name": {artist}}
	switch err := c.get(ctx, "/api/search", sq, &list); {
	case err == nil:
	case errors.Is(err, errNotFound):
		return Result{}, nil
	default:
		return Result{}, err
	}
	return best(list, durationSec), nil
}

// best picks, from search results, the one whose length is within the slack
// of the wanted one (any, when the length is unknown), preferring one with
// synced lyrics, then plain lyrics, then an instrumental flag.
func best(list []record, durationSec int) Result {
	var pick Result
	rank := func(r Result) int {
		switch {
		case r.Synced != "":
			return 3
		case r.Plain != "":
			return 2
		case r.Instrumental:
			return 1
		}
		return 0
	}
	bestDelta := math.MaxFloat64
	for _, rec := range list {
		r := rec.result()
		if !r.Found {
			continue
		}
		delta := 0.0
		if durationSec > 0 && rec.Duration > 0 {
			delta = math.Abs(rec.Duration - float64(durationSec))
			if delta > durationSlack {
				continue
			}
		}
		if rank(r) > rank(pick) || (rank(r) == rank(pick) && delta < bestDelta) {
			pick, bestDelta = r, delta
		}
	}
	return pick
}
