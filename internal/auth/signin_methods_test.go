package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/auth"
)

func TestSafeUsername(t *testing.T) {
	cases := map[string]string{
		"sam":                   "sam",
		"Sam.Example":           "Sam.Example",
		"sam@example.com":       "sam-example.com",
		"  sam  ":               "sam",
		"Zoë Müller":            "Zo-Mller",
		"..sam..":               "sam",
		"a":                     "axx",
		"":                      "xxx",
		"!!!":                   "xxx",
		"émile":                 "mile",
		"sam\x00\r\n;drop":      "samdrop",
		"日本語":                   "xxx",
		strings.Repeat("a", 80): strings.Repeat("a", 32),
	}
	for in, want := range cases {
		got := auth.SafeUsername(in)
		if got != want {
			t.Errorf("SafeUsername(%q) = %q, want %q", in, got, want)
		}
		if len(got) < 3 || len(got) > 32 {
			t.Errorf("SafeUsername(%q) = %q has a bad length", in, got)
		}
	}
}

func TestExternalUsersHaveNoPassword(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.CreateUser("ryan", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}
	u, err := svc.CreateExternalUser(auth.ExternalProfile{Provider: auth.SourceLDAP, Subject: "uuid-1", Username: "sam", Name: "Sam", Email: "sam@example.com", Role: auth.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	if u.Source != auth.SourceLDAP || u.IsLocal() || u.IsAdmin {
		t.Errorf("user = %+v", u)
	}
	if h := svc.StoredHashForTest(u.ID); h != "" {
		t.Errorf("stored hash = %q", h)
	}
	// No password opens it. Not the empty one, not a hash, not the user name.
	for _, pw := range []string{"", "sam", "hunter2hunter2", "$2a$10$abcdefghijklmnopqrstuuAbcdefghijklmnopqrstuvwxyz01234"} {
		if _, err := svc.Authenticate("sam", pw); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("Authenticate(sam, %q) = %v", pw, err)
		}
	}
	if err := svc.ChangePassword(u.ID, "", "a-new-password-1"); !errors.Is(err, auth.ErrNotLocal) {
		t.Errorf("ChangePassword = %v", err)
	}
	if err := svc.ResetPassword("sam", "a-new-password-1"); err == nil {
		t.Error("ResetPassword worked on an account with no password here")
	}
	pw := "a-new-password-1"
	if _, err := svc.UpdateAccount(u.ID, auth.AccountUpdate{Password: &pw}); !errors.Is(err, auth.ErrNotLocal) {
		t.Errorf("UpdateAccount with a password = %v", err)
	}
	if h := svc.StoredHashForTest(u.ID); h != "" {
		t.Errorf("a hash appeared: %q", h)
	}
}

func TestExternalIdentitiesAreUnique(t *testing.T) {
	svc := newTestService(t)
	svc.CreateUser("ryan", "hunter2hunter2")
	a, _ := svc.CreateExternalUser(auth.ExternalProfile{Provider: auth.SourceOIDC, Subject: "iss|1", Username: "sam", Role: auth.RoleMember})
	// The same identity again is refused, never a second account.
	if _, err := svc.CreateExternalUser(auth.ExternalProfile{Provider: auth.SourceOIDC, Subject: "iss|1", Username: "other", Role: auth.RoleMember}); !errors.Is(err, auth.ErrIdentityTaken) {
		t.Errorf("same identity: %v", err)
	}
	if n := countUsers(t, svc); n != 2 {
		t.Errorf("accounts = %d", n)
	}
	got, err := svc.UserByIdentity(auth.SourceOIDC, "iss|1")
	if err != nil || got.ID != a.ID {
		t.Errorf("lookup = %+v, %v", got, err)
	}
	// Another provider with the same subject is another person.
	if _, err := svc.UserByIdentity(auth.SourceLDAP, "iss|1"); !errors.Is(err, auth.ErrNoIdentity) {
		t.Errorf("other provider: %v", err)
	}
	if _, err := svc.UserByIdentity(auth.SourceOIDC, "iss|2"); !errors.Is(err, auth.ErrNoIdentity) {
		t.Errorf("other subject: %v", err)
	}
	// The user name is made unique without taking an existing account over.
	b, err := svc.CreateExternalUser(auth.ExternalProfile{Provider: auth.SourceOIDC, Subject: "iss|3", Username: "SAM", Role: auth.RoleMember, AllowSuffix: true})
	if err != nil || b.Username != "SAM-2" {
		t.Errorf("suffix: %+v, %v", b, err)
	}
	if _, err := svc.CreateExternalUser(auth.ExternalProfile{Provider: auth.SourceLDAP, Subject: "u-4", Username: "sam", Role: auth.RoleMember}); !errors.Is(err, auth.ErrUsernameTaken) {
		t.Errorf("no suffix: %v", err)
	}
	// An account has one link per provider; linking is not stealing.
	local, _ := svc.CreateUser("lena", "hunter2hunter2")
	if err := svc.LinkIdentity(local.ID, auth.SourceOIDC, "iss|1"); !errors.Is(err, auth.ErrIdentityTaken) {
		t.Errorf("stealing a link: %v", err)
	}
	if err := svc.LinkIdentity(local.ID, auth.SourceOIDC, "iss|9"); err != nil {
		t.Fatal(err)
	}
	if err := svc.LinkIdentity(local.ID, auth.SourceOIDC, "iss|10"); !errors.Is(err, auth.ErrIdentityTaken) {
		t.Errorf("second link: %v", err)
	}
	if err := svc.LinkIdentity(local.ID, "", "x"); err == nil {
		t.Error("an empty provider was linked")
	}
	// Removing an account removes its links.
	if err := svc.DeleteAccount(local.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UserByIdentity(auth.SourceOIDC, "iss|9"); !errors.Is(err, auth.ErrNoIdentity) {
		t.Errorf("link survived the account: %v", err)
	}
}

func countUsers(t *testing.T, svc *auth.Service) int {
	t.Helper()
	list, err := svc.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func TestExternalRoleSync(t *testing.T) {
	svc := newTestService(t)
	svc.CreateUser("ryan", "hunter2hunter2")
	u, _ := svc.CreateExternalUser(auth.ExternalProfile{Provider: auth.SourceOIDC, Subject: "s1", Username: "sam", Role: auth.RoleAdmin})
	if !u.IsAdmin {
		t.Fatal("not admin")
	}
	changed, err := svc.SyncExternalRole(u.ID, auth.RoleMember)
	if err != nil || !changed {
		t.Fatalf("demote: %v %v", changed, err)
	}
	if got, _ := svc.GetUser(u.ID); got.Role() != auth.RoleMember {
		t.Errorf("role = %s", got.Role())
	}
	if changed, _ := svc.SyncExternalRole(u.ID, auth.RoleMember); changed {
		t.Error("changed to the same role")
	}
	if _, err := svc.SyncExternalRole(u.ID, auth.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetUser(u.ID); got.Role() != auth.RoleViewer || got.IsAdmin {
		t.Errorf("role = %s", got.Role())
	}
	// A person with a password here keeps the role an administrator gave them.
	local, _ := svc.CreateAccount("lena", "hunter2hunter2", "", "", "", true)
	if changed, _ := svc.SyncExternalRole(local.ID, auth.RoleMember); changed {
		t.Error("a local account's role was changed by a provider")
	}
	// The last administrator that can sign in is never demoted.
	only := newTestService(t)
	only.CreateUser("ryan", "hunter2hunter2")
	solo, _ := only.CreateExternalUser(auth.ExternalProfile{Provider: auth.SourceOIDC, Subject: "s1", Username: "sam", Role: auth.RoleAdmin})
	rid, _ := only.UserByName("ryan")
	member := false
	if _, err := only.UpdateAccount(rid.ID, auth.AccountUpdate{IsAdmin: &member}); err != nil {
		t.Fatal(err)
	}
	if _, err := only.SyncExternalRole(solo.ID, auth.RoleMember); !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("last admin: %v", err)
	}
}

func TestLastAdministratorRulesCountOnlyEnabledOnes(t *testing.T) {
	svc := newTestService(t)
	a, _ := svc.CreateUser("ryan", "hunter2hunter2")
	b, _ := svc.CreateAccount("bea", "hunter2hunter2", "", "", "", true)
	yes, no := true, false
	// bea is turned off: ryan is now the only administrator who can sign in.
	if _, err := svc.UpdateAccount(b.ID, auth.AccountUpdate{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateAccount(a.ID, auth.AccountUpdate{Disabled: &yes}); !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("turning off the last enabled admin: %v", err)
	}
	if _, err := svc.UpdateAccount(a.ID, auth.AccountUpdate{IsAdmin: &no}); !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("demoting the last enabled admin: %v", err)
	}
	if _, err := svc.UpdateAccount(a.ID, auth.AccountUpdate{Viewer: &yes}); !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("making the last enabled admin a viewer: %v", err)
	}
	if err := svc.DeleteAccount(a.ID); !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("deleting the last enabled admin: %v", err)
	}
	// With bea back, either can step aside.
	if _, err := svc.UpdateAccount(b.ID, auth.AccountUpdate{Disabled: &no}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateAccount(a.ID, auth.AccountUpdate{Disabled: &yes}); err != nil {
		t.Errorf("turning off one of two admins: %v", err)
	}
}

func TestViewerRoleAndKeys(t *testing.T) {
	svc := newTestService(t)
	svc.CreateUser("ryan", "hunter2hunter2")
	v, err := svc.CreateAccountWithRole("vera", "hunter2hunter2", "", "", "", auth.RoleViewer)
	if err != nil || v.Role() != auth.RoleViewer || v.IsAdmin || !v.ReadOnly() {
		t.Fatalf("viewer = %+v, %v", v, err)
	}
	p, _ := svc.PermissionsOf(v.ID)
	if p.AddDirect || p.Manage || p.Releases || p.Retry || p.Subtitles || p.Movies || p.TV || p.Music || p.Books || !p.Play {
		t.Errorf("viewer permissions = %+v", p)
	}
	// Whatever is stored, a viewer never gets more.
	all := auth.DefaultPermissions()
	svc.SetPermissions(v.ID, all)
	if p, _ := svc.PermissionsOf(v.ID); p.Manage {
		t.Error("stored permissions widened a viewer")
	}
	u, _ := svc.UserByName("vera")
	if !u.Viewer || !u.ReadOnly() {
		t.Errorf("loaded = %+v", u)
	}
	// An administrator is never read-only by role, but a read-only key makes the request so.
	admin, _ := svc.UserByName("ryan")
	if admin.ReadOnly() {
		t.Error("admin is read-only")
	}
	key, _ := svc.CreateAPIKeyScoped(admin.ID, "dash", true)
	ku, err := svc.UserForAPIKey(key)
	if err != nil || !ku.ReadOnly() || !ku.ReadOnlyKey || !ku.IsAdmin {
		t.Fatalf("key user = %+v, %v", ku, err)
	}
	rw, _ := svc.CreateAPIKey(admin.ID, "script")
	if ku, _ := svc.UserForAPIKey(rw); ku.ReadOnly() {
		t.Error("an ordinary key is read-only")
	}
	keys, _ := svc.ListAPIKeys(admin.ID)
	ro := 0
	for _, k := range keys {
		if k.ReadOnly {
			ro++
		}
	}
	if len(keys) != 2 || ro != 1 {
		t.Errorf("keys = %+v", keys)
	}
	// A role switch to admin clears the viewer flag, and the other way round.
	yes, no := true, false
	if a, _ := svc.UpdateAccount(v.ID, auth.AccountUpdate{IsAdmin: &yes}); a.Viewer || !a.IsAdmin {
		t.Errorf("admin: %+v", a)
	}
	if a, _ := svc.UpdateAccount(v.ID, auth.AccountUpdate{IsAdmin: &no, Viewer: &yes}); !a.Viewer || a.IsAdmin {
		t.Errorf("viewer: %+v", a)
	}
	if _, err := svc.UpdateAccount(v.ID, auth.AccountUpdate{IsAdmin: &yes, Viewer: &yes}); err == nil {
		t.Error("admin and viewer at once")
	}
}

func TestDisabledAccountsAreOutEverywhere(t *testing.T) {
	svc := newTestService(t)
	svc.CreateUser("ryan", "hunter2hunter2")
	u, _ := svc.CreateAccount("sam", "hunter2hunter2", "", "", "", false)
	tok, _, _ := svc.CreateSession(u.ID)
	key, _ := svc.CreateAPIKey(u.ID, "k")
	yes, no := true, false
	svc.UpdateAccount(u.ID, auth.AccountUpdate{Disabled: &yes})
	if _, err := svc.UserForSession(tok); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("session: %v", err)
	}
	if _, err := svc.UserForAPIKey(key); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("key: %v", err)
	}
	if _, err := svc.Authenticate("sam", "hunter2hunter2"); !errors.Is(err, auth.ErrAccountDisabled) {
		t.Errorf("right password: %v", err)
	}
	if _, err := svc.Authenticate("sam", "wrong"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("wrong password: %v", err)
	}
	svc.UpdateAccount(u.ID, auth.AccountUpdate{Disabled: &no})
	if _, err := svc.Authenticate("sam", "hunter2hunter2"); err != nil {
		t.Errorf("after turning on: %v", err)
	}
	// Sessions are gone for good, not just hidden.
	if _, err := svc.UserForSession(tok); err == nil {
		t.Error("the old session came back")
	}
}

func TestSessionMethod(t *testing.T) {
	svc := newTestService(t)
	u, _ := svc.CreateUser("ryan", "hunter2hunter2")
	a, _, _ := svc.CreateSession(u.ID)
	b, _, _ := svc.CreateSessionVia(u.ID, auth.MethodPasskey)
	if svc.SessionMethod(a) != "password" || svc.SessionMethod(b) != "passkey" || svc.SessionMethod("nope") != "" {
		t.Errorf("methods: %q %q %q", svc.SessionMethod(a), svc.SessionMethod(b), svc.SessionMethod("nope"))
	}
}

func TestPasskeyStorage(t *testing.T) {
	svc := newTestService(t)
	a, _ := svc.CreateUser("ryan", "hunter2hunter2")
	b, _ := svc.CreateAccount("sam", "hunter2hunter2", "", "", "", false)
	h1, err := svc.WebAuthnHandle(a.ID)
	if err != nil || len(h1) != 64 {
		t.Fatalf("handle = %x, %v", h1, err)
	}
	h1b, _ := svc.WebAuthnHandle(a.ID)
	h2, _ := svc.WebAuthnHandle(b.ID)
	if string(h1) != string(h1b) || string(h1) == string(h2) {
		t.Error("handles are not stable and distinct")
	}
	if u, err := svc.UserByWebAuthnHandle(h2); err != nil || u.ID != b.ID {
		t.Errorf("by handle = %+v, %v", u, err)
	}
	for _, bad := range [][]byte{nil, {1, 2, 3}, make([]byte, 64)} {
		if _, err := svc.UserByWebAuthnHandle(bad); err == nil {
			t.Errorf("handle %x found someone", bad)
		}
	}
	id, err := svc.AddPasskey(a.ID, "  ", []byte("cred-1"), `{"id":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddPasskey(b.ID, "dup", []byte("cred-1"), `{}`); err == nil {
		t.Error("the same credential id was stored for two accounts")
	}
	keys, _ := svc.ListPasskeys(a.ID)
	if len(keys) != 1 || keys[0].Name != "Passkey" || keys[0].ID != id {
		t.Errorf("keys = %+v", keys)
	}
	if k, err := svc.PasskeyByCredentialID([]byte("cred-1")); err != nil || k.UserID != a.ID {
		t.Errorf("by credential id = %+v, %v", k, err)
	}
	if err := svc.RenamePasskey(b.ID, id, "stolen"); !errors.Is(err, auth.ErrPasskeyNotFound) {
		t.Errorf("rename by another account: %v", err)
	}
	if err := svc.DeletePasskey(b.ID, id); !errors.Is(err, auth.ErrPasskeyNotFound) {
		t.Errorf("delete by another account: %v", err)
	}
	if err := svc.SavePasskeyUse(id, `{"id":"x","signCount":4}`); err != nil {
		t.Fatal(err)
	}
	keys, _ = svc.ListPasskeys(a.ID)
	if keys[0].LastUsedAt == nil || !strings.Contains(keys[0].Credential, "4") {
		t.Errorf("use not saved: %+v", keys[0])
	}
	for i := 1; i < auth.MaxPasskeys; i++ {
		if _, err := svc.AddPasskey(a.ID, "k", []byte{byte(i), 9}, `{}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.AddPasskey(a.ID, "one more", []byte("last"), `{}`); err == nil {
		t.Error("more than the limit was stored")
	}
	if err := svc.DeletePasskey(a.ID, id); err != nil {
		t.Fatal(err)
	}
}
