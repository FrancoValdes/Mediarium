package announce

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/crypto"
)

// Connection is one saved IRC connection with everything it needs to run.
// Secret fields hold the plain values once loaded; they are never sent to the
// browser or written to a log.
type Connection struct {
	ID          int64
	Name        string
	Enabled     bool
	Preset      string
	Server      string
	Port        int
	TLS         bool
	InsecureTLS bool
	Nick        string
	Auth        string
	AuthAccount string
	AuthPass    string // secret
	Invite      string // secret: it carries the tracker's IRC key

	Channels   []Channel // Key is secret
	Announcers []string
	TrackerURL string
	Template   Template
	Keys       map[string]string // secret: passkey, rsskey, authkey...

	FreeleechOnly bool
	MaxPerHour    int
	MaxPerDay     int
	CreatedAt     time.Time
}

// Defaults for the limits of a new connection.
const (
	DefaultMaxPerHour = 5
	DefaultMaxPerDay  = 20
)

// Repo stores connections and the log of what they grabbed.
type Repo struct {
	db  *sql.DB
	box *crypto.Box
}

// NewRepo makes a repo.
func NewRepo(db *sql.DB, box *crypto.Box) *Repo { return &Repo{db: db, box: box} }

// ErrNotFound is returned for a connection that does not exist.
var ErrNotFound = errors.New("irc connection not found")

type secretBlob struct {
	Keys        map[string]string `json:"keys,omitempty"`
	ChannelKeys map[string]string `json:"channelKeys,omitempty"` // by lower-case channel name
}

type channelRow struct {
	Name string `json:"name"`
}

const connColumns = `id, name, enabled, preset, server, port, tls, insecure_tls, nick, auth_mode, auth_account, auth_password_enc,
 invite_enc, channels, announcers, tracker_url, template, secrets_enc, freeleech_only, max_per_hour, max_per_day, created_at`

func (r *Repo) enc(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	out, err := r.box.Encrypt(v)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	return out, nil
}

func (r *Repo) dec(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	out, err := r.box.Decrypt(v)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return out, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func (r *Repo) scan(sc rowScanner) (Connection, error) {
	var c Connection
	var enabled, tlsOn, insecure, freeleech int
	var passEnc, inviteEnc, chans, anns, tmpl, secretsEnc, created string
	if err := sc.Scan(&c.ID, &c.Name, &enabled, &c.Preset, &c.Server, &c.Port, &tlsOn, &insecure, &c.Nick, &c.Auth, &c.AuthAccount, &passEnc,
		&inviteEnc, &chans, &anns, &c.TrackerURL, &tmpl, &secretsEnc, &freeleech, &c.MaxPerHour, &c.MaxPerDay, &created); err != nil {
		return c, err
	}
	c.Enabled, c.TLS, c.InsecureTLS, c.FreeleechOnly = enabled != 0, tlsOn != 0, insecure != 0, freeleech != 0
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	var err error
	if c.AuthPass, err = r.dec(passEnc); err != nil {
		return c, fmt.Errorf("connection %d password: %w", c.ID, err)
	}
	if c.Invite, err = r.dec(inviteEnc); err != nil {
		return c, fmt.Errorf("connection %d invite command: %w", c.ID, err)
	}
	var blob secretBlob
	if secretsEnc != "" {
		plain, err := r.dec(secretsEnc)
		if err != nil {
			return c, fmt.Errorf("connection %d keys: %w", c.ID, err)
		}
		if err := json.Unmarshal([]byte(plain), &blob); err != nil {
			return c, fmt.Errorf("connection %d keys: %w", c.ID, err)
		}
	}
	c.Keys = blob.Keys
	if c.Keys == nil {
		c.Keys = map[string]string{}
	}
	var rows []channelRow
	if err := json.Unmarshal([]byte(chans), &rows); err != nil {
		return c, fmt.Errorf("connection %d channels: %w", c.ID, err)
	}
	for _, row := range rows {
		c.Channels = append(c.Channels, Channel{Name: row.Name, Key: blob.ChannelKeys[strings.ToLower(row.Name)]})
	}
	if err := json.Unmarshal([]byte(anns), &c.Announcers); err != nil {
		return c, fmt.Errorf("connection %d announcers: %w", c.ID, err)
	}
	if err := json.Unmarshal([]byte(tmpl), &c.Template); err != nil {
		return c, fmt.Errorf("connection %d template: %w", c.ID, err)
	}
	return c, nil
}

// values prepares the columns shared by insert and update.
func (r *Repo) values(c Connection) (passEnc, inviteEnc, chans, anns, tmpl, secretsEnc string, err error) {
	if passEnc, err = r.enc(c.AuthPass); err != nil {
		return
	}
	if inviteEnc, err = r.enc(c.Invite); err != nil {
		return
	}
	blob := secretBlob{Keys: map[string]string{}, ChannelKeys: map[string]string{}}
	rows := []channelRow{}
	for _, ch := range c.Channels {
		rows = append(rows, channelRow{Name: ch.Name})
		if ch.Key != "" {
			blob.ChannelKeys[strings.ToLower(ch.Name)] = ch.Key
		}
	}
	for k, v := range c.Keys {
		if v != "" {
			blob.Keys[k] = v
		}
	}
	b, _ := json.Marshal(rows)
	chans = string(b)
	announcers := c.Announcers
	if announcers == nil {
		announcers = []string{}
	}
	b, _ = json.Marshal(announcers)
	anns = string(b)
	b, _ = json.Marshal(c.Template)
	tmpl = string(b)
	if len(blob.Keys) > 0 || len(blob.ChannelKeys) > 0 {
		b, _ = json.Marshal(blob)
		if secretsEnc, err = r.enc(string(b)); err != nil {
			return
		}
	}
	return
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Create saves a new connection.
func (r *Repo) Create(c Connection) (Connection, error) {
	passEnc, inviteEnc, chans, anns, tmpl, secretsEnc, err := r.values(c)
	if err != nil {
		return Connection{}, err
	}
	res, err := r.db.Exec(`INSERT INTO irc_connections (name, enabled, preset, server, port, tls, insecure_tls, nick, auth_mode, auth_account,
		auth_password_enc, invite_enc, channels, announcers, tracker_url, template, secrets_enc, freeleech_only, max_per_hour, max_per_day)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.Name, b2i(c.Enabled), c.Preset, c.Server, c.Port, b2i(c.TLS), b2i(c.InsecureTLS), c.Nick, c.Auth, c.AuthAccount,
		passEnc, inviteEnc, chans, anns, c.TrackerURL, tmpl, secretsEnc, b2i(c.FreeleechOnly), c.MaxPerHour, c.MaxPerDay)
	if err != nil {
		return Connection{}, fmt.Errorf("insert irc connection: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Connection{}, fmt.Errorf("irc connection id: %w", err)
	}
	return r.Get(id)
}

// Update replaces a connection's settings. The caller fills every field,
// secrets included.
func (r *Repo) Update(c Connection) (Connection, error) {
	passEnc, inviteEnc, chans, anns, tmpl, secretsEnc, err := r.values(c)
	if err != nil {
		return Connection{}, err
	}
	res, err := r.db.Exec(`UPDATE irc_connections SET name=?, enabled=?, preset=?, server=?, port=?, tls=?, insecure_tls=?, nick=?, auth_mode=?,
		auth_account=?, auth_password_enc=?, invite_enc=?, channels=?, announcers=?, tracker_url=?, template=?, secrets_enc=?, freeleech_only=?,
		max_per_hour=?, max_per_day=? WHERE id=?`,
		c.Name, b2i(c.Enabled), c.Preset, c.Server, c.Port, b2i(c.TLS), b2i(c.InsecureTLS), c.Nick, c.Auth, c.AuthAccount,
		passEnc, inviteEnc, chans, anns, c.TrackerURL, tmpl, secretsEnc, b2i(c.FreeleechOnly), c.MaxPerHour, c.MaxPerDay, c.ID)
	if err != nil {
		return Connection{}, fmt.Errorf("update irc connection %d: %w", c.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Connection{}, ErrNotFound
	}
	return r.Get(c.ID)
}

// SetEnabled switches a connection on or off.
func (r *Repo) SetEnabled(id int64, on bool) error {
	res, err := r.db.Exec(`UPDATE irc_connections SET enabled = ? WHERE id = ?`, b2i(on), id)
	if err != nil {
		return fmt.Errorf("switch irc connection %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a connection and its log.
func (r *Repo) Delete(id int64) error {
	res, err := r.db.Exec(`DELETE FROM irc_connections WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete irc connection %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get loads one connection.
func (r *Repo) Get(id int64) (Connection, error) {
	c, err := r.scan(r.db.QueryRow(`SELECT `+connColumns+` FROM irc_connections WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	return c, err
}

// List loads every connection, oldest first.
func (r *Repo) List() ([]Connection, error) {
	rows, err := r.db.Query(`SELECT ` + connColumns + ` FROM irc_connections ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list irc connections: %w", err)
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		c, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Decision values stored in the log and shown on the settings page.
const (
	DecisionGrabbed   = "grabbed"
	DecisionWouldGrab = "would-grab"
	DecisionSkipped   = "skipped"
	DecisionDuplicate = "duplicate"
	DecisionIgnored   = "ignored"
	DecisionFailed    = "failed"
)

// LogEntry is one announcement and what became of it.
type LogEntry struct {
	ID           int64     `json:"id"`
	ConnectionID int64     `json:"connectionId"`
	At           time.Time `json:"at"`
	Channel      string    `json:"channel"`
	Announcer    string    `json:"announcer"`
	Release      string    `json:"release"`
	Category     string    `json:"category"`
	SizeBytes    int64     `json:"sizeBytes"`
	Decision     string    `json:"decision"`
	Reason       string    `json:"reason"`
	Item         string    `json:"item"` // the movie or show it was for
}

const logKeep = 200

// AddLog stores a grab or a failed grab. The rest of the announcements (most
// of them) only live in memory. Old rows are removed.
func (r *Repo) AddLog(e LogEntry) error {
	_, err := r.db.Exec(`INSERT INTO irc_announce_log (connection_id, channel, announcer, release, category, size_bytes, decision, reason, item)
		VALUES (?,?,?,?,?,?,?,?,?)`, e.ConnectionID, e.Channel, e.Announcer, e.Release, e.Category, e.SizeBytes, e.Decision, e.Reason, e.Item)
	if err != nil {
		return fmt.Errorf("log irc announcement: %w", err)
	}
	_, err = r.db.Exec(`DELETE FROM irc_announce_log WHERE connection_id = ? AND (at < ? OR id NOT IN (
		SELECT id FROM irc_announce_log WHERE connection_id = ? ORDER BY id DESC LIMIT ?))`,
		e.ConnectionID, time.Now().Add(-7*24*time.Hour).UTC().Format("2006-01-02T15:04:05.000Z"), e.ConnectionID, logKeep)
	if err != nil {
		return fmt.Errorf("trim irc announce log: %w", err)
	}
	return nil
}

// RecentLog returns the newest stored rows, newest first.
func (r *Repo) RecentLog(connectionID int64, limit int) ([]LogEntry, error) {
	rows, err := r.db.Query(`SELECT id, connection_id, at, channel, announcer, release, category, size_bytes, decision, reason, item
		FROM irc_announce_log WHERE connection_id = ? ORDER BY id DESC LIMIT ?`, connectionID, limit)
	if err != nil {
		return nil, fmt.Errorf("read irc announce log: %w", err)
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		var at string
		if err := rows.Scan(&e.ID, &e.ConnectionID, &at, &e.Channel, &e.Announcer, &e.Release, &e.Category, &e.SizeBytes, &e.Decision, &e.Reason, &e.Item); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountGrabs counts the releases a connection grabbed since a time. With an
// announcer name it counts that announcer's only.
func (r *Repo) CountGrabs(connectionID int64, announcer string, since time.Time) (int, error) {
	q := `SELECT COUNT(*) FROM irc_announce_log WHERE connection_id = ? AND decision = 'grabbed' AND at >= ?`
	args := []any{connectionID, since.UTC().Format("2006-01-02T15:04:05.000Z")}
	if announcer != "" {
		q += ` AND announcer = ? COLLATE NOCASE`
		args = append(args, announcer)
	}
	var n int
	if err := r.db.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count irc grabs: %w", err)
	}
	return n, nil
}

// GrabbedReleases lists the releases grabbed since a time, to seed the
// duplicate check after a restart.
func (r *Repo) GrabbedReleases(connectionID int64, since time.Time) ([]string, error) {
	rows, err := r.db.Query(`SELECT release FROM irc_announce_log WHERE connection_id = ? AND decision = 'grabbed' AND at >= ?`,
		connectionID, since.UTC().Format("2006-01-02T15:04:05.000Z"))
	if err != nil {
		return nil, fmt.Errorf("read grabbed irc releases: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
