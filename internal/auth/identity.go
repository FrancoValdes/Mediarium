package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Outside identities: the link between an account and the person a sign-in
// provider (an OpenID Connect server, a directory) says is signed in. The link
// is the provider's stable id for the person, never the user name or the email
// address, which can be renamed or given to somebody else.

var (
	// ErrIdentityTaken is returned when the outside identity already belongs to an account.
	ErrIdentityTaken = errors.New("that sign-in is already linked to an account")
	// ErrNoIdentity is returned when no account is linked to the identity.
	ErrNoIdentity = errors.New("no account is linked to that sign-in")
)

// Identity is one link between an account and a provider.
type Identity struct {
	Provider  string
	Subject   string
	CreatedAt time.Time
}

// UserByIdentity is the account linked to (provider, subject), or ErrNoIdentity.
func (s *Service) UserByIdentity(provider, subject string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(`SELECT `+userColsOf("u")+` FROM user_identities i JOIN users u ON u.id = i.user_id
		WHERE i.provider = ? AND i.subject = ?`, provider, subject).Scan(u.scanDest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoIdentity
	}
	if err != nil {
		return nil, fmt.Errorf("look up identity: %w", err)
	}
	return u, nil
}

// IdentitiesOf lists the providers linked to an account.
func (s *Service) IdentitiesOf(userID int64) ([]Identity, error) {
	rows, err := s.db.Query(`SELECT provider, subject, created_at FROM user_identities WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	defer rows.Close()
	var out []Identity
	for rows.Next() {
		var i Identity
		var created string
		if err := rows.Scan(&i.Provider, &i.Subject, &created); err != nil {
			return nil, fmt.Errorf("scan identity: %w", err)
		}
		i.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, i)
	}
	return out, rows.Err()
}

// LinkIdentity ties (provider, subject) to the account. An account has at most
// one link per provider, and an identity belongs to one account.
func (s *Service) LinkIdentity(userID int64, provider, subject string) error {
	if provider == "" || subject == "" {
		return errors.New("link identity: empty provider or subject")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin link identity: %w", err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM user_identities WHERE user_id = ? AND provider = ?`, userID, provider).Scan(&n); err != nil {
		return fmt.Errorf("check identity: %w", err)
	}
	if n > 0 {
		return ErrIdentityTaken
	}
	if _, err := tx.Exec(`INSERT INTO user_identities (user_id, provider, subject) VALUES (?, ?, ?)`, userID, provider, subject); err != nil {
		if isUniqueConstraintErr(err) {
			return ErrIdentityTaken
		}
		return fmt.Errorf("insert identity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit link identity: %w", err)
	}
	return nil
}

// UnlinkIdentity removes an account's link to a provider.
func (s *Service) UnlinkIdentity(userID int64, provider string) error {
	if _, err := s.db.Exec(`DELETE FROM user_identities WHERE user_id = ? AND provider = ?`, userID, provider); err != nil {
		return fmt.Errorf("unlink identity: %w", err)
	}
	return nil
}

// ExternalProfile is what a provider told us about a person.
type ExternalProfile struct {
	Provider string // "oidc" or "ldap"
	Subject  string // the provider's stable id for the person
	Username string // wanted user name (made safe and unique here)
	Name     string
	Email    string
	Role     string // RoleAdmin, RoleMember or RoleViewer
	// AllowSuffix lets a taken user name get a number added. Without it a
	// taken name is an error (ErrUsernameTaken).
	AllowSuffix bool
}

// CreateExternalUser makes the account for a person's first sign-in through a
// provider. It has no password (password_hash stays empty, so no password ever
// signs it in). The user name is made to fit the rules and, if another account
// already has it, gets a number added: the new account never takes over an
// existing one.
func (s *Service) CreateExternalUser(p ExternalProfile) (*User, error) {
	if p.Provider != SourceOIDC && p.Provider != SourceLDAP {
		return nil, fmt.Errorf("create external user: unknown provider %q", p.Provider)
	}
	if p.Subject == "" {
		return nil, errors.New("create external user: no subject")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin create external user: %w", err)
	}
	defer tx.Rollback()

	base := SafeUsername(p.Username)
	name := base
	for i := 2; ; i++ {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE lower(username) = lower(?)`, name).Scan(&n); err != nil {
			return nil, fmt.Errorf("check username: %w", err)
		}
		if n == 0 {
			break
		}
		if !p.AllowSuffix {
			return nil, ErrUsernameTaken
		}
		suffix := fmt.Sprintf("-%d", i)
		name = truncateRunes(base, 32-len(suffix)) + suffix
		if i > 500 {
			return nil, ErrUsernameTaken
		}
	}
	isAdmin, viewer := p.Role == RoleAdmin, p.Role == RoleViewer
	res, err := tx.Exec(`INSERT INTO users (username, password_hash, is_admin, is_viewer, first_name, last_name, email, auth_source)
		VALUES (?, '', ?, ?, ?, '', ?, ?)`, name, isAdmin, viewer, strings.TrimSpace(p.Name), strings.TrimSpace(p.Email), p.Provider)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return nil, ErrUsernameTaken
		}
		return nil, fmt.Errorf("insert external user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get inserted id: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO user_identities (user_id, provider, subject) VALUES (?, ?, ?)`, id, p.Provider, p.Subject); err != nil {
		if isUniqueConstraintErr(err) {
			return nil, ErrIdentityTaken
		}
		return nil, fmt.Errorf("insert identity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit create external user: %w", err)
	}
	return s.GetUser(id)
}

// SyncExternalRole sets the role of an account made through a provider to
// match what the provider says (an administrator group, for example). It never
// takes the administrator role from the last administrator who can sign in. It
// reports whether the role changed.
func (s *Service) SyncExternalRole(userID int64, role string) (bool, error) {
	u, err := s.GetUser(userID)
	if err != nil {
		return false, err
	}
	if u.Source == SourceLocal {
		return false, nil // a person with a password here keeps the role an administrator gave them
	}
	if u.Role() == role {
		return false, nil
	}
	upd := AccountUpdate{}
	admin, viewer := role == RoleAdmin, role == RoleViewer
	switch role {
	case RoleAdmin:
		upd.IsAdmin = &admin
	case RoleViewer:
		upd.Viewer = &viewer
	default:
		no := false
		upd.IsAdmin, upd.Viewer = &no, &no
	}
	if _, err := s.UpdateAccount(userID, upd); err != nil {
		return false, err
	}
	return true, nil
}

// RefreshExternalProfile updates the name and email of an account made through
// a provider, when the provider reports something (an empty value changes nothing).
func (s *Service) RefreshExternalProfile(userID int64, name, email string) error {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if name != "" {
		if _, err := s.db.Exec(`UPDATE users SET first_name = ?, last_name = '' WHERE id = ? AND auth_source != 'local'`, name, userID); err != nil {
			return fmt.Errorf("refresh name: %w", err)
		}
	}
	if email != "" {
		if _, err := s.db.Exec(`UPDATE users SET email = ? WHERE id = ? AND auth_source != 'local'`, email, userID); err != nil {
			return fmt.Errorf("refresh email: %w", err)
		}
	}
	return nil
}

// RenameExternal gives an account made through a provider the user name the
// provider now reports, when it changed and the new name is free. Otherwise
// nothing changes. It reports whether the name changed.
func (s *Service) RenameExternal(userID int64, username string) (bool, error) {
	name := SafeUsername(username)
	u, err := s.GetUser(userID)
	if err != nil {
		return false, err
	}
	if u.Source == SourceLocal || strings.EqualFold(u.Username, name) {
		return false, nil
	}
	taken, err := s.nameInUse(name, userID)
	if err != nil || taken {
		return false, err
	}
	if _, err := s.db.Exec(`UPDATE users SET username = ? WHERE id = ? AND auth_source != 'local'`, name, userID); err != nil {
		if isUniqueConstraintErr(err) {
			return false, nil
		}
		return false, fmt.Errorf("rename account: %w", err)
	}
	return true, nil
}

// LocalUsersByEmail lists the accounts with a password here that have this
// email address (any letter case), for linking a first single sign-on.
func (s *Service) LocalUsersByEmail(email string) ([]User, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT `+userCols+` FROM users WHERE lower(email) = lower(?) AND auth_source = 'local'`, email)
	if err != nil {
		return nil, fmt.Errorf("look up email: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(u.scanDest()...); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SafeUsername turns whatever a provider calls a person into a user name that
// meets the account rules: 3 to 32 characters, letters, digits, dots, dashes
// and underscores.
func SafeUsername(raw string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)), r == '.', r == '-', r == '_':
			b.WriteRune(r)
		case r == '@' || r == ' ':
			b.WriteRune('-')
		}
		// anything else (accents, symbols, control characters) is dropped
	}
	out := strings.Trim(b.String(), ".-_")
	out = truncateRunes(out, 32)
	for len(out) < 3 {
		out += "x"
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
