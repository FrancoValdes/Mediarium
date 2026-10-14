package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/backup"
	"github.com/rdborg/mediarium/internal/cloudbackup"
	"github.com/rdborg/mediarium/internal/netguard"
	"github.com/rdborg/mediarium/internal/problems"
	"github.com/rdborg/mediarium/internal/settings"
)

// Cloud copies of the nightly backup (Settings > System > Backup and restore). Off until
// an administrator sets it up. Each copy is the same zip that's kept in
// /config/backups (settings and database, never media), locked with a
// passphrase before it leaves the server, so the storage company only ever
// holds data it can't read. Everything here needs a signed-in browser
// session: the settings hold storage keys and the passphrase.

const (
	defaultCloudKeep = 14
	maxCloudKeep     = 365
	minPassphrase    = 10
	cloudSuffix      = ".enc"
	cloudTimeout     = 30 * time.Minute
	cloudTestTimeout = 30 * time.Second
)

// cloudConfig is the setting, stored encrypted.
type cloudConfig struct {
	Enabled bool   `json:"enabled"`
	Kind    string `json:"kind"` // "s3" or "webdav"

	// S3-style storage.
	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	AccessKey string `json:"accessKey,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
	PathStyle bool   `json:"pathStyle,omitempty"`

	// WebDAV.
	URL      string `json:"url,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	Passphrase string `json:"passphrase,omitempty"`
	Keep       int    `json:"keep,omitempty"`
}

// cloudStatus is what the last upload did (set by the app).
type cloudStatus struct {
	At      string `json:"at,omitempty"`
	Name    string `json:"name,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Problem string `json:"problem,omitempty"`
}

var cloudMu sync.Mutex // one upload at a time

func (s *Server) cloudConfig() cloudConfig {
	var c cloudConfig
	if v, err := s.Settings.Get(settings.KeyBackupCloud); err != nil {
		log.Printf("backup: read the cloud settings: %v", err)
	} else if v != "" {
		_ = json.Unmarshal([]byte(v), &c)
	}
	return c
}

func (c cloudConfig) keep() int {
	if c.Keep < 1 {
		return defaultCloudKeep
	}
	return min(c.Keep, maxCloudKeep)
}

func (c cloudConfig) store() (cloudbackup.Store, error) {
	// The address is typed in, so the connection goes through the guard that refuses cloud metadata services and the like.
	client := netguard.Client(cloudTimeout)
	switch c.Kind {
	case "s3":
		region := c.Region
		if region == "" {
			region = "us-east-1"
		}
		return &cloudbackup.S3{Endpoint: c.Endpoint, Region: region, Bucket: c.Bucket, Prefix: c.Prefix,
			AccessKey: c.AccessKey, SecretKey: c.SecretKey, PathStyle: c.PathStyle, Client: client}, nil
	case "webdav":
		return &cloudbackup.WebDAV{URL: c.URL, Username: c.Username, Password: c.Password, Client: client}, nil
	}
	return nil, errors.New("pick where the backups go")
}

// check says what's missing, in words for the settings page.
func (c cloudConfig) check() error {
	switch c.Kind {
	case "s3":
		switch {
		case !strings.HasPrefix(c.Endpoint, "https://") && !strings.HasPrefix(c.Endpoint, "http://"):
			return errors.New("enter the endpoint address, starting with https://")
		case c.Bucket == "":
			return errors.New("enter the bucket name")
		case c.AccessKey == "" || c.SecretKey == "":
			return errors.New("enter the access key and the secret key")
		}
	case "webdav":
		if !strings.HasPrefix(c.URL, "https://") && !strings.HasPrefix(c.URL, "http://") {
			return errors.New("enter the folder address, starting with https://")
		}
	default:
		return errors.New("pick where the backups go")
	}
	if len([]rune(c.Passphrase)) < minPassphrase {
		return fmt.Errorf("the passphrase needs at least %d characters", minPassphrase)
	}
	return nil
}

// cloudName is the name a saved backup gets in the storage.
func cloudName(local string) string { return local + cloudSuffix }

func isCloudBackup(name string) bool {
	return strings.HasPrefix(name, "mediarium-backup-") && strings.HasSuffix(name, ".zip"+cloudSuffix)
}

func (s *Server) cloudStatus() cloudStatus {
	var st cloudStatus
	if v, _ := s.Settings.Get(settings.KeyBackupCloudStatus); v != "" {
		_ = json.Unmarshal([]byte(v), &st)
	}
	return st
}

func (s *Server) saveCloudStatus(st cloudStatus) {
	b, _ := json.Marshal(st)
	if err := s.Settings.Set(settings.KeyBackupCloudStatus, string(b), false); err != nil {
		log.Printf("backup: %v", err)
	}
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// lockToTemp locks the saved backup at path into a temporary file in the
// config folder and returns that file's path, size and SHA-256.
func (s *Server) lockToTemp(path, passphrase string) (string, int64, string, error) {
	in, err := os.Open(path)
	if err != nil {
		return "", 0, "", fmt.Errorf("open the backup: %w", err)
	}
	defer in.Close()
	tmp, err := os.CreateTemp(s.cfg.ConfigDir, ".cloud-upload-*"+cloudSuffix)
	if err != nil {
		return "", 0, "", fmt.Errorf("make a temporary file: %w", err)
	}
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(tmp, h)}
	lockErr := cloudbackup.Lock(cw, in, passphrase)
	closeErr := tmp.Close()
	if lockErr != nil || closeErr != nil {
		os.Remove(tmp.Name())
		return "", 0, "", errors.Join(lockErr, closeErr)
	}
	return tmp.Name(), cw.n, hex.EncodeToString(h.Sum(nil)), nil
}

// uploadToCloud locks the saved backup called name, uploads it, and removes
// the oldest copies beyond the number kept. The result is recorded for the
// settings page; a failure is also recorded as a problem.
func (s *Server) uploadToCloud(ctx context.Context, c cloudConfig, name string) (cloudStatus, error) {
	cloudMu.Lock()
	defer cloudMu.Unlock()
	st := cloudStatus{At: time.Now().UTC().Format(time.RFC3339), Name: cloudName(name)}
	err := s.doUpload(ctx, c, name, &st)
	if err != nil {
		st.Problem = err.Error()
		problems.Record(problems.Problem{Code: problems.CodeCloudBackupFailed, Err: err})
		_ = s.QueueRepo.LogActivity(0, "backup", "Couldn't copy the backup to the cloud: "+err.Error())
	} else {
		_ = s.QueueRepo.LogActivity(0, "backup", fmt.Sprintf("Copied the backup to the cloud (%s)", humanBytes(st.Size)))
	}
	s.saveCloudStatus(st)
	return st, err
}

func (s *Server) doUpload(ctx context.Context, c cloudConfig, name string, st *cloudStatus) error {
	if err := c.check(); err != nil {
		return err
	}
	store, err := c.store()
	if err != nil {
		return err
	}
	path, err := backup.PathOf(s.cfg.ConfigDir, name)
	if err != nil {
		return fmt.Errorf("the backup %s isn't in the backups folder", name)
	}
	locked, size, hash, err := s.lockToTemp(path, c.Passphrase)
	if err != nil {
		return err
	}
	defer os.Remove(locked)
	f, err := os.Open(locked)
	if err != nil {
		return fmt.Errorf("open the locked backup: %w", err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(ctx, cloudTimeout)
	defer cancel()
	if err := store.Put(ctx, st.Name, f, size, hash); err != nil {
		return err
	}
	st.Size = size
	if err := pruneCloud(ctx, store, c.keep()); err != nil {
		log.Printf("backup: remove old cloud copies: %v", err)
	}
	return nil
}

// pruneCloud deletes the oldest backups beyond keep. Only files with the
// backup name pattern are touched, whatever else is in the folder.
func pruneCloud(ctx context.Context, store cloudbackup.Store, keep int) error {
	list, err := store.List(ctx)
	if err != nil {
		return err
	}
	var names []string
	for _, o := range list {
		if isCloudBackup(o.Name) {
			names = append(names, o.Name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names))) // the date is in the name: newest first
	for i := keep; i < len(names); i++ {
		if err := store.Delete(ctx, names[i]); err != nil {
			return err
		}
	}
	return nil
}

// cloudAfterBackup copies a fresh nightly backup to the cloud when that's
// switched on. It runs in the background so the nightly check isn't held up
// by a slow connection.
func (s *Server) cloudAfterBackup(saved backup.Saved) {
	c := s.cloudConfig()
	if !c.Enabled {
		return
	}
	go func() {
		if _, err := s.uploadToCloud(context.Background(), c, saved.Name); err != nil {
			log.Printf("backup: cloud copy failed: %v", err)
		}
	}()
}

// cloudPayload is the setting as the page sees it: never the keys,
// passwords or passphrase, only whether they're set.
type cloudPayload struct {
	Enabled       bool        `json:"enabled"`
	Kind          string      `json:"kind"`
	Endpoint      string      `json:"endpoint"`
	Region        string      `json:"region"`
	Bucket        string      `json:"bucket"`
	Prefix        string      `json:"prefix"`
	AccessKey     string      `json:"accessKey"`
	HasSecretKey  bool        `json:"hasSecretKey"`
	PathStyle     bool        `json:"pathStyle"`
	URL           string      `json:"url"`
	Username      string      `json:"username"`
	HasPassword   bool        `json:"hasPassword"`
	HasPassphrase bool        `json:"hasPassphrase"`
	Keep          int         `json:"keep"`
	Last          cloudStatus `json:"last"`
}

func (s *Server) cloudPayload() cloudPayload {
	c := s.cloudConfig()
	return cloudPayload{
		Enabled: c.Enabled, Kind: c.Kind, Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, Prefix: c.Prefix,
		AccessKey: c.AccessKey, HasSecretKey: c.SecretKey != "", PathStyle: c.PathStyle,
		URL: c.URL, Username: c.Username, HasPassword: c.Password != "", HasPassphrase: c.Passphrase != "",
		Keep: c.keep(), Last: s.cloudStatus(),
	}
}

// cloudFromRequest reads the settings sent by the page. Secrets left empty
// keep the saved ones, so the page never needs to hold them, but only while
// the backups still go to the same place: with another kind, endpoint,
// address or bucket they have to be typed again, so nobody can send the
// saved key, password or passphrase-locked backups somewhere else.
func (s *Server) cloudFromRequest(r *http.Request) (cloudConfig, error) {
	var req cloudConfig
	if err := decodeJSON(r, &req); err != nil {
		return req, err
	}
	old := s.cloudConfig()
	for _, p := range []*string{&req.Kind, &req.Endpoint, &req.Region, &req.Bucket, &req.Prefix, &req.AccessKey, &req.URL, &req.Username} {
		*p = strings.TrimSpace(*p)
	}
	if req.Kind != old.Kind || req.Endpoint != old.Endpoint || req.URL != old.URL || req.Bucket != old.Bucket {
		old.SecretKey, old.Password, old.Passphrase = "", "", ""
	}
	if req.SecretKey == "" {
		req.SecretKey = old.SecretKey
	}
	if req.Password == "" {
		req.Password = old.Password
	}
	if req.Passphrase == "" {
		req.Passphrase = old.Passphrase
	}
	if req.Keep < 0 || req.Keep > maxCloudKeep {
		req.Keep = 0
	}
	return req, nil
}

func cloudAllowed(w http.ResponseWriter, r *http.Request) bool {
	return requireAdmin(w, r) && requireSession(w, r)
}

// GET /api/system/backups/cloud
func (s *Server) handleGetCloudBackup(w http.ResponseWriter, r *http.Request) {
	if !cloudAllowed(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.cloudPayload())
}

// PUT /api/system/backups/cloud saves the settings. Switching it on needs
// everything filled in; switched off, whatever's there is kept as a draft.
func (s *Server) handlePutCloudBackup(w http.ResponseWriter, r *http.Request) {
	if !cloudAllowed(w, r) {
		return
	}
	c, err := s.cloudFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That request couldn't be read.")
		return
	}
	if c.Kind != "" && c.Kind != "s3" && c.Kind != "webdav" {
		writeError(w, http.StatusBadRequest, "Pick S3-style storage or WebDAV.")
		return
	}
	if c.Enabled {
		if err := c.check(); err != nil {
			writeError(w, http.StatusBadRequest, "Can't switch cloud backups on yet: "+err.Error()+".")
			return
		}
	}
	b, _ := json.Marshal(c)
	if err := s.Settings.Set(settings.KeyBackupCloud, string(b), true); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.cloudPayload())
}

// POST /api/system/backups/cloud/test tries the details sent (not yet
// saved): it uploads a tiny file, finds it in the list and deletes it.
func (s *Server) handleTestCloudBackup(w http.ResponseWriter, r *http.Request) {
	if !cloudAllowed(w, r) {
		return
	}
	c, err := s.cloudFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That request couldn't be read.")
		return
	}
	if err := c.check(); err != nil {
		writeError(w, http.StatusBadRequest, capitalise(err.Error())+".")
		return
	}
	store, _ := c.store()
	ctx, cancel := context.WithTimeout(r.Context(), cloudTestTimeout)
	defer cancel()
	if err := testStore(ctx, store); err != nil {
		writeError(w, http.StatusBadRequest, capitalise(err.Error())+".")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func testStore(ctx context.Context, store cloudbackup.Store) error {
	const name = "mediarium-connection-test.txt"
	body := []byte("Mediarium checked it can save backups here. This file can be deleted.\n")
	sum := sha256.Sum256(body)
	if err := store.Put(ctx, name, bytes.NewReader(body), int64(len(body)), hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	list, err := store.List(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, o := range list {
		found = found || o.Name == name
	}
	if err := store.Delete(ctx, name); err != nil {
		return err
	}
	if !found {
		return errors.New("the test file was saved but didn't show up in the folder's list")
	}
	return nil
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// POST /api/system/backups/cloud/upload copies the newest saved backup to
// the cloud now, making one first when the newest is over an hour old.
func (s *Server) handleUploadCloudBackup(w http.ResponseWriter, r *http.Request) {
	if !cloudAllowed(w, r) {
		return
	}
	c := s.cloudConfig()
	if err := c.check(); err != nil {
		writeError(w, http.StatusConflict, "Set up cloud backups and save first: "+err.Error()+".")
		return
	}
	list, err := backup.ListSaved(s.cfg.ConfigDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var name string
	if len(list) > 0 && time.Since(list[0].CreatedAt) < time.Hour {
		name = list[0].Name
	} else {
		saved, err := s.saveBackup(r.Context(), "for the cloud")
		if err != nil {
			log.Printf("backup: %v", err)
			writeError(w, http.StatusInternalServerError, "Couldn't save the backup. Check that the config folder has free space.")
			return
		}
		name = saved.Name
	}
	st, err := s.uploadToCloud(r.Context(), c, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "The backup was saved here but couldn't be copied to the cloud: "+err.Error()+".")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// GET /api/system/backups/cloud/list lists the copies in the storage.
func (s *Server) handleListCloudBackups(w http.ResponseWriter, r *http.Request) {
	if !cloudAllowed(w, r) {
		return
	}
	store, err := s.cloudConfig().store()
	if err != nil {
		writeJSON(w, http.StatusOK, []cloudbackup.Object{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), cloudTestTimeout)
	defer cancel()
	list, err := store.List(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, capitalise(err.Error())+".")
		return
	}
	out := []cloudbackup.Object{}
	for _, o := range list {
		if isCloudBackup(o.Name) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

var errNeedPassphrase = errors.New("the backup is locked")

// unlockUpload turns a locked backup into a plain zip next to it and returns
// the zip's path. A file that isn't locked comes back as it is.
func unlockUpload(path, passphrase string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 32)
	n, _ := io.ReadFull(f, head)
	if !cloudbackup.IsLocked(head[:n]) {
		return path, nil
	}
	if passphrase == "" {
		return "", errNeedPassphrase
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	out, err := os.CreateTemp(filepath.Dir(path), ".restore-unlocked-*.zip")
	if err != nil {
		return "", fmt.Errorf("make a temporary file: %w", err)
	}
	unlockErr := cloudbackup.Unlock(out, f, passphrase)
	closeErr := out.Close()
	if unlockErr != nil || closeErr != nil {
		os.Remove(out.Name())
		if errors.Is(unlockErr, cloudbackup.ErrWrongPassphrase) {
			return "", unlockErr
		}
		return "", errors.Join(unlockErr, closeErr)
	}
	return out.Name(), nil
}
