package api_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestAppLinks(t *testing.T) {
	_, base, admin, member, _ := familyServer(t)

	// Nothing yet; a bad address is refused.
	if list := getJSON[[]map[string]any](t, admin, base+"/api/apps"); len(list) != 0 {
		t.Fatalf("no apps yet: %v", list)
	}
	putJSON(t, admin, base+"/api/apps", []map[string]any{{"name": "Guide", "url": "ftp://box/guide"}}, http.StatusBadRequest)
	putJSON(t, admin, base+"/api/apps", []map[string]any{{"name": "Guide", "url": "http://user:pw@box/guide"}}, http.StatusBadRequest)
	putJSON(t, admin, base+"/api/apps", []map[string]any{{"name": "", "url": "http://box"}}, http.StatusBadRequest)
	// An address that could add a rule to the Content-Security-Policy header.
	for _, bad := range []string{"http://x;sandbox", "http://x;script-src%20*", "http://x'unsafe-inline'", "http://x y", "http://ex\"ample.com"} {
		putJSON(t, admin, base+"/api/apps", []map[string]any{{"name": "Guide", "url": bad, "mode": "frame", "enabled": true}}, http.StatusBadRequest)
	}

	saved := postJSONMethod[[]map[string]any](t, admin, http.MethodPut, base+"/api/apps", []map[string]any{
		{"name": "Channel guide", "url": "http://192.168.1.20:8080/guide", "mode": "frame", "icon": "tv", "enabled": true},
		{"name": "Old thing", "url": "https://old.example.com", "mode": "bogus", "icon": "Not A Name!", "enabled": false},
	}, http.StatusOK)
	if len(saved) != 2 || saved[0]["id"] == "" || saved[1]["mode"] != "tab" || saved[1]["icon"] != "" {
		t.Fatalf("saved list: %v", saved)
	}

	// An administrator sees both; a member only the one that is switched on.
	if got := getJSON[[]map[string]any](t, admin, base+"/api/apps"); len(got) != 2 {
		t.Fatalf("administrator: %v", got)
	}
	got := getJSON[[]map[string]any](t, member, base+"/api/apps")
	if len(got) != 1 || got[0]["name"] != "Channel guide" {
		t.Fatalf("member: %v", got)
	}
	// Only an administrator can change them.
	putJSON(t, member, base+"/api/apps", []map[string]any{}, http.StatusForbidden)

	// A frame add-on's address may be framed; nothing else.
	resp, err := admin.Get(base + "/api/apps")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-src https://www.youtube-nocookie.com http://192.168.1.20:8080;") {
		t.Fatalf("the frame add-on's origin is allowed in a frame: %s", csp)
	}
	if strings.Contains(csp, "old.example.com") {
		t.Fatalf("an add-on that opens in a tab, or is off, is not framed: %s", csp)
	}
}
