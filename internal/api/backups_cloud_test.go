package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"
)

// davServer is an in-memory WebDAV folder, like a Nextcloud or NAS share.
func davServer(t *testing.T) (*httptest.Server, webdav.FileSystem) {
	t.Helper()
	fs := webdav.NewMemFS()
	h := &webdav.Handler{Prefix: "/dav", FileSystem: fs, LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "nas" || p != "nas-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, fs
}

func davFiles(t *testing.T, fs webdav.FileSystem) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	dir, err := fs.OpenFile(t.Context(), "/backups", 0, 0)
	if err != nil {
		return out
	}
	defer dir.Close()
	infos, _ := dir.Readdir(-1)
	for _, fi := range infos {
		f, _ := fs.OpenFile(t.Context(), "/backups/"+fi.Name(), 0, 0)
		b, _ := io.ReadAll(f)
		f.Close()
		out[fi.Name()] = b
	}
	return out
}

func postRestoreWithPassphrase(t *testing.T, client *http.Client, base string, data []byte, passphrase string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "backup.zip.enc")
	part.Write(data)
	if passphrase != "" {
		mw.WriteField("passphrase", passphrase) // after the file: the order mustn't matter
	}
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/api/system/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestCloudBackupToWebDAVAndRestore(t *testing.T) {
	dav, fs := davServer(t)
	_, base, client := loginNewServer(t)
	const passphrase = "correct horse battery"
	settings := map[string]any{
		"enabled": true, "kind": "webdav", "url": dav.URL + "/dav/backups",
		"username": "nas", "password": "nas-password", "passphrase": "too short", "keep": 3,
	}

	// Switching on needs a long enough passphrase.
	putJSONStatus(t, client, base+"/api/system/backups/cloud", settings, http.StatusBadRequest)

	// Test tries the details without saving them, and tidies up after itself.
	settings["passphrase"] = passphrase
	settings["password"] = "wrong"
	if code, out := doJSONStatus(t, client, http.MethodPost, base+"/api/system/backups/cloud/test", settings); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "user name or password") {
		t.Fatalf("test with a wrong password: %d %v", code, out)
	}
	settings["password"] = "nas-password"
	postJSON[map[string]any](t, client, base+"/api/system/backups/cloud/test", settings, http.StatusOK)
	if files := davFiles(t, fs); len(files) != 0 {
		t.Fatalf("the test left files behind: %v", files)
	}
	if got := getJSON[map[string]any](t, client, base+"/api/system/backups/cloud"); got["enabled"] == true {
		t.Fatalf("test saved the settings: %v", got)
	}

	// Saved, the page sees whether secrets are set but never the secrets.
	putJSONStatus(t, client, base+"/api/system/backups/cloud", settings, http.StatusOK)
	resp, _ := client.Get(base + "/api/system/backups/cloud")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if bytes.Contains(raw, []byte(passphrase)) || bytes.Contains(raw, []byte("nas-password")) {
		t.Fatalf("the settings page was sent a secret: %s", raw)
	}
	var got struct {
		HasPassword, HasPassphrase bool
		Keep                       int
	}
	json.Unmarshal(raw, &got)
	if !got.HasPassword || !got.HasPassphrase || got.Keep != 3 {
		t.Fatalf("settings: %s", raw)
	}

	// Saving again with the secrets left empty keeps them.
	delete(settings, "password")
	delete(settings, "passphrase")
	putJSONStatus(t, client, base+"/api/system/backups/cloud", settings, http.StatusOK)

	// An API key can't read or change any of it.
	req, _ := http.NewRequest(http.MethodGet, base+"/api/system/backups/cloud", nil)
	req.Header.Set("X-API-Key", "anything")
	if resp, err := client.Do(req); err != nil || (resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized) {
		t.Fatalf("API key: %v %v", resp.StatusCode, err)
	}

	up := postJSON[struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}](t, client, base+"/api/system/backups/cloud/upload", nil, http.StatusOK)
	files := davFiles(t, fs)
	locked, ok := files[up.Name]
	if !ok || !strings.HasSuffix(up.Name, ".zip.enc") || int64(len(locked)) != up.Size {
		t.Fatalf("uploaded %+v, folder has %d files", up, len(files))
	}
	if bytes.Contains(locked, []byte("PK\x03\x04")) || bytes.Contains(locked, []byte("SQLite format")) {
		t.Fatal("the cloud copy isn't locked")
	}
	list := getJSON[[]map[string]any](t, client, base+"/api/system/backups/cloud/list")
	if len(list) != 1 || list[0]["name"] != up.Name {
		t.Fatalf("list: %v", list)
	}
	if st := getJSON[map[string]any](t, client, base+"/api/system/backups/cloud"); st["last"].(map[string]any)["name"] != up.Name {
		t.Fatalf("last upload not recorded: %v", st)
	}

	// Another install restores the cloud copy with the passphrase.
	serverB, baseB, clientB := loginNewServer(t)
	called := exitSpy(serverB)
	if code, out := postRestoreWithPassphrase(t, clientB, baseB, locked, ""); code != http.StatusBadRequest || out["needPassphrase"] != true {
		t.Fatalf("no passphrase: %d %v", code, out)
	}
	if code, out := postRestoreWithPassphrase(t, clientB, baseB, locked, "not the passphrase"); code != http.StatusBadRequest || out["needPassphrase"] != true {
		t.Fatalf("wrong passphrase: %d %v", code, out)
	}
	neverExited(t, called)
	if code, out := postRestoreWithPassphrase(t, clientB, baseB, locked, passphrase); code != http.StatusOK || out["restarting"] != true {
		t.Fatalf("restore: %d %v", code, out)
	}
	select {
	case <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("the app should restart after the restore")
	}
}
