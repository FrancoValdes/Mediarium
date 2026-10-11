package api

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/auth"
)

// accountPayload is one row of the account list an administrator manages.
// name is the name as stored (may be empty; the app then shows the username).
type accountPayload struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	Role        string     `json:"role"` // "admin", "member" or "viewer"
	IsAdmin     bool       `json:"isAdmin"`
	Disabled    bool       `json:"disabled"` // turned off: cannot sign in
	Source      string     `json:"source"`   // "local" (a password here), "oidc" or "ldap"
	CreatedAt   time.Time  `json:"createdAt"`
	LastLoginAt *time.Time `json:"lastLoginAt"` // null until the first sign-in
	// What a basic account may do (everything, for an administrator).
	Permissions auth.Permissions `json:"permissions"`
	TwoStep     bool             `json:"twoStep"` // two-step sign-in is on
}

func toAccountPayload(a *auth.Account) accountPayload {
	return accountPayload{
		ID: a.ID, Username: a.Username, Name: a.Name(), Email: a.Email,
		Role: a.Role(), IsAdmin: a.IsAdmin, Disabled: a.Disabled, Source: sourceOf(a.Source), CreatedAt: a.CreatedAt, LastLoginAt: a.LastLoginAt, Permissions: a.Permissions,
	}
}

// userRef names the account that did something, e.g. added a title.
type userRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// accountNames returns a lookup from account id to its reference, loading
// the account list at most once (on first use) per call of accountNames. An
// unknown or deleted account resolves to nil.
func (s *Server) accountNames() func(int64) *userRef {
	var names map[int64]string
	return func(id int64) *userRef {
		if id <= 0 {
			return nil
		}
		if names == nil {
			names = map[int64]string{}
			list, err := s.Auth.ListAccounts()
			if err != nil {
				log.Printf("api: list accounts for names: %v", err)
			}
			for i := range list {
				names[list[i].ID] = list[i].DisplayName()
			}
		}
		name, ok := names[id]
		if !ok {
			return nil
		}
		return &userRef{ID: id, Name: name}
	}
}

// requester is the signed-in account behind r: its id (0 if none) and the
// " by Name" suffix activity messages use.
func requester(r *http.Request) (id int64, byline string) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		return 0, ""
	}
	return u.ID, " by " + u.DisplayName()
}

// sourceOf is an account's sign-in source, "local" when none is stored.
func sourceOf(s string) string {
	if s == "" {
		return auth.SourceLocal
	}
	return s
}

// parseRole maps "admin", "member" and "viewer" to the role name.
func parseRole(role string) (string, bool) {
	switch r := strings.ToLower(strings.TrimSpace(role)); r {
	case auth.RoleAdmin, auth.RoleMember, auth.RoleViewer:
		return r, true
	}
	return "", false
}

const roleChoices = `Pick a role for this account: "admin", "member" or "viewer".`

// writeAccountError turns the account rules into plain answers.
func writeAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "That account doesn't exist.")
	case errors.Is(err, auth.ErrUsernameTaken):
		writeError(w, http.StatusConflict, "That username is already taken.")
	case errors.Is(err, auth.ErrLastAdmin):
		writeError(w, http.StatusConflict, "There must always be at least one administrator.")
	case errors.Is(err, auth.ErrNotLocal):
		writeError(w, http.StatusBadRequest, "This account signs in through single sign-on or the directory, so it has no password here.")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleListUsers lists every account with its role and last sign-in.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Auth.ListAccounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	twoStep, err := s.Auth.TwoStepUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]accountPayload, len(list))
	for i := range list {
		out[i] = toAccountPayload(&list[i])
		out[i].TwoStep = twoStep[list[i].ID]
	}
	writeJSON(w, http.StatusOK, out)
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	// Permissions for a basic account; left out, it may do everything a
	// basic account could always do.
	Permissions *auth.Permissions `json:"permissions"`
}

// handleCreateUser adds an account for someone else, for example a family
// member, with the role the administrator picks.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !requireSession(w, r) {
		return
	}
	var req createUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	first, last := splitName(req.Name, "", "")
	if rejectBad(w, checkUsername(req.Username), checkPassword(req.Password, "Password"), checkEmail(req.Email), checkMaxLen(first, "Name", nameMax)) {
		return
	}
	role, ok := parseRole(req.Role)
	if !ok {
		writeError(w, http.StatusBadRequest, roleChoices)
		return
	}
	created, err := s.Auth.CreateAccountWithRole(req.Username, req.Password, first, last, req.Email, role)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	if req.Permissions != nil && role == auth.RoleMember {
		if err := s.Auth.SetPermissions(created.ID, *req.Permissions); err != nil {
			writeAccountError(w, err)
			return
		}
		if again, err := s.Auth.GetAccount(created.ID); err == nil {
			created = again
		}
	}
	writeJSON(w, http.StatusCreated, toAccountPayload(created))
}

type updateUserRequest struct {
	Name        *string           `json:"name"`
	Email       *string           `json:"email"`
	Role        *string           `json:"role"`
	Disabled    *bool             `json:"disabled"`
	Password    *string           `json:"password"`
	Permissions *auth.Permissions `json:"permissions"`
}

func accountID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid account ID.")
		return 0, false
	}
	return id, true
}

// handleUpdateUser changes another account's name, email or role, or sets a
// new password for it (which signs that account out everywhere). An
// administrator cannot take away their own administrator role.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !requireSession(w, r) {
		return
	}
	id, ok := accountID(w, r)
	if !ok {
		return
	}
	var req updateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	var upd auth.AccountUpdate
	if req.Name != nil {
		first, last := splitName(*req.Name, "", "")
		if rejectBad(w, checkMaxLen(first, "Name", nameMax)) {
			return
		}
		upd.FirstName, upd.LastName = &first, &last
	}
	if req.Email != nil {
		email := strings.TrimSpace(*req.Email)
		if rejectBad(w, checkEmail(email)) {
			return
		}
		upd.Email = &email
	}
	if req.Role != nil {
		role, ok := parseRole(*req.Role)
		if !ok {
			writeError(w, http.StatusBadRequest, roleChoices)
			return
		}
		if me := auth.UserFromContext(r.Context()); me != nil && me.ID == id && role != auth.RoleAdmin {
			writeError(w, http.StatusBadRequest, "You can't remove your own administrator role. Ask another administrator to do it.")
			return
		}
		isAdmin, viewer := role == auth.RoleAdmin, role == auth.RoleViewer
		upd.IsAdmin, upd.Viewer = &isAdmin, &viewer
	}
	if req.Disabled != nil {
		if me := auth.UserFromContext(r.Context()); me != nil && me.ID == id && *req.Disabled {
			writeError(w, http.StatusBadRequest, "You can't turn off your own account. Ask another administrator to do it.")
			return
		}
		upd.Disabled = req.Disabled
	}
	if req.Password != nil {
		if rejectBad(w, checkPassword(*req.Password, "Password")) {
			return
		}
		upd.Password = req.Password
	}
	updated, err := s.Auth.UpdateAccount(id, upd)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	if req.Permissions != nil {
		if err := s.Auth.SetPermissions(id, *req.Permissions); err != nil {
			writeAccountError(w, err)
			return
		}
		if again, err := s.Auth.GetAccount(id); err == nil {
			updated = again
		}
	}
	writeJSON(w, http.StatusOK, toAccountPayload(updated))
}

// handleDeleteUser removes an account with its sessions and API keys. Titles
// it added stay in the library. An administrator cannot delete themselves.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := accountID(w, r)
	if !ok {
		return
	}
	if me := auth.UserFromContext(r.Context()); me != nil && me.ID == id {
		writeError(w, http.StatusBadRequest, "You can't delete your own account.")
		return
	}
	if err := s.Auth.DeleteAccount(id); err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}
