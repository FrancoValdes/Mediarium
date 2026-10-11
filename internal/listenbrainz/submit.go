package listenbrainz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/netguard"
)

// This file is the other direction: sending what someone plays to their own
// ListenBrainz account with their own user token
// (https://listenbrainz.readthedocs.io/en/latest/users/api/core.html).
// The token is only ever sent in the Authorization header to the ListenBrainz
// server and is never logged.

const (
	submitTimeout = 20 * time.Second
	maxSubmitBody = 1 << 20
	// MaxListensPerSubmit is how many listens one request carries; the
	// service allows a thousand.
	MaxListensPerSubmit = 100
	// MediaPlayer and SubmissionClient name this app in what is sent.
	mediaPlayer = "Mediarium"
)

// ErrBadToken is returned when ListenBrainz doesn't accept the token (401).
var ErrBadToken = errors.New("ListenBrainz didn't accept the token")

// RateLimitedError is returned when ListenBrainz asks the caller to slow down
// (429). RetryAfter is how long it says to wait (zero when it doesn't say).
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string { return "ListenBrainz is asking for fewer requests" }

// Listen is one play. ListenedAt is the Unix time the play started.
type Listen struct {
	ArtistName       string
	TrackName        string
	ReleaseName      string
	ListenedAt       int64
	DurationMs       int
	TrackNumber      int
	ArtistMBIDs      []string
	ReleaseGroupMBID string
}

// ShouldSubmit applies ListenBrainz's rule for when a play counts: half of
// the track or four minutes, whichever is less. durationSec is the track's
// length (0 when unknown, then only the four minutes count) and playedSec how
// long it has really been played.
func ShouldSubmit(playedSec, durationSec float64) bool {
	need := 240.0
	if durationSec > 0 && durationSec/2 < need {
		need = durationSec / 2
	}
	return playedSec >= need
}

// Submitter sends plays to one ListenBrainz server.
type Submitter struct {
	baseURL       string
	userAgent     string
	clientVersion string
	http          *http.Client
}

// SubmitOption changes a Submitter made by NewSubmitter.
type SubmitOption func(*Submitter)

// WithSubmitBaseURL points the submitter at another server (a fake in tests).
func WithSubmitBaseURL(u string) SubmitOption {
	return func(s *Submitter) { s.baseURL = strings.TrimRight(u, "/") }
}

// WithSubmitHTTPClient replaces the HTTP client (tests).
func WithSubmitHTTPClient(h *http.Client) SubmitOption { return func(s *Submitter) { s.http = h } }

// NewSubmitter returns a submitter for the public server that identifies
// itself as this version of Mediarium.
func NewSubmitter(version string, opts ...SubmitOption) *Submitter {
	if version == "" {
		version = "dev"
	}
	s := &Submitter{baseURL: DefaultBaseURL, userAgent: UserAgent(version), clientVersion: version, http: netguard.Client(submitTimeout)}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Submitter) do(ctx context.Context, method, path, token string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("listenbrainz: encode request: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, rd)
	if err != nil {
		return fmt.Errorf("listenbrainz: build request: %w", err)
	}
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("listenbrainz: %w", netguard.CleanError(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubmitBody))
	if err != nil {
		return fmt.Errorf("listenbrainz: read answer: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrBadToken
	case resp.StatusCode == http.StatusTooManyRequests:
		wait := time.Duration(0)
		if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && n > 0 {
			wait = time.Duration(n) * time.Second
		} else if n, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Reset-In")); err == nil && n > 0 {
			wait = time.Duration(n) * time.Second
		}
		return &RateLimitedError{RetryAfter: wait}
	case resp.StatusCode != http.StatusOK:
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error != "" {
			return fmt.Errorf("listenbrainz: status %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("listenbrainz: unexpected status %d", resp.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("listenbrainz %s: decode answer: %w", path, err)
		}
	}
	return nil
}

// ValidateToken asks whether a user token is accepted and whose it is
// (GET /1/validate-token). An invalid token is valid=false with no error.
func (s *Submitter) ValidateToken(ctx context.Context, token string) (user string, valid bool, err error) {
	var out struct {
		Valid    bool   `json:"valid"`
		UserName string `json:"user_name"`
	}
	if err := s.do(ctx, http.MethodGet, "/1/validate-token", token, nil, &out); err != nil {
		if errors.Is(err, ErrBadToken) {
			return "", false, nil
		}
		return "", false, err
	}
	return out.UserName, out.Valid, nil
}

type lbMetadata struct {
	ArtistName  string         `json:"artist_name"`
	TrackName   string         `json:"track_name"`
	ReleaseName string         `json:"release_name,omitempty"`
	Additional  map[string]any `json:"additional_info"`
}

type lbListen struct {
	ListenedAt int64      `json:"listened_at,omitempty"`
	Metadata   lbMetadata `json:"track_metadata"`
}

func (s *Submitter) encode(l Listen) lbListen {
	info := map[string]any{
		"media_player":              mediaPlayer,
		"submission_client":         mediaPlayer,
		"submission_client_version": s.clientVersion,
	}
	if l.DurationMs > 0 {
		info["duration_ms"] = l.DurationMs
	}
	if l.TrackNumber > 0 {
		info["tracknumber"] = l.TrackNumber
	}
	if len(l.ArtistMBIDs) > 0 {
		info["artist_mbids"] = l.ArtistMBIDs
	}
	if l.ReleaseGroupMBID != "" {
		info["release_group_mbid"] = l.ReleaseGroupMBID
	}
	return lbListen{
		ListenedAt: l.ListenedAt,
		Metadata:   lbMetadata{ArtistName: l.ArtistName, TrackName: l.TrackName, ReleaseName: l.ReleaseName, Additional: info},
	}
}

// PlayingNow tells ListenBrainz what is playing right now. It is only shown
// for a while and doesn't count as a listen.
func (s *Submitter) PlayingNow(ctx context.Context, token string, l Listen) error {
	l.ListenedAt = 0
	body := map[string]any{"listen_type": "playing_now", "payload": []lbListen{s.encode(l)}}
	return s.do(ctx, http.MethodPost, "/1/submit-listens", token, body, nil)
}

// Submit records finished plays. One play is sent as a "single", several as an
// "import" (at most MaxListensPerSubmit at a time).
func (s *Submitter) Submit(ctx context.Context, token string, listens []Listen) error {
	if len(listens) == 0 {
		return nil
	}
	if len(listens) > MaxListensPerSubmit {
		return fmt.Errorf("listenbrainz: at most %d listens per request", MaxListensPerSubmit)
	}
	payload := make([]lbListen, 0, len(listens))
	for _, l := range listens {
		payload = append(payload, s.encode(l))
	}
	kind := "single"
	if len(payload) > 1 {
		kind = "import"
	}
	return s.do(ctx, http.MethodPost, "/1/submit-listens", token, map[string]any{"listen_type": kind, "payload": payload}, nil)
}
