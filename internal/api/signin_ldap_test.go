package api_test

import (
	"crypto/tls"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/rdborg/mediarium/internal/ldapauth"
)

// fakeLDAP is a small directory behind the Conn interface. It understands the
// two searches Mediarium makes (a person by uid or mail, and a group by member)
// and checks passwords on bind.
type fakeLDAP struct {
	mu       sync.Mutex
	dials    int
	filters  []string
	users    []*dirUser
	down     bool
	svcDN    string
	svcPW    string
	groupsDN string
}

type dirUser struct {
	dn, uid, cn, mail, uuid, password string
	admin                             bool
}

func newFakeLDAP() *fakeLDAP {
	return &fakeLDAP{svcDN: "cn=search,dc=example,dc=com", svcPW: "service-secret", groupsDN: "ou=groups,dc=example,dc=com",
		users: []*dirUser{
			{dn: "uid=sam,ou=people,dc=example,dc=com", uid: "sam", cn: "Sam Example", mail: "sam@example.com", uuid: "uuid-sam", password: "dir-password"},
			{dn: "uid=ann,ou=people,dc=example,dc=com", uid: "ann", cn: "Ann Admin", mail: "ann@example.com", uuid: "uuid-ann", password: "ann-password", admin: true},
		}}
}

func (f *fakeLDAP) dial(ldapauth.Config) (ldapauth.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dials++
	if f.down {
		return nil, errors.New("dial tcp: connection refused")
	}
	return &fakeConn{f: f}, nil
}

func (f *fakeLDAP) dialCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.dials }

type fakeConn struct{ f *fakeLDAP }

func (c *fakeConn) StartTLS(*tls.Config) error { return nil }
func (c *fakeConn) Close() error               { return nil }
func (c *fakeConn) Bind(dn, pw string) error {
	if pw == "" {
		return ldap.NewError(ldap.LDAPResultInvalidCredentials, errors.New("empty"))
	}
	if dn == c.f.svcDN && pw == c.f.svcPW {
		return nil
	}
	for _, u := range c.f.users {
		if u.dn == dn && u.password == pw {
			return nil
		}
	}
	return ldap.NewError(ldap.LDAPResultInvalidCredentials, errors.New("bad credentials"))
}

var hexEscape = regexp.MustCompile(`\\([0-9a-f]{2})`)

func unescapeFilter(s string) string {
	return hexEscape.ReplaceAllStringFunc(s, func(m string) string {
		var b byte
		for _, c := range m[1:] {
			b <<= 4
			if c >= 'a' {
				b |= byte(c-'a') + 10
			} else {
				b |= byte(c - '0')
			}
		}
		return string([]byte{b})
	})
}

func (c *fakeConn) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f := c.f
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filters = append(f.filters, req.Filter)
	if req.BaseDN == f.groupsDN {
		// (&(objectClass=groupOfNames)(cn=mediarium-admins)(member=<dn>))
		for _, u := range f.users {
			if u.admin && strings.Contains(req.Filter, "(member="+strings.NewReplacer(`(`, `\28`, `)`, `\29`).Replace(u.dn)+")") {
				return &ldap.SearchResult{Entries: []*ldap.Entry{ldap.NewEntry("cn=mediarium-admins,"+f.groupsDN, nil)}}, nil
			}
		}
		return &ldap.SearchResult{}, nil
	}
	var matches []*ldap.Entry
	for _, u := range f.users {
		// Only filters of the exact shape Mediarium builds find anyone.
		for _, want := range []string{"(&(objectClass=person)(uid=" + u.uid + "))", "(|(uid=" + u.uid + ")(mail=" + u.mail + "))"} {
			_ = want
		}
		if filterMatches(req.Filter, u) {
			matches = append(matches, ldap.NewEntry(u.dn, map[string][]string{
				"uid": {u.uid}, "cn": {u.cn}, "mail": {u.mail}, "entryUUID": {u.uuid},
			}))
		}
	}
	return &ldap.SearchResult{Entries: matches}, nil
}

// filterMatches evaluates the only two filter shapes the tests configure.
func filterMatches(filter string, u *dirUser) bool {
	switch {
	case strings.HasPrefix(filter, "(&(objectClass=person)(uid=") && strings.HasSuffix(filter, "))"):
		v := strings.TrimSuffix(strings.TrimPrefix(filter, "(&(objectClass=person)(uid="), "))")
		return !strings.ContainsAny(v, "()*\\") && strings.EqualFold(unescapeFilter(v), u.uid)
	case strings.HasPrefix(filter, "(|(uid=") && strings.HasSuffix(filter, "))"):
		inner := strings.TrimSuffix(strings.TrimPrefix(filter, "(|(uid="), "))")
		uidPart, mailPart, ok := strings.Cut(inner, ")(mail=")
		if !ok || strings.ContainsAny(uidPart+mailPart, "()*") {
			return false
		}
		return strings.EqualFold(unescapeFilter(uidPart), u.uid) || strings.EqualFold(unescapeFilter(mailPart), u.mail)
	}
	return false
}

type ldapEnv struct {
	*signInEnv
	dir *fakeLDAP
}

func (e *ldapEnv) config(over map[string]any) map[string]any {
	c := map[string]any{
		"enabled": true, "url": "ldaps://ldap.example.com:636", "bindDn": e.dir.svcDN, "bindPassword": e.dir.svcPW,
		"baseDn": "dc=example,dc=com", "userFilter": "(&(objectClass=person)(uid={username}))",
		"autoCreate": true, "defaultRole": "member",
	}
	for k, v := range over {
		c[k] = v
	}
	return c
}

func (e *ldapEnv) saveConfig(over map[string]any) map[string]any {
	e.t.Helper()
	return e.must(e.admin, "PUT", "/api/auth/ldap", e.config(over), http.StatusOK)
}

func newLDAPEnv(t *testing.T) *ldapEnv {
	t.Helper()
	e := &ldapEnv{signInEnv: newSignInEnv(t, nil), dir: newFakeLDAP()}
	e.server.TestSetLDAPDial(e.dir.dial)
	e.saveConfig(nil)
	return e
}

func TestLDAPFirstSignInMakesAnAccountWithoutAPassword(t *testing.T) {
	e := newLDAPEnv(t)
	sam, status, out := e.login("sam", "dir-password")
	if status != http.StatusOK || out["username"] != "sam" || out["role"] != "member" || out["source"] != "ldap" {
		t.Fatalf("sign-in: %d %v", status, out["_raw"])
	}
	if u, _ := e.signedInAs(sam); u != "sam" {
		t.Fatalf("not signed in: %q", u)
	}
	me := e.must(sam, "GET", "/api/auth/me", nil, http.StatusOK)
	if me["email"] != "sam@example.com" || me["name"] != "Sam Example" {
		t.Errorf("profile = %v", me)
	}
	// The password is nowhere: not hashed, not encrypted, not in a session.
	if hash := e.scalar(`SELECT password_hash FROM users WHERE username = 'sam'`); hash != "" {
		t.Errorf("a password hash was stored: %q", hash)
	}
	if got := e.scalar(`SELECT method FROM sessions WHERE user_id = (SELECT id FROM users WHERE username = 'sam')`); got != "ldap" {
		t.Errorf("session method = %q", got)
	}
	for _, table := range []string{"users", "sessions", "settings", "api_keys"} {
		rows, err := e.db.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rows.Scan(ptrs...)
			for _, v := range vals {
				if s, ok := v.(string); ok && strings.Contains(s, "dir-password") {
					t.Errorf("the directory password is stored in %s", table)
				}
			}
		}
		rows.Close()
	}
	// The same person signing in again is the same account.
	again, status, _ := e.login("SAM", "dir-password")
	if u, _ := e.signedInAs(again); status != http.StatusOK || u != "sam" {
		t.Errorf("second sign-in: %d %q", status, u)
	}
	if n := e.scalar(`SELECT COUNT(*) FROM users`); n != "2" {
		t.Errorf("accounts = %s", n)
	}
	// The directory's password can't be changed or replaced from here.
	e.must(sam, "POST", "/api/auth/change-password", map[string]string{"currentPassword": "dir-password", "newPassword": "a-new-long-password"}, http.StatusBadRequest)
	id := e.scalar(`SELECT id FROM users WHERE username = 'sam'`)
	e.must(e.admin, "PUT", "/api/users/"+id, map[string]string{"password": "an-admin-set-password"}, http.StatusBadRequest)
	if err := e.server.Auth.ResetPassword("sam", "a-reset-password"); err == nil {
		t.Error("the reset-password command set a password on a directory account")
	}
	if hash := e.scalar(`SELECT password_hash FROM users WHERE username = 'sam'`); hash != "" {
		t.Errorf("a password hash appeared: %q", hash)
	}
	// And the two-step code is not for these accounts.
	e.must(sam, "POST", "/api/auth/two-step/setup", nil, http.StatusBadRequest)
}

func TestLDAPWrongPasswordsLookTheSameAndAreLimited(t *testing.T) {
	e := newLDAPEnv(t)
	_, s1, o1 := e.login("sam", "wrong")
	_, s2, o2 := e.login("nobody", "wrong")
	if s1 != http.StatusUnauthorized || s2 != s1 || o1["error"] != o2["error"] || o1["error"] != "Wrong username or password." {
		t.Fatalf("a wrong password and an unknown user must look the same: %d %v / %d %v", s1, o1["error"], s2, o2["error"])
	}
	// Same limits as a local sign-in: five wrong tries from one address, then it is closed to it.
	for i := 0; i < 3; i++ {
		e.login("sam", "wrong")
	}
	if _, status, _ := e.login("sam", "dir-password"); status != http.StatusTooManyRequests {
		t.Errorf("after five wrong tries the right password got %d", status)
	}
}

func TestLDAPPerAccountLimitFromManyAddresses(t *testing.T) {
	e := newLDAPEnv(t)
	for i := 0; i < 25; i++ {
		// a different "address" each time, through a proxy the server trusts
		c := e.client()
		e.call(c, "POST", "/api/auth/login", map[string]string{"username": "sam", "password": "wrong"}, "X-Forwarded-For", "203.0.113."+itoa(int64(i+1)))
	}
	c := e.client()
	if status, _ := e.call(c, "POST", "/api/auth/login", map[string]string{"username": "sam", "password": "dir-password"}, "X-Forwarded-For", "203.0.113.200"); status != http.StatusTooManyRequests {
		t.Errorf("the account was not closed after 25 wrong tries from many addresses: %d", status)
	}
}

func TestLDAPNoNetworkForAnEmptyOrOddPassword(t *testing.T) {
	e := newLDAPEnv(t)
	before := e.dir.dialCount()
	for _, tc := range []struct{ user, pw string }{{"sam", ""}, {"", "dir-password"}, {"sam\x00", "dir-password"}, {"sam", "dir\x00password"}, {strings.Repeat("a", 400), "x"}} {
		if _, status, _ := e.login(tc.user, tc.pw); status != http.StatusUnauthorized && status != http.StatusTooManyRequests {
			t.Errorf("%q / %q: %d", tc.user, tc.pw, status)
		}
	}
	if e.dir.dialCount() != before {
		t.Errorf("the directory was contacted %d times for sign-ins that can never work", e.dir.dialCount()-before)
	}
}

func TestLDAPInjectionCharactersNeverChangeTheSearch(t *testing.T) {
	e := newLDAPEnv(t)
	for _, name := range []string{"*", "*)(uid=*", "sam)(|(uid=*", `sam\`, "sam*", "(uid=sam)"} {
		c, status, _ := e.login(name, "dir-password")
		if status == http.StatusOK {
			u, _ := e.signedInAs(c)
			t.Errorf("%q signed in as %q", name, u)
		}
	}
	e.dir.mu.Lock()
	defer e.dir.mu.Unlock()
	for _, f := range e.dir.filters {
		// (&(objectClass=person)(uid=...)) has three pairs and nothing typed adds one
		if strings.Count(f, "(") != 3 || strings.Count(f, ")") != 3 {
			t.Errorf("the search %q has more structure than the one Mediarium wrote", f)
		}
	}
	if n := e.scalar(`SELECT COUNT(*) FROM users`); n != "1" {
		t.Errorf("an account was made: %s", n)
	}
}

func TestLDAPAdministratorGroup(t *testing.T) {
	e := newLDAPEnv(t)
	e.saveConfig(map[string]any{"adminGroupFilter": "(&(objectClass=groupOfNames)(cn=mediarium-admins)(member={dn}))", "groupBaseDn": e.dir.groupsDN})
	ann, status, _ := e.login("ann", "ann-password")
	if _, role := e.signedInAs(ann); status != http.StatusOK || role != "admin" {
		t.Fatalf("ann: %d %s", status, role)
	}
	e.must(ann, "GET", "/api/users", nil, http.StatusOK)
	sam, _, _ := e.login("sam", "dir-password")
	if _, role := e.signedInAs(sam); role != "member" {
		t.Errorf("sam: %s", role)
	}
	// Taken out of the group, the next sign-in makes ann a member again.
	e.dir.users[1].admin = false
	ann2, _, _ := e.login("ann", "ann-password")
	if _, role := e.signedInAs(ann2); role != "member" {
		t.Errorf("after leaving the group: %s", role)
	}
}

func TestLDAPAccountsAreNotMadeWhenSwitchedOff(t *testing.T) {
	e := newLDAPEnv(t)
	e.saveConfig(map[string]any{"autoCreate": false})
	_, status, out := e.login("sam", "dir-password")
	if status != http.StatusForbidden || !strings.Contains(out["_raw"].(string), "no account") {
		t.Errorf("no automatic accounts: %d %v", status, out["_raw"])
	}
	if n := e.scalar(`SELECT COUNT(*) FROM users`); n != "1" {
		t.Errorf("an account was made: %s", n)
	}
}

func TestLDAPDisabledAccount(t *testing.T) {
	e := newLDAPEnv(t)
	sam, _, _ := e.login("sam", "dir-password")
	id := e.scalar(`SELECT id FROM users WHERE username = 'sam'`)
	e.must(e.admin, "PUT", "/api/users/"+id, map[string]any{"disabled": true}, http.StatusOK)
	if got, _ := e.call(sam, "GET", "/api/auth/me", nil); got != http.StatusUnauthorized {
		t.Errorf("open session: %d", got)
	}
	_, status, out := e.login("sam", "dir-password")
	if status != http.StatusForbidden || !strings.Contains(out["_raw"].(string), "turned off") {
		t.Errorf("sign-in: %d %v", status, out["_raw"])
	}
	// A wrong password to a disabled account is still just a wrong password.
	if _, status, _ := e.login("sam", "nope"); status != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", status)
	}
}

// A local account with a password here is never handed to the directory, and a
// directory person never lands on a local account that happens to share a name.
func TestLDAPNeverTakesOverALocalAccount(t *testing.T) {
	e := newLDAPEnv(t)
	e.addAccount("sam", "member") // a local sam with another password
	before := e.dir.dialCount()
	// The directory's sam can't sign in as the local sam with the directory password...
	if _, status, _ := e.login("sam", "dir-password"); status != http.StatusUnauthorized {
		t.Errorf("directory password on a local account: %d", status)
	}
	if e.dir.dialCount() != before {
		t.Errorf("a local name was sent to the directory")
	}
	// ...and the local sam signs in with the local password only.
	c, status, out := e.login("sam", "long-enough-password")
	if status != http.StatusOK || out["source"] != "local" {
		t.Fatalf("local sign-in: %d %v", status, out["_raw"])
	}
	_ = c
	// The same person reaching the directory by another attribute can't take the name either.
	e.saveConfig(map[string]any{"userFilter": "(|(uid={username})(mail={username}))"})
	_, status, out = e.login("sam@example.com", "dir-password")
	if status != http.StatusConflict || !strings.Contains(out["_raw"].(string), "already exists") {
		t.Errorf("name clash: %d %v", status, out["_raw"])
	}
	if n := e.scalar(`SELECT COUNT(*) FROM users WHERE auth_source = 'ldap'`); n != "0" {
		t.Errorf("a directory account was made next to a local one: %s", n)
	}
}

func TestLDAPAccountFollowsTheDirectoryIDNotTheName(t *testing.T) {
	e := newLDAPEnv(t)
	e.login("sam", "dir-password")
	// The person is renamed in the directory; their id stays.
	e.dir.users[0].uid = "samuel"
	e.dir.users[0].mail = "samuel@example.com"
	_, status, _ := e.login("samuel", "dir-password")
	if status != http.StatusOK {
		t.Fatalf("renamed sign-in: %d", status)
	}
	if n := e.scalar(`SELECT COUNT(*) FROM users WHERE auth_source = 'ldap'`); n != "1" {
		t.Errorf("a second account was made for the same person: %s", n)
	}
	// A different person given the old name does not get sam's account.
	e.dir.users = append(e.dir.users, &dirUser{dn: "uid=sam,ou=people,dc=example,dc=com", uid: "sam", cn: "Another Sam", mail: "other@example.com", uuid: "uuid-other", password: "other-password"})
	c, status, _ := e.login("sam", "other-password")
	if status != http.StatusOK {
		t.Fatalf("the new sam: %d", status)
	}
	me := e.must(c, "GET", "/api/auth/me", nil, http.StatusOK)
	if me["name"] == "Sam Example" {
		t.Error("the new person got the old person's account")
	}
}

func TestLDAPDirectoryDownIsNotAWrongPassword(t *testing.T) {
	e := newLDAPEnv(t)
	e.dir.down = true
	for i := 0; i < 8; i++ {
		_, status, out := e.login("sam", "dir-password")
		if status != http.StatusBadGateway || !strings.Contains(out["_raw"].(string), "directory") {
			t.Fatalf("attempt %d: %d %v", i, status, out["_raw"])
		}
	}
	e.dir.down = false
	if _, status, _ := e.login("sam", "dir-password"); status != http.StatusOK {
		t.Errorf("after the directory came back: %d (an outage must not count as wrong passwords)", status)
	}
	// What the person sees doesn't carry the connection error.
	e.dir.down = true
	_, _, out := e.login("sam", "dir-password")
	if strings.Contains(out["_raw"].(string), "refused") || strings.Contains(out["_raw"].(string), "dial tcp") {
		t.Errorf("the answer shows connection details: %v", out["_raw"])
	}
}

func TestLDAPSettings(t *testing.T) {
	e := newLDAPEnv(t)
	got := e.must(e.admin, "GET", "/api/auth/ldap", nil, http.StatusOK)
	if strings.Contains(got["_raw"].(string), e.dir.svcPW) || got["bindPassword"] != "" || got["hasBindPassword"] != true {
		t.Errorf("the bind password leaks or isn't reported as set: %v", got["_raw"])
	}
	// Kept when left blank, for the same server.
	e.saveConfig(map[string]any{"bindPassword": "", "autoCreate": false})
	if _, status, _ := e.login("sam", "dir-password"); status != http.StatusForbidden { // 403: signed in at the directory, no account made
		t.Errorf("after saving with the password left blank: %d", status)
	}
	// Not kept when the server address changes.
	status, out := e.call(e.admin, "PUT", "/api/auth/ldap", e.config(map[string]any{"url": "ldaps://elsewhere.example.com", "bindPassword": ""}))
	if status != http.StatusBadRequest || !strings.Contains(out["_raw"].(string), "password of the bind account") {
		t.Errorf("moved server kept the password: %d %v", status, out["_raw"])
	}
	// A bind account the directory refuses can't be switched on.
	status, out = e.call(e.admin, "PUT", "/api/auth/ldap", e.config(map[string]any{"bindPassword": "wrong"}))
	if status != http.StatusBadRequest || !strings.Contains(out["_raw"].(string), "bind account") {
		t.Errorf("bad bind account: %d %v", status, out["_raw"])
	}
	// Plain ldap:// without StartTLS is refused unless it is allowed on purpose.
	status, out = e.call(e.admin, "PUT", "/api/auth/ldap", e.config(map[string]any{"url": "ldap://ldap.example.com"}))
	if status != http.StatusBadRequest || !strings.Contains(out["_raw"].(string), "StartTLS") {
		t.Errorf("plain ldap: %d %v", status, out["_raw"])
	}
	e.must(e.admin, "PUT", "/api/auth/ldap", e.config(map[string]any{"url": "ldap://ldap.example.com", "startTls": true}), http.StatusOK)
	// A filter without {username} is refused; a draft (off) is kept as typed.
	status, _ = e.call(e.admin, "PUT", "/api/auth/ldap", e.config(map[string]any{"userFilter": "(uid=sam)"}))
	if status != http.StatusBadRequest {
		t.Errorf("a filter without {username}: %d", status)
	}
	e.must(e.admin, "PUT", "/api/auth/ldap", e.config(map[string]any{"enabled": false, "userFilter": "(uid=sam)"}), http.StatusOK)
	// Off means off: nobody is sent to the directory.
	before := e.dir.dialCount()
	if _, status, _ := e.login("ann", "ann-password"); status != http.StatusUnauthorized || e.dir.dialCount() != before {
		t.Errorf("sign-in while off: %d, dials %d -> %d", status, before, e.dir.dialCount())
	}
	if m := e.must(e.client(), "GET", "/api/auth/methods", nil, http.StatusOK); m["ldap"] != false {
		t.Errorf("methods = %v", m)
	}
}

func TestLDAPTestButton(t *testing.T) {
	e := newLDAPEnv(t)
	body := func(user, pw string) map[string]any {
		c := e.config(nil)
		c["bindPassword"] = ""
		c["testUsername"], c["testPassword"] = user, pw
		return c
	}
	out := e.must(e.admin, "POST", "/api/auth/ldap/test", body("sam", ""), http.StatusOK)
	if out["found"] != true || out["dn"] != "uid=sam,ou=people,dc=example,dc=com" || out["email"] != "sam@example.com" || out["passwordChecked"] != false {
		t.Errorf("found: %v", out)
	}
	out = e.must(e.admin, "POST", "/api/auth/ldap/test", body("sam", "dir-password"), http.StatusOK)
	if out["passwordChecked"] != true || out["passwordOk"] != true {
		t.Errorf("good password: %v", out)
	}
	out = e.must(e.admin, "POST", "/api/auth/ldap/test", body("sam", "nope"), http.StatusOK)
	if out["passwordChecked"] != true || out["passwordOk"] != false {
		t.Errorf("bad password: %v", out)
	}
	if out = e.must(e.admin, "POST", "/api/auth/ldap/test", body("ghost", ""), http.StatusOK); out["found"] != false {
		t.Errorf("unknown: %v", out)
	}
	// Testing does not sign anybody in or make an account.
	if n := e.scalar(`SELECT COUNT(*) FROM users`); n != "1" {
		t.Errorf("the test made an account: %s", n)
	}
	e.dir.down = true
	status, o := e.call(e.admin, "POST", "/api/auth/ldap/test", body("sam", ""))
	if status != http.StatusBadGateway || !strings.Contains(o["error"].(string), "ldap.example.com") {
		t.Errorf("down: %d %v", status, o["_raw"])
	}
	e.must(e.admin, "POST", "/api/auth/ldap/test", body("", ""), http.StatusBadRequest)
}

func TestLDAPWorksWhenPasswordSignInIsOff(t *testing.T) {
	e := newLDAPEnv(t)
	e.login("sam", "dir-password")
	// Make sam an administrator so that he can switch the password form off.
	id := e.scalar(`SELECT id FROM users WHERE username = 'sam'`)
	e.must(e.admin, "PUT", "/api/users/"+id, map[string]string{"role": "admin"}, http.StatusOK)
	sam, _, _ := e.login("sam", "dir-password")
	e.must(sam, "PUT", "/api/auth/sign-in", map[string]any{"localSignIn": false}, http.StatusOK)
	if _, status, _ := e.login("ryan", adminPassword); status != http.StatusForbidden {
		t.Errorf("a local account while the password form is off: %d", status)
	}
	if _, status, _ := e.login("sam", "dir-password"); status != http.StatusOK {
		t.Errorf("a directory account while the password form is off: %d", status)
	}
	// The directory going away puts the password form back by itself.
	e.saveConfig(map[string]any{"enabled": false})
	if _, status, _ := e.login("ryan", adminPassword); status != http.StatusOK {
		t.Errorf("after the directory was switched off: %d", status)
	}
}
