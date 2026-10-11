package auth

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Passkey storage. The credential column holds the WebAuthn library's own JSON
// record of the credential (public key, counter, flags): nothing in it is a
// secret. This package does not know the library, it only keeps the text.

// ErrPasskeyNotFound is returned when a passkey does not exist (for that account).
var ErrPasskeyNotFound = errors.New("no such passkey")

// MaxPasskeys is how many passkeys one account may have.
const MaxPasskeys = 20

// Passkey is one registered passkey.
type Passkey struct {
	ID           int64
	UserID       int64
	Name         string
	CredentialID []byte
	Credential   string // the library's JSON record
	CreatedAt    time.Time
	LastUsedAt   *time.Time
}

// WebAuthnHandle is the random id an account goes by on its authenticators (64
// bytes, made on first use). It is never the user name, so renaming an account
// changes nothing on the devices.
func (s *Service) WebAuthnHandle(userID int64) ([]byte, error) {
	var h []byte
	err := s.db.QueryRow(`SELECT webauthn_handle FROM users WHERE id = ?`, userID).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read passkey handle: %w", err)
	}
	if len(h) == 64 {
		return h, nil
	}
	h = make([]byte, 64)
	if _, err := rand.Read(h); err != nil {
		return nil, fmt.Errorf("make passkey handle: %w", err)
	}
	// Only the first caller's handle sticks, so two registrations at once agree.
	if _, err := s.db.Exec(`UPDATE users SET webauthn_handle = ? WHERE id = ? AND (webauthn_handle IS NULL OR length(webauthn_handle) != 64)`, h, userID); err != nil {
		return nil, fmt.Errorf("save passkey handle: %w", err)
	}
	if err := s.db.QueryRow(`SELECT webauthn_handle FROM users WHERE id = ?`, userID).Scan(&h); err != nil {
		return nil, fmt.Errorf("read passkey handle: %w", err)
	}
	return h, nil
}

// UserByWebAuthnHandle is the account a passkey's user handle names.
func (s *Service) UserByWebAuthnHandle(handle []byte) (*User, error) {
	if len(handle) != 64 {
		return nil, ErrUserNotFound
	}
	u := &User{}
	err := s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE webauthn_handle = ?`, handle).Scan(u.scanDest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("look up passkey handle: %w", err)
	}
	return u, nil
}

// AddPasskey stores a new passkey for the account, up to MaxPasskeys.
func (s *Service) AddPasskey(userID int64, name string, credentialID []byte, credential string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin add passkey: %w", err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM passkeys WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count passkeys: %w", err)
	}
	if n >= MaxPasskeys {
		return 0, fmt.Errorf("an account can have at most %d passkeys", MaxPasskeys)
	}
	res, err := tx.Exec(`INSERT INTO passkeys (user_id, credential_id, name, credential) VALUES (?, ?, ?, ?)`, userID, credentialID, name, credential)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return 0, errors.New("that passkey is already registered")
		}
		return 0, fmt.Errorf("insert passkey: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get passkey id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit add passkey: %w", err)
	}
	return id, nil
}

const passkeyColumns = `id, user_id, name, credential_id, credential, created_at, last_used_at`

func scanPasskey(scan func(...any) error) (Passkey, error) {
	var p Passkey
	var created string
	var used sql.NullString
	if err := scan(&p.ID, &p.UserID, &p.Name, &p.CredentialID, &p.Credential, &created, &used); err != nil {
		return Passkey{}, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if used.Valid && used.String != "" {
		if t, err := time.Parse(time.RFC3339Nano, used.String); err == nil {
			p.LastUsedAt = &t
		}
	}
	return p, nil
}

// ListPasskeys returns an account's passkeys, oldest first.
func (s *Service) ListPasskeys(userID int64) ([]Passkey, error) {
	rows, err := s.db.Query(`SELECT `+passkeyColumns+` FROM passkeys WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	defer rows.Close()
	var out []Passkey
	for rows.Next() {
		p, err := scanPasskey(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan passkey: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PasskeyByCredentialID finds the passkey with this credential id.
func (s *Service) PasskeyByCredentialID(credentialID []byte) (*Passkey, error) {
	p, err := scanPasskey(s.db.QueryRow(`SELECT `+passkeyColumns+` FROM passkeys WHERE credential_id = ?`, credentialID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPasskeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("look up passkey: %w", err)
	}
	return &p, nil
}

// SavePasskeyUse stores the updated credential record (a higher counter) after
// a good sign-in and stamps the time.
func (s *Service) SavePasskeyUse(id int64, credential string) error {
	if _, err := s.db.Exec(`UPDATE passkeys SET credential = ?, last_used_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, credential, id); err != nil {
		return fmt.Errorf("save passkey use: %w", err)
	}
	return nil
}

// RenamePasskey changes the name of the account's passkey.
func (s *Service) RenamePasskey(userID, id int64, name string) error {
	name = strings.TrimSpace(name)
	res, err := s.db.Exec(`UPDATE passkeys SET name = ? WHERE id = ? AND user_id = ?`, name, id, userID)
	if err != nil {
		return fmt.Errorf("rename passkey: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPasskeyNotFound
	}
	return nil
}

// DeletePasskey removes the account's passkey.
func (s *Service) DeletePasskey(userID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM passkeys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete passkey: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPasskeyNotFound
	}
	return nil
}
