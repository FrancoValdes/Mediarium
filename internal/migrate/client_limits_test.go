package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A small answer is limited to a few megabytes, a library list to more; past
// the limit the import says the answer is too big instead of reading on.
func TestReadLimitsAnswerSize(t *testing.T) {
	big := `["` + strings.Repeat("x", maxResponseBytes) + `"]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()
	conn := Conn{URL: srv.URL, APIKey: "k"}

	var out []string
	err := get(context.Background(), srv.Client(), conn, "/api/v3/qualityprofile", nil, false, &out)
	if err == nil || !strings.Contains(err.Error(), "too big") {
		t.Fatalf("small endpoint: %v", err)
	}
	out = nil
	if err := get(context.Background(), srv.Client(), conn, "/api/v3/movie", nil, false, &out); err != nil || len(out) != 1 {
		t.Fatalf("library list: %v", err)
	}
	if err := read(context.Background(), srv.Client(), conn, call{path: "/api", xml: true}, &out); err == nil || !strings.Contains(err.Error(), "too big") {
		t.Fatalf("xml: %v", err)
	}
}

func TestCappedReaderExactLimit(t *testing.T) {
	var out []string
	r := &cappedReader{r: strings.NewReader(`["abc"]`), max: 7}
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		t.Fatalf("exactly the limit: %v", err)
	}
	r = &cappedReader{r: strings.NewReader(`["abcd"]`), max: 7}
	if err := json.NewDecoder(r).Decode(&out); !errors.Is(err, errAnswerTooBig) {
		t.Fatalf("one byte over the limit: %v", err)
	}
}

func TestRedirectsKeepTheScheme(t *testing.T) {
	hc := newHTTPClient()
	mk := func(u string) *http.Request {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	cases := []struct {
		from, to string
		ok       bool
	}{
		{"https://nas:7878/api", "https://nas:7878/radarr/api", true},
		{"http://nas:7878/api", "http://nas:7878/radarr/api", true},
		{"https://nas:7878/api", "http://nas:7878/api", false},
		{"http://nas:7878/api", "https://nas:7878/api", false},
		{"http://nas:7878/api", "http://other:7878/api", false},
	}
	for _, tc := range cases {
		err := hc.CheckRedirect(mk(tc.to), []*http.Request{mk(tc.from)})
		if (err == nil) != tc.ok {
			t.Errorf("%s -> %s: %v", tc.from, tc.to, err)
		}
	}
}
