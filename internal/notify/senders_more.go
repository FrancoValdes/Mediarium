package notify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
)

// AppriseSender hands the message to an Apprise API server, which passes it on
// to any of the services Apprise knows. With Key it uses a configuration saved
// on that server (POST /notify/{key}); without, it sends the Apprise service
// addresses in URLs along with the message (the stateless POST /notify).
type AppriseSender struct {
	Server   string // base address of the Apprise API, no trailing slash
	Key      string // saved configuration key, or empty
	URLs     string // Apprise service addresses, used when Key is empty
	Tag      string // optional tag to pick destinations
	Username string // optional login for a server behind basic authentication
	Password string
	Client   *http.Client
}

func NewAppriseSender(server, key, urls, tag, username, password string) *AppriseSender {
	return &AppriseSender{
		Server: strings.TrimRight(server, "/"), Key: key, URLs: urls, Tag: tag,
		Username: username, Password: password, Client: newHTTPClient(),
	}
}

// SplitAppriseURLs splits the service addresses a person typed (separated by
// commas, spaces or line breaks) into a clean list.
func SplitAppriseURLs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' })
}

// appriseType maps an event to the notification type Apprise understands, so
// services that colour or icon their messages do it sensibly.
func appriseType(ev Event) string {
	switch EventKind(ev.Type) {
	case EventImported, EventSubtitle:
		return "success"
	case EventFailed:
		return "failure"
	case EventHealth, EventConflict:
		return "warning"
	}
	return "info"
}

func (s *AppriseSender) Send(ctx context.Context, ev Event) error {
	body := map[string]any{"title": ev.Title, "body": ev.Message, "type": appriseType(ev), "format": "text"}
	if ev.rich() {
		var md strings.Builder
		md.WriteString(mdEscape(ev.Lead))
		for _, d := range ev.Details {
			md.WriteString("\n\n**" + mdEscape(d.Label) + ":** " + mdEscape(d.Value))
		}
		if u := httpsURL(ev.LinkURL); u != "" {
			md.WriteString("\n\n[Open in Mediarium](" + u + ")")
		}
		body["body"], body["format"] = md.String(), "markdown"
	}
	if s.Tag != "" {
		body["tag"] = s.Tag
	}
	endpoint := s.Server + "/notify"
	if s.Key != "" {
		endpoint += "/" + url.PathEscape(s.Key)
	} else {
		body["urls"] = strings.Join(SplitAppriseURLs(s.URLs), ",")
	}
	headers := map[string]string{}
	if s.Username != "" {
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(s.Username+":"+s.Password))
	}
	return doJSONPostHeaders(ctx, s.Client, endpoint, body, headers)
}

// MatrixSender posts a message into a Matrix room with the client-server API
// (PUT .../rooms/{roomId}/send/m.room.message/{txnId}). The account the access
// token belongs to must already be in the room.
type MatrixSender struct {
	Homeserver  string
	RoomID      string
	AccessToken string
	Client      *http.Client
}

func NewMatrixSender(homeserver, roomID, accessToken string) *MatrixSender {
	return &MatrixSender{Homeserver: strings.TrimRight(homeserver, "/"), RoomID: roomID, AccessToken: accessToken, Client: newHTTPClient()}
}

// matrixEscape escapes a room id for use as a path segment. The spec's own
// examples write "!" and ":" as %21 and %3A.
func matrixEscape(s string) string {
	return strings.NewReplacer("!", "%21", ":", "%3A").Replace(url.PathEscape(s))
}

// newTxnID is a fresh transaction id. Matrix uses it to ignore a repeated
// request, so every message needs its own.
func newTxnID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("mediarium-%d", len(b))
	}
	return "mediarium-" + hex.EncodeToString(b)
}

func (s *MatrixSender) Send(ctx context.Context, ev Event) error {
	// m.text rather than m.notice: the usual push rules stay quiet for notices,
	// and the point here is that the phone buzzes.
	body := map[string]any{"msgtype": "m.text", "body": ev.Title + "\n" + ev.Message}
	if ev.rich() {
		e := html.EscapeString
		var plain strings.Builder
		plain.WriteString(ev.Title + "\n" + ev.Lead)
		var h strings.Builder
		h.WriteString("<b>" + e(ev.Title) + "</b><br>" + e(oneLine(ev.Lead)))
		for _, d := range ev.Details {
			plain.WriteString("\n" + d.Label + ": " + d.Value)
			h.WriteString("<br><b>" + e(d.Label) + ":</b> " + e(oneLine(d.Value)))
		}
		if u := httpsURL(ev.LinkURL); u != "" {
			plain.WriteString("\n" + u)
			h.WriteString("<br><a href=\"" + e(u) + "\">Open in Mediarium</a>")
		}
		body["body"] = plain.String()
		body["format"], body["formatted_body"] = "org.matrix.custom.html", h.String()
	}
	endpoint := s.Homeserver + "/_matrix/client/v3/rooms/" + matrixEscape(s.RoomID) + "/send/m.room.message/" + newTxnID()
	return doJSONSend(ctx, s.Client, http.MethodPut, endpoint, body, map[string]string{"Authorization": "Bearer " + s.AccessToken})
}

// HomeAssistantSender notifies through Home Assistant: either a notify service
// (POST /api/services/notify/{service}, with a long-lived access token) or a
// webhook (POST /api/webhook/{id}, which needs no token) that an automation
// listens to.
type HomeAssistantSender struct {
	URL       string
	Service   string // notify service name, "notify" for notify.notify
	Token     string
	WebhookID string
	Client    *http.Client
}

func NewHomeAssistantSender(baseURL, service, token, webhookID string) *HomeAssistantSender {
	if service == "" {
		service = "notify"
	}
	return &HomeAssistantSender{URL: strings.TrimRight(baseURL, "/"), Service: service, Token: token, WebhookID: webhookID, Client: newHTTPClient()}
}

func (s *HomeAssistantSender) Send(ctx context.Context, ev Event) error {
	if s.WebhookID != "" {
		payload := map[string]any{
			"event":   ev.Type,
			"title":   ev.Title,
			"message": ev.Message,
		}
		if !ev.Timestamp.IsZero() {
			payload["at"] = ev.Timestamp.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		if ev.rich() {
			payload["details"] = ev.Details
			if u := httpsURL(ev.LinkURL); u != "" {
				payload["link"] = u
			}
			if u := httpsURL(ev.PosterURL); u != "" {
				payload["poster"] = u
			}
		}
		return doJSONPost(ctx, s.Client, s.URL+"/api/webhook/"+url.PathEscape(s.WebhookID), payload)
	}
	body := map[string]any{"title": ev.Title, "message": ev.Message}
	return doJSONPostHeaders(ctx, s.Client, s.URL+"/api/services/notify/"+url.PathEscape(s.Service), body,
		map[string]string{"Authorization": "Bearer " + s.Token})
}
