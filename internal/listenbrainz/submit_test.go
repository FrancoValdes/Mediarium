package listenbrainz_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rdborg/mediarium/internal/listenbrainz"
)

func TestShouldSubmit(t *testing.T) {
	tests := []struct {
		played, length float64
		want           bool
	}{
		{100, 200, true},   // half of a 200 s track
		{99, 200, false},   // just under
		{240, 1200, true},  // four minutes of a long track
		{239, 1200, false}, // just under four minutes
		{30, 60, true},
		{10, 60, false},
		{240, 0, true}, // length unknown: only the four minutes count
		{120, 0, false},
		{0, 100, false},
	}
	for _, tc := range tests {
		if got := listenbrainz.ShouldSubmit(tc.played, tc.length); got != tc.want {
			t.Errorf("ShouldSubmit(%v of %v) = %v, want %v", tc.played, tc.length, got, tc.want)
		}
	}
}

type captured struct {
	method, path, auth, ua string
	body                   map[string]any
}

func fakeServer(t *testing.T, status int, reply string, headers map[string]string) (*listenbrainz.Submitter, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.auth, got.ua = r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		b, _ := io.ReadAll(r.Body)
		if len(b) > 0 {
			if err := json.Unmarshal(b, &got.body); err != nil {
				t.Errorf("body is not JSON: %v", err)
			}
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return listenbrainz.NewSubmitter("9.9", listenbrainz.WithSubmitBaseURL(srv.URL), listenbrainz.WithSubmitHTTPClient(srv.Client())), got
}

var sample = listenbrainz.Listen{
	ArtistName: "The Band", TrackName: "Song", ReleaseName: "Album", ListenedAt: 1700000000, DurationMs: 201000, TrackNumber: 3,
	ArtistMBIDs: []string{"a-mbid"}, ReleaseGroupMBID: "rg-mbid",
}

func TestSubmitOneListenIsASingleWithTheDocumentedShape(t *testing.T) {
	s, got := fakeServer(t, 200, `{"status":"ok"}`, nil)
	if err := s.Submit(context.Background(), "tok-123", []listenbrainz.Listen{sample}); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/1/submit-listens" || got.auth != "Token tok-123" || got.ua != "Mediarium/9.9 ( https://github.com/rdborg/Mediarium )" {
		t.Fatalf("request: %+v", got)
	}
	if got.body["listen_type"] != "single" {
		t.Fatalf("listen_type %v", got.body["listen_type"])
	}
	l := got.body["payload"].([]any)[0].(map[string]any)
	meta := l["track_metadata"].(map[string]any)
	info := meta["additional_info"].(map[string]any)
	if l["listened_at"] != float64(1700000000) || meta["artist_name"] != "The Band" || meta["track_name"] != "Song" || meta["release_name"] != "Album" ||
		info["media_player"] != "Mediarium" || info["submission_client"] != "Mediarium" || info["submission_client_version"] != "9.9" ||
		info["duration_ms"] != float64(201000) || info["tracknumber"] != float64(3) || info["release_group_mbid"] != "rg-mbid" || info["artist_mbids"].([]any)[0] != "a-mbid" {
		t.Fatalf("listen: %+v", l)
	}
}

func TestSeveralListensAreAnImportAndPlayingNowHasNoTime(t *testing.T) {
	s, got := fakeServer(t, 200, `{"status":"ok"}`, nil)
	other := sample
	other.ListenedAt = 1700000300
	if err := s.Submit(context.Background(), "t", []listenbrainz.Listen{sample, other}); err != nil {
		t.Fatal(err)
	}
	if got.body["listen_type"] != "import" || len(got.body["payload"].([]any)) != 2 {
		t.Fatalf("import: %+v", got.body)
	}
	if err := s.PlayingNow(context.Background(), "t", sample); err != nil {
		t.Fatal(err)
	}
	pl := got.body["payload"].([]any)
	if got.body["listen_type"] != "playing_now" || len(pl) != 1 {
		t.Fatalf("playing now: %+v", got.body)
	}
	if _, has := pl[0].(map[string]any)["listened_at"]; has {
		t.Fatal("playing_now must not carry listened_at")
	}
	if err := s.Submit(context.Background(), "t", make([]listenbrainz.Listen, listenbrainz.MaxListensPerSubmit+1)); err == nil {
		t.Fatal("too many listens in one request must be refused")
	}
	if err := s.Submit(context.Background(), "t", nil); err != nil {
		t.Fatalf("nothing to send: %v", err)
	}
}

func TestSubmitErrors(t *testing.T) {
	s, _ := fakeServer(t, 401, `{"code":401,"error":"Invalid authorization token."}`, nil)
	if err := s.Submit(context.Background(), "bad", []listenbrainz.Listen{sample}); !errors.Is(err, listenbrainz.ErrBadToken) {
		t.Fatalf("401: %v", err)
	}
	s, _ = fakeServer(t, 429, `{}`, map[string]string{"Retry-After": "7"})
	var limited *listenbrainz.RateLimitedError
	if err := s.Submit(context.Background(), "t", []listenbrainz.Listen{sample}); !errors.As(err, &limited) || limited.RetryAfter.Seconds() != 7 {
		t.Fatalf("429: %v", err)
	}
	s, _ = fakeServer(t, 400, `{"code":400,"error":"Invalid JSON document submitted."}`, nil)
	if err := s.Submit(context.Background(), "t", []listenbrainz.Listen{sample}); err == nil || errors.Is(err, listenbrainz.ErrBadToken) {
		t.Fatalf("400: %v", err)
	}
}

func TestValidateToken(t *testing.T) {
	s, got := fakeServer(t, 200, `{"code":200,"message":"Token valid.","valid":true,"user_name":"ryan"}`, nil)
	user, valid, err := s.ValidateToken(context.Background(), "tok")
	if err != nil || !valid || user != "ryan" || got.method != "GET" || got.path != "/1/validate-token" || got.auth != "Token tok" {
		t.Fatalf("valid: %q %v %v %+v", user, valid, err, got)
	}
	s, _ = fakeServer(t, 200, `{"code":200,"message":"Token invalid.","valid":false}`, nil)
	if _, valid, err := s.ValidateToken(context.Background(), "nope"); err != nil || valid {
		t.Fatalf("invalid: %v %v", valid, err)
	}
	s, _ = fakeServer(t, 500, `oops`, nil)
	if _, _, err := s.ValidateToken(context.Background(), "t"); err == nil {
		t.Fatal("a server error must be an error, not 'invalid'")
	}
}

func TestTopRecordings(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1/popularity/top-recordings-for-artist/abc-123" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`[{"recording_name":"Closer","total_listen_count":50},{"recording_name":"","total_listen_count":3},{"recording_name":"Hurt","total_listen_count":40}]`))
	})
	got, err := c.TopRecordings(context.Background(), "abc-123")
	if err != nil || len(got) != 2 || got[0].Name != "Closer" || got[1].ListenCount != 40 {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err := c.TopRecordings(context.Background(), ""); err != nil || got != nil {
		t.Fatalf("no artist: %v %v", got, err)
	}
}
