// Package ereader keeps what an account needs to use its books on an e-reader:
// the e-reader password for the OPDS catalogue, the secret address for Kobo
// sync, and what has been sent to (and reported by) each Kobo.
//
// Both secrets are random and long. They are found by their SHA-256 and
// compared in constant time, so a stolen copy of the database cannot be used
// to sign in. The Kobo address is also kept encrypted, so the account can see
// it again; the e-reader password is shown once, when it is made.
package ereader

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned for a secret nobody has.
var ErrNotFound = errors.New("no such e-reader access")

// Sealer encrypts the Kobo address for storage (the server's crypto.Box).
type Sealer interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(encoded string) (string, error)
}

// Repo stores the e-reader access of every account.
type Repo struct {
	db  *sql.DB
	box Sealer
}

// NewRepo returns a Repo over db. box may be nil in tests that never ask for
// the Kobo address back; the address is then not kept.
func NewRepo(db *sql.DB, box Sealer) *Repo { return &Repo{db: db, box: box} }

func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// passwordAlphabet leaves out the letters and digits that look alike (0 o, 1 l i).
const passwordAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// newPassword is 24 characters from a 31-letter alphabet in groups of four,
// "abcd-efgh-...": about 119 bits, short enough to type on a phone.
func newPassword() (string, error) {
	const groups, size = 6, 4
	buf := make([]byte, groups*size)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("make e-reader password: %w", err)
	}
	var b strings.Builder
	for i, c := range buf {
		if i > 0 && i%size == 0 {
			b.WriteByte('-')
		}
		// 248 is the largest multiple of 31 below 256: no letter is likelier than another.
		for c >= 248 {
			var one [1]byte
			if _, err := rand.Read(one[:]); err != nil {
				return "", fmt.Errorf("make e-reader password: %w", err)
			}
			c = one[0]
		}
		b.WriteByte(passwordAlphabet[int(c)%len(passwordAlphabet)])
	}
	return b.String(), nil
}

// NormalizePassword drops the dashes and spaces people add or lose when they
// copy the password, and lower-cases it.
func NormalizePassword(p string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(p)))
}

// newToken is 64 hex characters (256 bits).
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make Kobo token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// TokenLen is the length of a Kobo token; anything else is turned away at once.
const TokenLen = 64

// Access says which e-reader features an account has switched on.
type Access struct {
	OPDS        bool
	OPDSCreated string
	Kobo        bool
	KoboCreated string
}

// Get returns what the account has switched on.
func (r *Repo) Get(userID int64) (Access, error) {
	var opds, kobo, oc, kc string
	err := r.db.QueryRow(`SELECT opds_hash, kobo_hash, opds_created_at, kobo_created_at FROM ereader_access WHERE user_id = ?`, userID).Scan(&opds, &kobo, &oc, &kc)
	if errors.Is(err, sql.ErrNoRows) {
		return Access{}, nil
	}
	if err != nil {
		return Access{}, fmt.Errorf("read e-reader access: %w", err)
	}
	return Access{OPDS: opds != "", OPDSCreated: oc, Kobo: kobo != "", KoboCreated: kc}, nil
}

func (r *Repo) ensure(userID int64) error {
	_, err := r.db.Exec(`INSERT OR IGNORE INTO ereader_access (user_id) VALUES (?)`, userID)
	if err != nil {
		return fmt.Errorf("save e-reader access: %w", err)
	}
	return nil
}

// NewOPDSPassword makes a new e-reader password for the account, replacing
// the old one (which stops working). It is returned once and never kept.
func (r *Repo) NewOPDSPassword(userID int64) (string, error) {
	pw, err := newPassword()
	if err != nil {
		return "", err
	}
	if err := r.ensure(userID); err != nil {
		return "", err
	}
	if _, err := r.db.Exec(`UPDATE ereader_access SET opds_hash = ?, opds_created_at = ? WHERE user_id = ?`, sha(NormalizePassword(pw)), now(), userID); err != nil {
		return "", fmt.Errorf("save e-reader password: %w", err)
	}
	return pw, nil
}

// RemoveOPDS turns the account's e-reader password off.
func (r *Repo) RemoveOPDS(userID int64) error {
	if _, err := r.db.Exec(`UPDATE ereader_access SET opds_hash = '', opds_created_at = '' WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("remove e-reader password: %w", err)
	}
	return nil
}

// CheckOPDS reports whether password is the account's e-reader password.
func (r *Repo) CheckOPDS(userID int64, password string) bool {
	var stored string
	if err := r.db.QueryRow(`SELECT opds_hash FROM ereader_access WHERE user_id = ?`, userID).Scan(&stored); err != nil {
		stored = ""
	}
	given := sha(NormalizePassword(password))
	// Compare something even when there is no password, so the time says nothing.
	if stored == "" {
		stored = strings.Repeat("0", len(given))
		subtle.ConstantTimeCompare([]byte(given), []byte(stored))
		return false
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(stored)) == 1
}

// NewKoboToken makes a new Kobo sync token for the account, replacing the old
// one (whose address stops working). Syncing starts over for the new address.
func (r *Repo) NewKoboToken(userID int64) (string, error) {
	if r.box == nil {
		return "", errors.New("no key to keep the Kobo address with")
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	enc, err := r.box.Encrypt(token)
	if err != nil {
		return "", fmt.Errorf("keep Kobo token: %w", err)
	}
	if err := r.ensure(userID); err != nil {
		return "", err
	}
	if _, err := r.db.Exec(`UPDATE ereader_access SET kobo_hash = ?, kobo_token_enc = ?, kobo_created_at = ? WHERE user_id = ?`, sha(token), enc, now(), userID); err != nil {
		return "", fmt.Errorf("save Kobo token: %w", err)
	}
	return token, nil
}

// KoboToken returns the account's Kobo token, or "" when it has none.
func (r *Repo) KoboToken(userID int64) (string, error) {
	var enc string
	err := r.db.QueryRow(`SELECT kobo_token_enc FROM ereader_access WHERE user_id = ? AND kobo_hash <> ''`, userID).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) || enc == "" {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Kobo token: %w", err)
	}
	if r.box == nil {
		return "", nil
	}
	token, err := r.box.Decrypt(enc)
	if err != nil {
		return "", fmt.Errorf("read Kobo token: %w", err)
	}
	return token, nil
}

// RemoveKobo turns the account's Kobo sync off and forgets what was sent.
func (r *Repo) RemoveKobo(userID int64) error {
	if _, err := r.db.Exec(`UPDATE ereader_access SET kobo_hash = '', kobo_token_enc = '', kobo_created_at = '' WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("remove Kobo token: %w", err)
	}
	return r.ResetKobo(userID)
}

// UserForKobo returns the account a Kobo token belongs to.
func (r *Repo) UserForKobo(token string) (int64, error) {
	if len(token) != TokenLen {
		return 0, ErrNotFound
	}
	given := sha(token)
	var (
		id     int64
		stored string
	)
	err := r.db.QueryRow(`SELECT user_id, kobo_hash FROM ereader_access WHERE kobo_hash = ?`, given).Scan(&id, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("look up Kobo token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(given), []byte(stored)) != 1 {
		return 0, ErrNotFound
	}
	return id, nil
}
