package mediaservers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// KindWebhook is any server or tool without its own support: after imports
// Mediarium sends its address a small JSON message naming the folders that
// changed (after path mapping), batched like the scans of Plex, Jellyfin and
// Emby. The token, when set, is sent as "Authorization: Bearer <token>".
const KindWebhook Kind = "webhook"

// WebhookMessage is what a webhook server receives.
type WebhookMessage struct {
	// Event is "refresh" after imports (Folders empty: scan everything) or
	// "test" for the Test button.
	Event string `json:"event"`
	// Kind is what was imported: movie, tv, music or book ("" for a full
	// refresh or a test).
	Kind    string   `json:"kind,omitempty"`
	Folders []string `json:"folders"`
	Source  string   `json:"source"`
	At      string   `json:"at"`
}

func (c *Client) webhookSend(ctx context.Context, s Server, msg WebhookMessage) error {
	if msg.Folders == nil {
		msg.Folders = []string{}
	}
	msg.Source = "mediarium"
	msg.At = time.Now().UTC().Format(time.RFC3339)
	base, err := NormalizeURL(s.BaseURL)
	if err != nil {
		return userErr(err, "The webhook address is not valid: %v", err)
	}
	req, err := newRequest(ctx, s.Kind, http.MethodPost, base, "", nil, msg)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mediarium/"+c.Version)
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	err = c.exchange(req, s, base, nil)
	var se *statusError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errUnauthorized):
		return userErr(err, "The webhook refused the request (401/403). Check the token.")
	case errors.Is(err, errNotFound):
		return userErr(err, "Nothing was found at that webhook address (404). Check the path.")
	case errors.As(err, &se):
		return userErr(err, "The webhook answered with an error (%s).", se.Error())
	}
	return err
}

func (c *Client) webhookTest(ctx context.Context, s Server) (TestResult, error) {
	if err := c.webhookSend(ctx, s, WebhookMessage{Event: "test"}); err != nil {
		return TestResult{}, err
	}
	return TestResult{ServerName: "Webhook", Libraries: []Library{}}, nil
}

func (c *Client) webhookRefresh(ctx context.Context, s Server, kind MediaKind, folders []string) ([]string, error) {
	mapped := make([]string, 0, len(folders))
	for _, f := range folders {
		mapped = append(mapped, MapPath(f, s.PathMap))
	}
	if err := c.webhookSend(ctx, s, WebhookMessage{Event: "refresh", Kind: string(kind), Folders: mapped}); err != nil {
		return nil, err
	}
	return []string{fmt.Sprintf("sent %d %s", len(mapped), plural(len(mapped), "folder", "folders"))}, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
