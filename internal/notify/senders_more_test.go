package notify_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/notify"
)

// richEvent is an event as Compose builds it, with the extras.
var richEvent = notify.Event{
	Type: "imported", Title: "Night Harbour (1997) is ready to watch", Message: "It joined your library.\nQuality: 1080p",
	Timestamp: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	Lead:      "It joined your library.",
	Details:   []notify.Detail{{Label: "Quality", Value: "1080p"}, {Label: "Size", Value: "4.2 GB"}},
	LinkURL:   "https://media.example.com/movie/7",
	PosterURL: "https://image.tmdb.org/poster.jpg",
}

// requestLog records the last request a fake server saw.
type requestLog struct {
	method, uri, path string
	headers           http.Header
	body              string
}

func fakeServer(t *testing.T, status int, reply string) (*httptest.Server, *requestLog) {
	t.Helper()
	got := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.uri, got.path, got.headers, got.body = r.Method, r.RequestURI, r.URL.Path, r.Header.Clone(), string(b)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestAppriseStateless(t *testing.T) {
	srv, got := fakeServer(t, 200, `{"error": null}`)
	s := notify.NewAppriseSender(srv.URL+"/", "", "discord://id/tok, mailto://u:p@gmail.com\ntgram://bot/chat", "", "", "")
	if err := s.Send(context.Background(), fixtureEvent); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/notify" {
		t.Fatalf("expected POST /notify, got %s %s", got.method, got.path)
	}
	if got.headers.Get("Content-Type") != "application/json" || got.headers.Get("Authorization") != "" {
		t.Fatalf("unexpected headers %v", got.headers)
	}
	body := decode(t, got.body)
	if body["urls"] != "discord://id/tok,mailto://u:p@gmail.com,tgram://bot/chat" {
		t.Fatalf("urls should be one comma list, got %v", body["urls"])
	}
	if body["title"] != fixtureEvent.Title || body["body"] != fixtureEvent.Message || body["type"] != "success" || body["format"] != "text" {
		t.Fatalf("unexpected body %+v", body)
	}
	if _, has := body["tag"]; has {
		t.Fatal("no tag was given")
	}
}

func TestAppriseKeyTagAndLogin(t *testing.T) {
	srv, got := fakeServer(t, 200, "")
	s := notify.NewAppriseSender(srv.URL, "my-config_1", "", "mediarium,family", "ann", "pw")
	if err := s.Send(context.Background(), richEvent); err != nil {
		t.Fatal(err)
	}
	if got.path != "/notify/my-config_1" {
		t.Fatalf("expected the saved configuration path, got %s", got.path)
	}
	body := decode(t, got.body)
	if _, has := body["urls"]; has {
		t.Fatal("a saved configuration must not also send urls")
	}
	if body["tag"] != "mediarium,family" || body["format"] != "markdown" {
		t.Fatalf("unexpected body %+v", body)
	}
	text, _ := body["body"].(string)
	if !strings.Contains(text, "**Quality:** 1080p") || !strings.Contains(text, "[Open in Mediarium](https://media.example.com/movie/7)") {
		t.Fatalf("markdown body is missing details: %q", text)
	}
	if got.headers.Get("Authorization") != "Basic YW5uOnB3" {
		t.Fatalf("expected basic auth, got %q", got.headers.Get("Authorization"))
	}
}

func TestAppriseTypeByEvent(t *testing.T) {
	cases := map[string]string{"failed": "failure", "health": "warning", "conflict": "warning", "imported": "success", "grabbed": "info", "update": "info"}
	for typ, want := range cases {
		srv, got := fakeServer(t, 200, "")
		ev := fixtureEvent
		ev.Type = typ
		if err := notify.NewAppriseSender(srv.URL, "k", "", "", "", "").Send(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
		if g := decode(t, got.body)["type"]; g != want {
			t.Errorf("%s: type %v, want %s", typ, g, want)
		}
	}
}

func TestAppriseFailureIsReported(t *testing.T) {
	srv, _ := fakeServer(t, 424, "")
	err := notify.NewAppriseSender(srv.URL, "", "discord://a/b", "", "", "").Send(context.Background(), fixtureEvent)
	if err == nil || !strings.Contains(err.Error(), "424") {
		t.Fatalf("expected a 424 error, got %v", err)
	}
}

func TestMatrixSender(t *testing.T) {
	srv, got := fakeServer(t, 200, `{"event_id":"$abc"}`)
	s := notify.NewMatrixSender(srv.URL+"/", "!AbCdEf:example.org", "syt_secret")
	if err := s.Send(context.Background(), richEvent); err != nil {
		t.Fatal(err)
	}
	if got.method != "PUT" {
		t.Fatalf("Matrix sends with PUT, got %s", got.method)
	}
	if !strings.HasPrefix(got.uri, "/_matrix/client/v3/rooms/%21AbCdEf%3Aexample.org/send/m.room.message/") {
		t.Fatalf("unexpected path %s", got.uri)
	}
	txn1 := got.uri[strings.LastIndex(got.uri, "/")+1:]
	if len(txn1) < 10 {
		t.Fatalf("transaction id too short: %q", txn1)
	}
	if got.headers.Get("Authorization") != "Bearer syt_secret" {
		t.Fatalf("expected a bearer token, got %q", got.headers.Get("Authorization"))
	}
	body := decode(t, got.body)
	if body["msgtype"] != "m.text" || body["format"] != "org.matrix.custom.html" {
		t.Fatalf("unexpected body %+v", body)
	}
	if h, _ := body["formatted_body"].(string); !strings.Contains(h, "<b>Quality:</b> 1080p") || !strings.Contains(h, `<a href="https://media.example.com/movie/7">`) {
		t.Fatalf("formatted body %q", h)
	}
	if p, _ := body["body"].(string); !strings.HasPrefix(p, richEvent.Title+"\n") || !strings.Contains(p, "Quality: 1080p") {
		t.Fatalf("plain body %q", p)
	}
	// A second message needs a different transaction id, or the server drops it.
	if err := s.Send(context.Background(), richEvent); err != nil {
		t.Fatal(err)
	}
	if txn2 := got.uri[strings.LastIndex(got.uri, "/")+1:]; txn2 == txn1 {
		t.Fatal("transaction ids must not repeat")
	}
}

func TestMatrixPlainEventAndHTMLEscaping(t *testing.T) {
	srv, got := fakeServer(t, 200, "{}")
	ev := notify.Event{Type: "failed", Title: "<script>x</script>", Message: "plain"}
	if err := notify.NewMatrixSender(srv.URL, "!r:example.org", "t").Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	body := decode(t, got.body)
	if _, has := body["formatted_body"]; has {
		t.Fatal("a plain event has no formatted body")
	}
	srv2, got2 := fakeServer(t, 200, "{}")
	ev.Lead, ev.Title = "a < b", "<b>hi</b>"
	if err := notify.NewMatrixSender(srv2.URL, "!r:example.org", "t").Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if h, _ := decode(t, got2.body)["formatted_body"].(string); strings.Contains(h, "<b>hi</b>") || !strings.Contains(h, "a &lt; b") {
		t.Fatalf("html must be escaped, got %q", h)
	}
}

func TestMatrixErrorsAreExplained(t *testing.T) {
	srv, _ := fakeServer(t, 403, `{"errcode":"M_FORBIDDEN","error":"You are not in the room"}`)
	err := notify.NewMatrixSender(srv.URL, "!r:example.org", "SECRETTOKEN").Send(context.Background(), fixtureEvent)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected a 403 error, got %v", err)
	}
	if strings.Contains(err.Error(), "SECRETTOKEN") || strings.Contains(err.Error(), "r:example.org") {
		t.Fatalf("error leaks request details: %v", err)
	}
}

func TestHomeAssistantService(t *testing.T) {
	srv, got := fakeServer(t, 200, `[]`)
	s := notify.NewHomeAssistantSender(srv.URL+"/", "mobile_app_pixel", "llat_secret", "")
	if err := s.Send(context.Background(), richEvent); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/api/services/notify/mobile_app_pixel" {
		t.Fatalf("unexpected request %s %s", got.method, got.path)
	}
	if got.headers.Get("Authorization") != "Bearer llat_secret" {
		t.Fatalf("expected a bearer token, got %q", got.headers.Get("Authorization"))
	}
	body := decode(t, got.body)
	if body["title"] != richEvent.Title || body["message"] != richEvent.Message {
		t.Fatalf("unexpected body %+v", body)
	}
}

func TestHomeAssistantDefaultServiceAndWebhook(t *testing.T) {
	srv, got := fakeServer(t, 200, "")
	if err := notify.NewHomeAssistantSender(srv.URL, "", "tok", "").Send(context.Background(), fixtureEvent); err != nil {
		t.Fatal(err)
	}
	if got.path != "/api/services/notify/notify" {
		t.Fatalf("an empty service means notify.notify, got %s", got.path)
	}

	if err := notify.NewHomeAssistantSender(srv.URL, "ignored", "", "hook-1_ID").Send(context.Background(), richEvent); err != nil {
		t.Fatal(err)
	}
	if got.path != "/api/webhook/hook-1_ID" || got.headers.Get("Authorization") != "" {
		t.Fatalf("a webhook needs no token: %s %v", got.path, got.headers)
	}
	body := decode(t, got.body)
	if body["event"] != "imported" || body["title"] != richEvent.Title || body["link"] != richEvent.LinkURL || body["at"] != "2024-01-02T03:04:05Z" {
		t.Fatalf("unexpected webhook body %+v", body)
	}
}

func TestHomeAssistantUnknownServiceIs404(t *testing.T) {
	srv, _ := fakeServer(t, 404, "")
	err := notify.NewHomeAssistantSender(srv.URL, "nope", "t", "").Send(context.Background(), fixtureEvent)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected a 404 error, got %v", err)
	}
}

func TestNewTypesValidation(t *testing.T) {
	cases := []struct {
		name string
		typ  string
		cfg  map[string]string
		want string // substring of the error, or "" for valid
	}{
		{"apprise urls", "apprise", map[string]string{"server": "http://apprise:8000", "urls": "discord://a/b,tgram://c/d"}, ""},
		{"apprise key", "apprise", map[string]string{"server": "http://apprise:8000", "key": "home_1"}, ""},
		{"apprise spaces between urls", "apprise", map[string]string{"server": "http://apprise:8000", "urls": "discord://a/b tgram://c/d"}, ""},
		{"apprise nothing to send to", "apprise", map[string]string{"server": "http://apprise:8000"}, "service addresses"},
		{"apprise both", "apprise", map[string]string{"server": "http://apprise:8000", "key": "k", "urls": "discord://a/b"}, "not both"},
		{"apprise bad url", "apprise", map[string]string{"server": "http://apprise:8000", "urls": "discord.com/hook"}, "isn't an Apprise address"},
		{"apprise bad key", "apprise", map[string]string{"server": "http://apprise:8000", "key": "a/b"}, "configuration key"},
		{"apprise server without scheme", "apprise", map[string]string{"server": "apprise:8000", "key": "k"}, "http://"},
		{"apprise half a login", "apprise", map[string]string{"server": "http://a:1", "key": "k", "username": "x"}, "both the username"},
		{"matrix ok", "matrix", map[string]string{"url": "https://matrix.org", "roomId": "!abc:matrix.org", "accessToken": "syt_x"}, ""},
		{"matrix alias", "matrix", map[string]string{"url": "https://matrix.org", "roomId": "#room:matrix.org", "accessToken": "syt_x"}, "room ID"},
		{"matrix no token", "matrix", map[string]string{"url": "https://matrix.org", "roomId": "!abc:matrix.org"}, "Access token can't be left empty"},
		{"matrix token with space", "matrix", map[string]string{"url": "https://matrix.org", "roomId": "!abc:matrix.org", "accessToken": "a b"}, "space"},
		{"ha service", "homeassistant", map[string]string{"url": "http://ha.local:8123", "service": "mobile_app_pixel", "token": "t"}, ""},
		{"ha webhook", "homeassistant", map[string]string{"url": "http://ha.local:8123", "webhookId": "abc-123"}, ""},
		{"ha needs one of them", "homeassistant", map[string]string{"url": "http://ha.local:8123"}, "long-lived"},
		{"ha bad service", "homeassistant", map[string]string{"url": "http://ha.local:8123", "service": "notify.phone", "token": "t"}, "notify service"},
		{"ha bad webhook", "homeassistant", map[string]string{"url": "http://ha.local:8123", "webhookId": "a/b"}, "webhook ID"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := notify.Validate(c.typ, notify.ApplyDefaults(c.typ, c.cfg))
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestNewTypesAreInTheCatalogAndBuildSenders(t *testing.T) {
	for _, typ := range []string{"apprise", "matrix", "homeassistant"} {
		info, ok := notify.TypeByName(typ)
		if !ok || len(info.Fields) == 0 {
			t.Fatalf("%s missing from the catalog", typ)
		}
	}
	cases := map[string]map[string]string{
		"apprise":       {"server": "http://a:1", "urls": "discord://a/b"},
		"matrix":        {"url": "http://m:1", "roomId": "!r:m", "accessToken": "t"},
		"homeassistant": {"url": "http://h:1", "token": "t"},
	}
	for typ, cfg := range cases {
		s, err := notify.NewSender(notify.Target{Type: notify.TargetType(typ), Config: cfg})
		if err != nil || s == nil {
			t.Fatalf("%s: NewSender: %v", typ, err)
		}
	}
	// The tokens and addresses with tokens in them are secrets.
	for typ, secrets := range map[string][]string{
		"apprise":       {"key", "urls", "password"},
		"matrix":        {"accessToken"},
		"homeassistant": {"token", "webhookId"},
	} {
		info, _ := notify.TypeByName(typ)
		for _, name := range secrets {
			found := false
			for _, f := range info.Fields {
				if f.Name == name {
					found = true
					if !f.Secret() {
						t.Errorf("%s.%s must be a secret field", typ, name)
					}
				}
			}
			if !found {
				t.Errorf("%s has no field %s", typ, name)
			}
		}
	}
}

func TestSplitAppriseURLs(t *testing.T) {
	got := notify.SplitAppriseURLs(" a://1 ,b://2,, c://3\n d://4 ")
	if strings.Join(got, "|") != "a://1|b://2|c://3|d://4" {
		t.Fatalf("got %v", got)
	}
}
