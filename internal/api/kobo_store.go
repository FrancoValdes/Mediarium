package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// A Kobo that syncs with Mediarium still talks to Kobo's own store for
// everything Mediarium does not do: signing in, its store pages, purchases,
// the books it bought there. It has been told (api_endpoint) that Mediarium is
// the store, so those calls arrive here, and Mediarium passes them on to
// storeapi.kobo.com exactly as the Kobo sent them and hands back the answer.
//
// What is passed on is only what the Kobo itself sent (its own sign-in token
// and device details), to the one fixed Kobo address, and it is not kept or
// logged. The account's Mediarium token is never sent. If Kobo's servers
// cannot be reached, store calls are answered with an empty "{}", which the
// device accepts, and only the Mediarium library keeps working.

// koboSkipHeaders are the headers of the store's answer that are not handed on
// to the Kobo: hop-by-hop ones, and ones that would describe the wrong
// connection. (The store is one fixed, known address, so its answers are
// filtered with a block list.)
var koboSkipHeaders = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true, "Proxy-Authorization": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "Host": true,
	"Accept-Encoding": true, "Content-Length": true, "Content-Encoding": true,
	"X-Forwarded-For": true, "X-Forwarded-Host": true, "X-Forwarded-Proto": true, "X-Forwarded-Port": true,
	"X-Real-Ip": true, "Forwarded": true, "X-Api-Key": true, "Cookie": true,
}

// koboRequestHeaders are the only headers of the Kobo's request that go on to
// the store. A list of what is allowed, not of what is refused: a reverse
// proxy in front of Mediarium adds headers of its own (who is signed in, the
// visitor's address, a tunnel's ray id, and so on) and none of that may reach
// Kobo. Accept-Encoding and Content-Length are left out on purpose: the
// request to the store sets its own, and the answer is decoded here.
var koboRequestHeaders = map[string]bool{
	"Authorization": true, "User-Agent": true, "Accept": true, "Accept-Language": true,
	"Content-Type": true,
}

// koboForwardsHeader says whether a header of the Kobo's request goes on to the
// store: the ones listed above, and the Kobo's own X-Kobo-* headers (device id,
// sync token, user key and so on).
func koboForwardsHeader(name string) bool {
	name = http.CanonicalHeaderKey(name)
	return koboRequestHeaders[name] || strings.HasPrefix(name, "X-Kobo-")
}

const (
	koboBodyLimit  = 8 << 20
	koboReplyLimit = 64 << 20
)

// koboStore returns the address and client used for the Kobo store.
func (s *Server) koboStore() (string, *http.Client) {
	rt := s.ereaderState()
	rt.storeMu.RLock()
	defer rt.storeMu.RUnlock()
	return rt.storeBase, rt.storeHTTP
}

// koboStoreCall sends the Kobo's request on to the store. body is what the
// Kobo sent (already read), path is the store path ("/v1/...").
func (s *Server) koboStoreCall(ctx context.Context, r *http.Request, path string, body []byte, timeout time.Duration) (*http.Response, error) {
	base, client := s.koboStore()
	target := base + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	var rd io.Reader
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, rd)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("build store request: %w", err)
	}
	for k, vs := range r.Header {
		if !koboForwardsHeader(k) {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("ask the Kobo store: %w", err)
	}
	// Release the timeout when the caller is done with the body.
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func readKoboBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(r.Body, koboBodyLimit))
	return data
}

// koboForward passes a request the Mediarium library has nothing to say about
// on to the Kobo store and hands back its answer.
func (s *Server) koboForward(w http.ResponseWriter, r *http.Request, path string) {
	body := readKoboBody(r)
	resp, err := s.koboStoreCall(r.Context(), r, path, body, 25*time.Second)
	if err != nil {
		slog.Info("kobo: the store could not be reached", "method", r.Method)
		writeKoboOffline(w, r, path)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if koboSkipHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, io.LimitReader(resp.Body, koboReplyLimit))
	}
}

// writeKoboOffline answers a store call that could not be passed on. The
// Kobo store pages (wish list, deals, recommendations) get the empty list they
// expect; anything else gets "{}".
func writeKoboOffline(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case strings.HasSuffix(path, "/loyalty/benefits"):
		writeJSON(w, http.StatusOK, map[string]any{"Benefits": map[string]any{}})
	case strings.HasSuffix(path, "/analytics/gettests"):
		writeJSON(w, http.StatusOK, map[string]any{"Result": "Success", "TestKey": r.Header.Get("X-Kobo-Userkey"), "Tests": map[string]any{}})
	case strings.HasSuffix(path, "/prices"), strings.HasSuffix(path, "/user/wishlist"), strings.HasSuffix(path, "/deals"),
		strings.HasSuffix(path, "/products/books/subscriptions"):
		writeJSON(w, http.StatusOK, map[string]any{"Items": []any{}})
	case strings.HasSuffix(path, "/user/recommendations"):
		writeJSON(w, http.StatusOK, []any{})
	default:
		writeKoboEmpty(w)
	}
}

// writeKoboEmpty is the answer for a store call that could not be passed on.
func writeKoboEmpty(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{})
}

// koboAuthFallback answers a sign-in call when the store cannot be reached:
// the Kobo gets tokens that are good for talking to Mediarium, which does not
// check them.
func koboAuthFallback(w http.ResponseWriter, body []byte) {
	random := func() string {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	var in struct {
		UserKey string `json:"UserKey"`
	}
	_ = json.Unmarshal(body, &in)
	writeJSON(w, http.StatusOK, map[string]any{
		"AccessToken":  random(),
		"RefreshToken": random(),
		"TokenType":    "Bearer",
		"TrackingId":   newUUIDv4(),
		"UserKey":      in.UserKey,
	})
}

func newUUIDv4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// koboStoreResources asks the store for its own /v1/initialization answer, so
// the Kobo gets the store addresses for its country. nil when it can't be asked.
func (s *Server) koboStoreResources(r *http.Request) map[string]any {
	resp, err := s.koboStoreCall(r.Context(), r, "/v1/initialization", nil, 8*time.Second)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var out struct {
		Resources map[string]any `json:"Resources"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil || len(out.Resources) == 0 {
		return nil
	}
	return out.Resources
}

// koboStoreSync asks the store for the books the Kobo bought there, so its
// library is not only Mediarium's. rawToken is the store's own sync token from
// the last time. It returns the items, the store's new token and the headers
// the Kobo should see; ok is false when the store gave nothing.
func (s *Server) koboStoreSync(r *http.Request, rawToken string) (items []json.RawMessage, newToken string, header http.Header, ok bool) {
	// Not signed in to Kobo: there is no store library to ask for.
	if r.Header.Get("Authorization") == "" {
		return nil, "", nil, false
	}
	req := r.Clone(r.Context())
	req.Header = r.Header.Clone()
	req.Header.Del("X-Kobo-Synctoken")
	if rawToken != "" {
		req.Header.Set("x-kobo-synctoken", rawToken)
	}
	resp, err := s.koboStoreCall(r.Context(), req, "/v1/library/sync", nil, 25*time.Second)
	if err != nil {
		return nil, "", nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", nil, false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, koboReplyLimit))
	if err != nil {
		return nil, "", nil, false
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, "", nil, false
	}
	header = http.Header{}
	for _, k := range []string{"X-Kobo-Sync", "X-Kobo-Sync-Mode", "X-Kobo-Recent-Reads"} {
		if v := resp.Header.Get(k); v != "" {
			header.Set(k, v)
		}
	}
	return items, strings.TrimSpace(resp.Header.Get("X-Kobo-Synctoken")), header, true
}
