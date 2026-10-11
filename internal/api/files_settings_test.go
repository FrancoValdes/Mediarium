package api_test

import (
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/fileperms"
)

func TestFilePermissionsSettings(t *testing.T) {
	base, client := newFolderTestServer(t)
	t.Cleanup(func() { fileperms.Set(fileperms.Modes{}) })
	url := base + "/api/settings/file-permissions"

	// Nothing set: the default, and nothing is applied.
	got := getJSON[map[string]any](t, client, url)
	if got["fileMode"] != "" || got["folderMode"] != "" {
		t.Fatalf("default modes = %v, want both empty", got)
	}
	if m := fileperms.Get(); m.File != 0 || m.Dir != 0 {
		t.Fatalf("modes in use by default = %+v", m)
	}

	// Bad input is refused with a plain message and saves nothing.
	for _, body := range []map[string]any{
		{"fileMode": "abc"},
		{"fileMode": "64"},
		{"folderMode": "1777"},
		{"fileMode": "444"},   // the owner couldn't write
		{"folderMode": "644"}, // the owner couldn't enter
		{"fileMode": "644", "folderMode": "999"},
	} {
		resp := putJSONStatus(t, client, url, body, 400)
		if msg, _ := resp["error"].(string); msg == "" {
			t.Errorf("%v: no message in %v", body, resp)
		}
	}
	got = getJSON[map[string]any](t, client, url)
	if got["fileMode"] != "" || got["folderMode"] != "" {
		t.Fatalf("a refused change was saved: %v", got)
	}

	// World-writable needs a clear yes.
	resp := putJSONStatus(t, client, url, map[string]any{"folderMode": "777"}, 400)
	if msg, _ := resp["error"].(string); !strings.Contains(msg, "world-writable") {
		t.Errorf("message %q does not warn about world-writable", msg)
	}
	putJSONStatus(t, client, url, map[string]any{"fileMode": "666", "confirmWorldWritable": true}, 200)
	if m := fileperms.Get(); m.File != 0o666 {
		t.Errorf("confirmed world-writable mode not applied: %+v", m)
	}

	// Normal values are saved, tidied to three digits, and applied at once.
	resp = putJSONStatus(t, client, url, map[string]any{"fileMode": "0664", "folderMode": "775"}, 200)
	if resp["fileMode"] != "664" || resp["folderMode"] != "775" {
		t.Errorf("answer = %v", resp)
	}
	if m := fileperms.Get(); m.File != 0o664 || m.Dir != 0o775 {
		t.Errorf("modes in use = %+v, want 664 and 775", m)
	}
	got = getJSON[map[string]any](t, client, url)
	if got["fileMode"] != "664" || got["folderMode"] != "775" {
		t.Errorf("saved modes = %v", got)
	}

	// Leaving a field out keeps it; an empty one goes back to the default.
	putJSONStatus(t, client, url, map[string]any{"folderMode": ""}, 200)
	got = getJSON[map[string]any](t, client, url)
	if got["fileMode"] != "664" || got["folderMode"] != "" {
		t.Errorf("after clearing the folder mode: %v", got)
	}
	if m := fileperms.Get(); m.File != 0o664 || m.Dir != 0 {
		t.Errorf("modes in use = %+v", m)
	}
}
