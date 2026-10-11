package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrUserNotFound is returned when an account id does not exist.
	ErrUserNotFound = errors.New("no such account")
	// ErrLastAdmin is returned when a change would leave no administrator.
	ErrLastAdmin = errors.New("there must always be at least one administrator")
)

// Account is a User plus the bookkeeping the account list shows.
type Account struct {
	User
	CreatedAt   time.Time
	LastLoginAt *time.Time  // nil until the account first signs in
	Permissions Permissions // what a basic account may do (all, for an administrator)
}

// AccountUpdate lists the changes an administrator makes to an account; a nil
// field is left as it is.
type AccountUpdate struct {
	FirstName *string
	LastName  *string
	Email     *string
	IsAdmin   *bool
	Viewer    *bool   // the read-only role; setting it makes the account a non-admin
	Disabled  *bool   // turn the account off (its sessions are signed out) or on again
	Password  *string // a new password; the account's sessions are signed out
}

const accountColumns = `id, username, is_admin, first_name, last_name, email, created_at, last_login_at, permissions, is_viewer, disabled, auth_source`

func scanAccount(scan func(dest ...any) error) (Account, error) {
	var (
		a       Account
		created string
		last    sql.NullString
		perms   string
	)
	if err := scan(&a.ID, &a.Username, &a.IsAdmin, &a.FirstName, &a.LastName, &a.Email, &created, &last, &perms, &a.Viewer, &a.Disabled, &a.Source); err != nil {
		return Account{}, err
	}
	a.Permissions = parsePermissions(perms)
	if a.IsAdmin {
		a.Permissions = DefaultPermissions()
		a.Viewer = false
	} else if a.Viewer {
		a.Permissions = ViewerPermissions()
	}
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return Account{}, fmt.Errorf("parse created_at: %w", err)
	}
	a.CreatedAt = t
	if last.Valid && last.String != "" {
		lt, err := time.Parse(time.RFC3339Nano, last.String)
		if err != nil {
			return Account{}, fmt.Errorf("parse last_login_at: %w", err)
		}
		a.LastLoginAt = &lt
	}
	return a, nil
}

// ListAccounts returns every account, oldest first.
func (s *Service) ListAccounts() ([]Account, error) {
	rows, err := s.db.Query(`SELECT ` + accountColumns + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAccount loads one account, or ErrUserNotFound.
func (s *Service) GetAccount(id int64) (*Account, error) {
	a, err := scanAccount(s.db.QueryRow(`SELECT `+accountColumns+` FROM users WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	return &a, nil
}

// CreateAccount adds an account with an explicit role, for an administrator
// setting up someone else's login. ErrUsernameTaken when the name is in use.
func (s *Service) CreateAccount(username, password, firstName, lastName, email string, isAdmin bool) (*Account, error) {
	role := RoleMember
	if isAdmin {
		role = RoleAdmin
	}
	return s.CreateAccountWithRole(username, password, firstName, lastName, email, role)
}

// CreateAccountWithRole is CreateAccount with the role named: RoleAdmin,
// RoleMember or RoleViewer.
func (s *Service) CreateAccountWithRole(username, password, firstName, lastName, email, role string) (*Account, error) {
	isAdmin, viewer := role == RoleAdmin, role == RoleViewer
	if len(password) > MaxPasswordBytes {
		return nil, ErrPasswordTooLong
	}
	if taken, err := s.nameInUse(username, 0); err != nil {
		return nil, err
	} else if taken {
		return nil, ErrUsernameTaken
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), hashCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, is_admin, is_viewer, first_name, last_name, email) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		username, string(hash), isAdmin, viewer, firstName, lastName, email)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return nil, ErrUsernameTaken
		}
		return nil, fmt.Errorf("insert account: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get inserted account id: %w", err)
	}
	return s.GetAccount(id)
}

// otherAdmins counts administrators other than id, inside tx.
func otherAdmins(tx *sql.Tx, id int64) (int, error) {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1 AND disabled = 0 AND id != ?`, id).Scan(&n); err != nil {
		return 0, fmt.Errorf("count administrators: %w", err)
	}
	return n, nil
}

// UpdateAccount applies upd to account id in one transaction. Taking the
// administrator role away from the last administrator fails with
// ErrLastAdmin; a new password signs every session of the account out.
func (s *Service) UpdateAccount(id int64, upd AccountUpdate) (*Account, error) {
	var newHash string
	if upd.IsAdmin != nil && *upd.IsAdmin && upd.Viewer != nil && *upd.Viewer {
		return nil, errors.New("an account is either an administrator or a viewer")
	}
	if upd.Password != nil {
		if len(*upd.Password) > MaxPasswordBytes {
			return nil, ErrPasswordTooLong
		}
		h, err := bcrypt.GenerateFromPassword([]byte(*upd.Password), hashCost)
		if err != nil {
			return nil, fmt.Errorf("hash password: %w", err)
		}
		newHash = string(h)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin update account: %w", err)
	}
	defer tx.Rollback()

	var isAdmin, wasDisabled bool
	var source string
	if err := tx.QueryRow(`SELECT is_admin, disabled, auth_source FROM users WHERE id = ?`, id).Scan(&isAdmin, &wasDisabled, &source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("lookup account: %w", err)
	}
	if upd.Password != nil && source != SourceLocal {
		return nil, ErrNotLocal
	}
	// Taking the administrator role away, or turning the account off, must
	// leave another administrator who can still sign in.
	losesAdmin := isAdmin && !wasDisabled && ((upd.IsAdmin != nil && !*upd.IsAdmin) || (upd.Viewer != nil && *upd.Viewer) || (upd.Disabled != nil && *upd.Disabled))
	if losesAdmin {
		n, err := otherAdmins(tx, id)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, ErrLastAdmin
		}
	}

	set := func(query string, args ...any) error {
		if _, err := tx.Exec(query, args...); err != nil {
			return fmt.Errorf("update account: %w", err)
		}
		return nil
	}
	if upd.FirstName != nil {
		if err := set(`UPDATE users SET first_name = ? WHERE id = ?`, *upd.FirstName, id); err != nil {
			return nil, err
		}
	}
	if upd.LastName != nil {
		if err := set(`UPDATE users SET last_name = ? WHERE id = ?`, *upd.LastName, id); err != nil {
			return nil, err
		}
	}
	if upd.Email != nil {
		if err := set(`UPDATE users SET email = ? WHERE id = ?`, *upd.Email, id); err != nil {
			return nil, err
		}
	}
	if upd.IsAdmin != nil {
		if err := set(`UPDATE users SET is_admin = ? WHERE id = ?`, *upd.IsAdmin, id); err != nil {
			return nil, err
		}
		if *upd.IsAdmin {
			if err := set(`UPDATE users SET is_viewer = 0 WHERE id = ?`, id); err != nil {
				return nil, err
			}
		}
	}
	if upd.Viewer != nil {
		if err := set(`UPDATE users SET is_viewer = ? WHERE id = ?`, *upd.Viewer, id); err != nil {
			return nil, err
		}
		if *upd.Viewer {
			if err := set(`UPDATE users SET is_admin = 0 WHERE id = ?`, id); err != nil {
				return nil, err
			}
		}
	}
	if upd.Disabled != nil {
		if err := set(`UPDATE users SET disabled = ? WHERE id = ?`, *upd.Disabled, id); err != nil {
			return nil, err
		}
		if *upd.Disabled {
			if err := set(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
				return nil, err
			}
		}
	}
	if upd.Password != nil {
		if err := set(`UPDATE users SET password_hash = ? WHERE id = ?`, newHash, id); err != nil {
			return nil, err
		}
		if err := set(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit update account: %w", err)
	}
	return s.GetAccount(id)
}

// DeleteAccount removes an account together with its sessions and API keys.
// Deleting the last administrator fails with ErrLastAdmin.
func (s *Service) DeleteAccount(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete account: %w", err)
	}
	defer tx.Rollback()

	var isAdmin bool
	if err := tx.QueryRow(`SELECT is_admin FROM users WHERE id = ?`, id).Scan(&isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("lookup account: %w", err)
	}
	if isAdmin {
		n, err := otherAdmins(tx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLastAdmin
		}
	}
	for _, q := range []string{
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM api_keys WHERE user_id = ?`,
		`DELETE FROM user_identities WHERE user_id = ?`,
		`DELETE FROM passkeys WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return fmt.Errorf("delete account: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete account: %w", err)
	}
	return nil
}

// RecordLogin stamps the account's last sign-in time.
func (s *Service) RecordLogin(id int64) error {
	if _, err := s.db.Exec(`UPDATE users SET last_login_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, id); err != nil {
		return fmt.Errorf("record login: %w", err)
	}
	return nil
}
