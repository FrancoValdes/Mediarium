package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Apprise, Matrix and Home Assistant: the Test button reaches a fake server of
// each, the secrets stay out of the answer and the list, and a form with
// nothing to send to is refused with a reason.
func TestNotificationNewChannelsSaveAndTest(t *testing.T) {
	_, base, client := loginNewServer(t)
	seen := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen <- r.Method + " " + r.URL.Path + "|" + r.Header.Get("Authorization") + "|" + string(b)
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(srv.Close)

	cases := []struct {
		typ        string
		cfg        map[string]string
		secret     string
		wantPrefix string
		wantAuth   string
	}{
		{"apprise", map[string]string{"server": srv.URL, "urls": "discord://ID/SECRETAPPRISETOKEN"}, "SECRETAPPRISETOKEN", "POST /notify|", ""},
		{"matrix", map[string]string{"url": srv.URL, "roomId": "!room:example.org", "accessToken": "SECRETMATRIXTOKEN"}, "SECRETMATRIXTOKEN", "PUT /_matrix/client/v3/rooms/!room:example.org/send/m.room.message/", "Bearer SECRETMATRIXTOKEN"},
		{"homeassistant", map[string]string{"url": srv.URL, "service": "mobile_app_phone", "token": "SECRETHATOKEN"}, "SECRETHATOKEN", "POST /api/services/notify/mobile_app_phone|", "Bearer SECRETHATOKEN"},
	}
	for _, c := range cases {
		res := postJSON[map[string]any](t, client, base+"/api/notifications/test", map[string]any{"type": c.typ, "config": c.cfg}, http.StatusOK)
		if res["sent"] != true {
			t.Fatalf("%s: test not sent: %+v", c.typ, res)
		}
		got := <-seen
		parts := strings.SplitN(got, "|", 3)
		if !strings.HasPrefix(parts[0]+"|", c.wantPrefix) && !strings.HasPrefix(parts[0], strings.TrimSuffix(c.wantPrefix, "|")) {
			t.Fatalf("%s: unexpected request %q", c.typ, got)
		}
		if parts[1] != c.wantAuth {
			t.Fatalf("%s: unexpected Authorization %q", c.typ, parts[1])
		}
		raw, _ := json.Marshal(res)
		if strings.Contains(string(raw), c.secret) {
			t.Fatalf("%s: a secret leaked into the test log: %s", c.typ, raw)
		}
		postJSON[map[string]any](t, client, base+"/api/notifications", map[string]any{"name": c.typ, "type": c.typ, "config": c.cfg}, http.StatusCreated)
	}
	list, _ := json.Marshal(getJSON[[]map[string]any](t, client, base+"/api/notifications"))
	for _, c := range cases {
		if strings.Contains(string(list), c.secret) {
			t.Fatalf("%s: a secret leaked into the list: %s", c.typ, list)
		}
	}
	res := postJSON[map[string]any](t, client, base+"/api/notifications", map[string]any{"type": "apprise", "config": map[string]string{"server": srv.URL}}, http.StatusBadRequest)
	if res["error"] == nil {
		t.Fatalf("expected an error: %+v", res)
	}
}
