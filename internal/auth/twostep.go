package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Two-step sign-in: after the password, a six-digit code from an
// authenticator app (see internal/totp). The secret arrives here already
// encrypted by the caller; recovery codes are kept only as hashes.

// TwoStep is an account's two-step sign-in state.
type TwoStep struct {
	Secret       string // encrypted; "" when never set up
	Enabled      bool
	LastStep     uint64 // the last code step used
	RecoveryLeft int
}

// ErrNoUser means the account doesn't exist.
var ErrNoUser = errors.New("no such account")

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func loadTwoStep(q rowQuerier, userID int64) (TwoStep, []string, error) {
	var st TwoStep
	var enabled int
	var codes string
	err := q.QueryRow(`SELECT totp_secret, totp_enabled, totp_last_step, recovery_codes FROM users WHERE id = ?`, userID).
		Scan(&st.Secret, &enabled, &st.LastStep, &codes)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil, ErrNoUser
	}
	if err != nil {
		return st, nil, fmt.Errorf("read two-step sign-in: %w", err)
	}
	st.Enabled = enabled == 1
	var hashes []string
	if codes != "" {
		if err := json.Unmarshal([]byte(codes), &hashes); err != nil {
			return st, nil, fmt.Errorf("read recovery codes: %w", err)
		}
	}
	st.RecoveryLeft = len(hashes)
	return st, hashes, nil
}

// TwoStepOf reads an account's two-step sign-in state.
func (s *Service) TwoStepOf(userID int64) (TwoStep, error) {
	st, _, err := loadTwoStep(s.db, userID)
	return st, err
}

// TwoStepUsers lists the accounts that have two-step sign-in on.
func (s *Service) TwoStepUsers() (map[int64]bool, error) {
	rows, err := s.db.Query(`SELECT id FROM users WHERE totp_enabled = 1`)
	if err != nil {
		return nil, fmt.Errorf("list two-step accounts: %w", err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan two-step account: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ErrTwoStepOn means two-step sign-in is already on for the account.
var ErrTwoStepOn = errors.New("two-step sign-in is already on for this account")

// StartTwoStep keeps a new (encrypted) secret while the person adds it to
// their app. It isn't used for signing in until EnableTwoStep.
func (s *Service) StartTwoStep(userID int64, encryptedSecret string) error {
	res, err := s.db.Exec(`UPDATE users SET totp_secret = ?, totp_last_step = 0 WHERE id = ? AND totp_enabled = 0`, encryptedSecret, userID)
	if err != nil {
		return fmt.Errorf("start two-step sign-in: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTwoStepOn
	}
	return nil
}

// EnableTwoStep switches two-step sign-in on once the first code matched
// (step is that code's step), with a fresh set of recovery codes. It answers
// the recovery codes, which are shown once and never again.
func (s *Service) EnableTwoStep(userID int64, step uint64) ([]string, error) {
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	res, err := s.db.Exec(`UPDATE users SET totp_enabled = 1, totp_last_step = ?, recovery_codes = ? WHERE id = ? AND totp_secret != '' AND totp_enabled = 0`, step, hashes, userID)
	if err != nil {
		return nil, fmt.Errorf("switch on two-step sign-in: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrTwoStepOn
	}
	return codes, nil
}

// NewRecoveryCodes replaces an account's recovery codes.
func (s *Service) NewRecoveryCodes(userID int64) ([]string, error) {
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`UPDATE users SET recovery_codes = ? WHERE id = ? AND totp_enabled = 1`, hashes, userID); err != nil {
		return nil, fmt.Errorf("make new recovery codes: %w", err)
	}
	return codes, nil
}

// DisableTwoStep switches two-step sign-in off and forgets the secret.
func (s *Service) DisableTwoStep(userID int64) error {
	if _, err := s.db.Exec(`UPDATE users SET totp_secret = '', totp_enabled = 0, totp_last_step = 0, recovery_codes = '' WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("switch off two-step sign-in: %w", err)
	}
	return nil
}

// DisableTwoStepByName is DisableTwoStep for the command-line reset.
func (s *Service) DisableTwoStepByName(username string) error {
	res, err := s.db.Exec(`UPDATE users SET totp_secret = '', totp_enabled = 0, totp_last_step = 0, recovery_codes = '' WHERE username = ? COLLATE NOCASE`, strings.TrimSpace(username))
	if err != nil {
		return fmt.Errorf("switch off two-step sign-in: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no account called %q", username)
	}
	return nil
}

// UseStep records that a code's step was used, so it can't be used again.
// It reports false when that step (or a later one) was used already, for
// instance by a second sign-in racing with the same code.
func (s *Service) UseStep(userID int64, step uint64) (bool, error) {
	res, err := s.db.Exec(`UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`, step, userID, step)
	if err != nil {
		return false, fmt.Errorf("record the code: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// UseRecoveryCode signs in with a recovery code instead of an app code. A
// code works once; it reports whether it matched.
func (s *Service) UseRecoveryCode(userID int64, code string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("use a recovery code: %w", err)
	}
	defer tx.Rollback()
	st, hashes, err := loadTwoStep(tx, userID)
	if err != nil {
		return false, err
	}
	if !st.Enabled {
		return false, nil
	}
	want := hashRecoveryCode(code)
	left := make([]string, 0, len(hashes))
	found := false
	for _, h := range hashes {
		if !found && subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			found = true
			continue
		}
		left = append(left, h)
	}
	if !found {
		return false, nil
	}
	b, _ := json.Marshal(left)
	if _, err := tx.Exec(`UPDATE users SET recovery_codes = ? WHERE id = ?`, string(b), userID); err != nil {
		return false, fmt.Errorf("use a recovery code: %w", err)
	}
	return true, tx.Commit()
}

// recoveryAlphabet leaves out letters and digits that are easy to mix up.
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// newRecoveryCodes makes ten codes like "k7m2-p9qa" and their stored form.
func newRecoveryCodes() ([]string, string, error) {
	codes := make([]string, 10)
	hashes := make([]string, 10)
	buf := make([]byte, 8)
	for i := range codes {
		if _, err := rand.Read(buf); err != nil {
			return nil, "", fmt.Errorf("make recovery codes: %w", err)
		}
		var b strings.Builder
		for j, c := range buf {
			if j == 4 {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[int(c)%len(recoveryAlphabet)])
		}
		codes[i] = b.String()
		hashes[i] = hashRecoveryCode(codes[i])
	}
	stored, _ := json.Marshal(hashes)
	return codes, string(stored), nil
}

// hashRecoveryCode ignores case, spaces and the dash, as people type them.
func hashRecoveryCode(code string) string {
	c := strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(code))
	sum := sha256.Sum256([]byte("mediarium-recovery:" + c))
	return hex.EncodeToString(sum[:])
}
