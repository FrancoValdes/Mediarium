package ldapauth_test

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/rdborg/mediarium/internal/ldapauth"
)

// fakeDirectory records what the client does and answers from the test.
type fakeDirectory struct {
	dials      int
	startTLS   int
	startTLSOK error
	binds      [][2]string
	searches   []*ldap.SearchRequest
	closed     int

	bindFn   func(dn, pw string) error
	searchFn func(req *ldap.SearchRequest) (*ldap.SearchResult, error)
	dialErr  error
}

func (f *fakeDirectory) client() *ldapauth.Client {
	return &ldapauth.Client{Dial: func(cfg ldapauth.Config) (ldapauth.Conn, error) {
		f.dials++
		if f.dialErr != nil {
			return nil, f.dialErr
		}
		return f, nil
	}}
}
func (f *fakeDirectory) Bind(dn, pw string) error {
	f.binds = append(f.binds, [2]string{dn, pw})
	if f.bindFn != nil {
		return f.bindFn(dn, pw)
	}
	return nil
}
func (f *fakeDirectory) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.searches = append(f.searches, req)
	return f.searchFn(req)
}
func (f *fakeDirectory) StartTLS(*tls.Config) error { f.startTLS++; return f.startTLSOK }
func (f *fakeDirectory) Close() error               { f.closed++; return nil }

func entry(dn string, attrs map[string][]string) *ldap.Entry { return ldap.NewEntry(dn, attrs) }

func result(entries ...*ldap.Entry) *ldap.SearchResult { return &ldap.SearchResult{Entries: entries} }

func baseConfig() ldapauth.Config {
	return ldapauth.Config{
		Enabled: true, URL: "ldaps://ldap.example.com:636", BindDN: "cn=search,dc=example,dc=com", BindPassword: "service-secret",
		BaseDN: "dc=example,dc=com", UserFilter: "(&(objectClass=person)(uid={username}))",
	}
}

var sam = entry("uid=sam,ou=people,dc=example,dc=com", map[string][]string{
	"uid": {"sam"}, "cn": {"Sam Example"}, "mail": {"sam@example.com"}, "entryUUID": {"1111-2222"},
})

// The directory answers for sam only when the filter is exactly what we
// expect for him.
func samDirectory(wantFilter string) *fakeDirectory {
	f := &fakeDirectory{}
	f.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
		if req.Filter == wantFilter {
			return result(sam), nil
		}
		return result(), nil
	}
	f.bindFn = func(dn, pw string) error {
		if dn == "cn=search,dc=example,dc=com" && pw == "service-secret" {
			return nil
		}
		if dn == sam.DN && pw == "right-password" {
			return nil
		}
		return ldap.NewError(ldap.LDAPResultInvalidCredentials, errors.New("bad"))
	}
	return f
}

func TestSignInSucceeds(t *testing.T) {
	f := samDirectory("(&(objectClass=person)(uid=sam))")
	e, err := f.client().Authenticate(context.Background(), baseConfig(), "sam", "right-password")
	if err != nil {
		t.Fatal(err)
	}
	if e.DN != sam.DN || e.Username != "sam" || e.Name != "Sam Example" || e.Email != "sam@example.com" || e.ID != "1111-2222" || e.Admin {
		t.Errorf("entry = %+v", e)
	}
	// The service account searched first, then the person's own bind checked the password.
	if len(f.binds) != 2 || f.binds[0][0] != "cn=search,dc=example,dc=com" || f.binds[1] != [2]string{sam.DN, "right-password"} {
		t.Errorf("binds = %v", f.binds)
	}
	if f.closed != 1 {
		t.Errorf("connection closed %d times", f.closed)
	}
	if req := f.searches[0]; req.BaseDN != "dc=example,dc=com" || req.Scope != ldap.ScopeWholeSubtree || req.SizeLimit != 2 {
		t.Errorf("search = %+v", req)
	}
}

func TestSignInFailures(t *testing.T) {
	cases := []struct {
		name     string
		user, pw string
		wantErr  error
		dials    int // how many connections the attempt may open
	}{
		{"wrong password", "sam", "wrong", ldapauth.ErrInvalidCredentials, 1},
		{"unknown user", "nobody", "right-password", ldapauth.ErrInvalidCredentials, 1},
		{"empty password", "sam", "", ldapauth.ErrInvalidCredentials, 0},
		{"empty user", "", "right-password", ldapauth.ErrInvalidCredentials, 0},
		{"NUL in the password", "sam", "right\x00password", ldapauth.ErrInvalidCredentials, 0},
		{"NUL in the user name", "sam\x00", "right-password", ldapauth.ErrInvalidCredentials, 0},
		{"newline in the user name", "sam\n", "right-password", ldapauth.ErrInvalidCredentials, 0},
		{"huge user name", strings.Repeat("a", 300), "right-password", ldapauth.ErrInvalidCredentials, 0},
		{"huge password", "sam", strings.Repeat("a", 2000), ldapauth.ErrInvalidCredentials, 0},
		{"invalid UTF-8", "sam\xff", "right-password", ldapauth.ErrInvalidCredentials, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := samDirectory("(&(objectClass=person)(uid=sam))")
			e, err := f.client().Authenticate(context.Background(), baseConfig(), tc.user, tc.pw)
			if !errors.Is(err, tc.wantErr) || e != nil {
				t.Fatalf("got %+v, %v; want %v", e, err, tc.wantErr)
			}
			if f.dials > tc.dials {
				t.Errorf("opened %d connections, want at most %d", f.dials, tc.dials)
			}
			// A password is never sent to the directory for an entry that was not found.
			for _, b := range f.binds {
				if b[0] != "cn=search,dc=example,dc=com" && b[0] != sam.DN {
					t.Errorf("bound as %q", b[0])
				}
			}
		})
	}
}

// Characters that mean something in a filter must not change the search.
func TestFilterInjection(t *testing.T) {
	cases := map[string]string{
		"*":                              `(&(objectClass=person)(uid=\2a))`,
		"sam)(uid=*":                     `(&(objectClass=person)(uid=sam\29\28uid=\2a))`,
		"*)(|(uid=*":                     `(&(objectClass=person)(uid=\2a\29\28|\28uid=\2a))`,
		`sam\`:                           `(&(objectClass=person)(uid=sam\5c))`,
		"sam)(objectClass=*))(&(uid=sam": `(&(objectClass=person)(uid=sam\29\28objectClass=\2a\29\29\28&\28uid=sam))`,
		"{username}":                     `(&(objectClass=person)(uid={username}))`,
		"samé":                           `(&(objectClass=person)(uid=sam\c3\a9))`,
	}
	for in, want := range cases {
		f := &fakeDirectory{}
		f.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) { return result(), nil }
		_, err := f.client().Authenticate(context.Background(), baseConfig(), in, "pw")
		if !errors.Is(err, ldapauth.ErrInvalidCredentials) {
			t.Fatalf("%q: %v", in, err)
		}
		if len(f.searches) != 1 || f.searches[0].Filter != want {
			got := ""
			if len(f.searches) > 0 {
				got = f.searches[0].Filter
			}
			t.Errorf("%q searched with %q, want %q", in, got, want)
		}
	}
}

func TestAmbiguousUserSignsNobodyIn(t *testing.T) {
	f := &fakeDirectory{}
	f.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) { return result(sam, sam), nil }
	if _, err := f.client().Authenticate(context.Background(), baseConfig(), "sam", "right-password"); !errors.Is(err, ldapauth.ErrAmbiguous) {
		t.Errorf("err = %v", err)
	}
	// The directory's own size limit message counts the same.
	g := &fakeDirectory{}
	g.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
		return result(sam, sam), ldap.NewError(ldap.LDAPResultSizeLimitExceeded, errors.New("limit"))
	}
	if _, err := g.client().Authenticate(context.Background(), baseConfig(), "sam", "right-password"); !errors.Is(err, ldapauth.ErrAmbiguous) {
		t.Errorf("size limit: err = %v", err)
	}
	for _, b := range append(f.binds, g.binds...) {
		if b[0] == sam.DN {
			t.Errorf("a password was tried for an ambiguous user")
		}
	}
}

func TestDirectoryProblemsAreNotWrongPasswords(t *testing.T) {
	t.Run("cannot connect", func(t *testing.T) {
		f := &fakeDirectory{dialErr: errors.New("connection refused")}
		_, err := f.client().Authenticate(context.Background(), baseConfig(), "sam", "x")
		if !errors.Is(err, ldapauth.ErrUnavailable) || !strings.Contains(err.Error(), "ldap.example.com") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("bind account refused", func(t *testing.T) {
		f := &fakeDirectory{bindFn: func(dn, pw string) error {
			return ldap.NewError(ldap.LDAPResultInvalidCredentials, errors.New("no"))
		}}
		_, err := f.client().Authenticate(context.Background(), baseConfig(), "sam", "x")
		if !errors.Is(err, ldapauth.ErrUnavailable) || !strings.Contains(err.Error(), "bind account") {
			t.Errorf("err = %v", err)
		}
		for _, b := range f.binds {
			if b[1] == "x" {
				t.Errorf("the typed password was sent after the service bind failed")
			}
		}
	})
	t.Run("search fails", func(t *testing.T) {
		f := &fakeDirectory{searchFn: func(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
			return nil, ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("no base"))
		}}
		_, err := f.client().Authenticate(context.Background(), baseConfig(), "sam", "x")
		if !errors.Is(err, ldapauth.ErrUnavailable) || !strings.Contains(err.Error(), "base DN") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestStartTLSBeforeAnyPassword(t *testing.T) {
	cfg := baseConfig()
	cfg.URL = "ldap://ldap.example.com:389"
	cfg.StartTLS = true
	t.Run("ok", func(t *testing.T) {
		f := samDirectory("(&(objectClass=person)(uid=sam))")
		if _, err := f.client().Authenticate(context.Background(), cfg, "sam", "right-password"); err != nil {
			t.Fatal(err)
		}
		if f.startTLS != 1 {
			t.Errorf("StartTLS called %d times", f.startTLS)
		}
	})
	t.Run("fails: nothing is sent in the clear", func(t *testing.T) {
		f := samDirectory("(&(objectClass=person)(uid=sam))")
		f.startTLSOK = errors.New("tls: handshake failure")
		_, err := f.client().Authenticate(context.Background(), cfg, "sam", "right-password")
		if !errors.Is(err, ldapauth.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
		if len(f.binds) != 0 {
			t.Errorf("bound without encryption: %v", f.binds)
		}
	})
}

func TestAdminGroup(t *testing.T) {
	cfg := baseConfig()
	cfg.AdminGroupFilter = "(&(objectClass=groupOfNames)(cn=mediarium-admins)(member={dn}))"
	cfg.GroupBaseDN = "ou=groups,dc=example,dc=com"
	run := func(dn string, member bool) (*ldapauth.Entry, *fakeDirectory) {
		f := &fakeDirectory{}
		f.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
			if req.BaseDN == "ou=groups,dc=example,dc=com" {
				if member {
					return result(entry("cn=mediarium-admins,ou=groups,dc=example,dc=com", nil)), nil
				}
				return result(), nil
			}
			return result(entry(dn, map[string][]string{"uid": {"sam"}})), nil
		}
		e, err := f.client().Authenticate(context.Background(), cfg, "sam", "pw")
		if err != nil {
			t.Fatal(err)
		}
		return e, f
	}
	e, f := run("uid=sam,dc=example,dc=com", true)
	if !e.Admin {
		t.Error("a member of the group was not an administrator")
	}
	if got := f.searches[1].Filter; got != "(&(objectClass=groupOfNames)(cn=mediarium-admins)(member=uid=sam,dc=example,dc=com))" {
		t.Errorf("group filter = %q", got)
	}
	if e, _ := run("uid=sam,dc=example,dc=com", false); e.Admin {
		t.Error("a non-member became an administrator")
	}
	// A DN with filter characters in it is escaped inside the group filter too.
	_, f = run("cn=a)(cn=*,dc=example,dc=com", true)
	if got := f.searches[1].Filter; !strings.Contains(got, `(member=cn=a\29\28cn=\2a,dc=example,dc=com)`) {
		t.Errorf("group filter = %q", got)
	}
}

func TestPlaceholdersAreExpandedOnce(t *testing.T) {
	cfg := baseConfig()
	cfg.AdminGroupFilter = "(&(cn=admins)(member={dn})(memberUid={username}))"
	f := &fakeDirectory{}
	f.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
		if req.BaseDN == cfg.BaseDN && strings.HasPrefix(req.Filter, "(&(objectClass=person)") {
			return result(entry("uid={dn},dc=example,dc=com", map[string][]string{"uid": {"{dn}"}})), nil
		}
		return result(), nil
	}
	if _, err := f.client().Authenticate(context.Background(), cfg, "{dn}", "pw"); err != nil {
		t.Fatal(err)
	}
	got := f.searches[1].Filter
	want := "(&(cn=admins)(member=uid={dn},dc=example,dc=com)(memberUid={dn}))"
	if got != want {
		t.Errorf("group filter = %q, want %q", got, want)
	}
}

func TestBinaryIDIsHex(t *testing.T) {
	cfg := baseConfig()
	cfg.IDAttr = "objectGUID"
	f := &fakeDirectory{}
	e := ldap.NewEntry("cn=sam,dc=example,dc=com", nil)
	e.Attributes = []*ldap.EntryAttribute{{Name: "objectGUID", ByteValues: [][]byte{{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}}}, {Name: "uid", Values: []string{"sam"}, ByteValues: [][]byte{[]byte("sam")}}}
	f.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) { return result(e), nil }
	got, err := f.client().Authenticate(context.Background(), cfg, "sam", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "deadbeef0001" {
		t.Errorf("id = %q", got.ID)
	}
}

func TestMissingIDFallsBackToDN(t *testing.T) {
	f := &fakeDirectory{}
	f.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
		return result(entry("UID=Sam,DC=Example,DC=com", map[string][]string{"uid": {"sam"}})), nil
	}
	got, err := f.client().Authenticate(context.Background(), baseConfig(), "sam", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "uid=sam,dc=example,dc=com" || !got.IDIsDN {
		t.Errorf("id = %q (from the DN: %v)", got.ID, got.IDIsDN)
	}
}

func TestConfigCheck(t *testing.T) {
	ok := baseConfig()
	if err := ok.Check(); err != nil {
		t.Fatal(err)
	}
	mod := func(f func(c *ldapauth.Config)) ldapauth.Config { c := baseConfig(); f(&c); return c }
	bad := map[string]ldapauth.Config{
		"no server":                  mod(func(c *ldapauth.Config) { c.URL = "" }),
		"not an ldap scheme":         mod(func(c *ldapauth.Config) { c.URL = "https://ldap.example.com" }),
		"a path":                     mod(func(c *ldapauth.Config) { c.URL = "ldaps://ldap.example.com/dc=x" }),
		"a user in the address":      mod(func(c *ldapauth.Config) { c.URL = "ldaps://admin:pw@ldap.example.com" }),
		"plain ldap, no startTLS":    mod(func(c *ldapauth.Config) { c.URL = "ldap://ldap.example.com" }),
		"no base DN":                 mod(func(c *ldapauth.Config) { c.BaseDN = "" }),
		"bind DN without a password": mod(func(c *ldapauth.Config) { c.BindPassword = "" }),
		"no {username} in filter":    mod(func(c *ldapauth.Config) { c.UserFilter = "(uid=sam)" }),
		"broken filter":              mod(func(c *ldapauth.Config) { c.UserFilter = "(uid={username}" }),
		"broken group filter":        mod(func(c *ldapauth.Config) { c.AdminGroupFilter = "(member={dn}" }),
		"group filter about nobody":  mod(func(c *ldapauth.Config) { c.AdminGroupFilter = "(cn=admins)" }),
		"bad CA":                     mod(func(c *ldapauth.Config) { c.CACert = "not a certificate" }),
	}
	for name, c := range bad {
		if c.Check() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	good := []ldapauth.Config{
		mod(func(c *ldapauth.Config) { c.URL = "ldap://ldap.example.com"; c.StartTLS = true }),
		mod(func(c *ldapauth.Config) { c.URL = "ldap://ldap.example.com"; c.AllowPlain = true }),
		mod(func(c *ldapauth.Config) { c.BindDN, c.BindPassword = "", "" }),
		mod(func(c *ldapauth.Config) { c.UserFilter = "(|(uid={username})(mail={username}))" }),
	}
	for i, c := range good {
		if err := c.Check(); err != nil {
			t.Errorf("good %d refused: %v", i, err)
		}
	}
}

func TestDefaults(t *testing.T) {
	d := (ldapauth.Config{URL: "ldaps://x", BaseDN: "dc=x"}).Defaults()
	if d.UsernameAttr != "uid" || d.NameAttr != "cn" || d.EmailAttr != "mail" || d.IDAttr != "entryUUID" || d.GroupBaseDN != "dc=x" || d.DefaultRole != "member" || d.TimeoutSeconds != 10 {
		t.Errorf("defaults = %+v", d)
	}
	if (ldapauth.Config{TimeoutSeconds: 9999}).Defaults().TimeoutSeconds != 60 {
		t.Error("timeout is not capped")
	}
	if (ldapauth.Config{DefaultRole: "admin"}).Defaults().DefaultRole != "member" {
		t.Error("an LDAP default role can never be admin")
	}
}

func TestTestReport(t *testing.T) {
	cfg := baseConfig()
	f := samDirectory("(&(objectClass=person)(uid=sam))")
	rep, err := f.client().Test(context.Background(), cfg, "sam", "")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || rep.DN != sam.DN || rep.Email != "sam@example.com" || rep.PasswordChecked {
		t.Errorf("report = %+v", rep)
	}
	rep, _ = samDirectory("(&(objectClass=person)(uid=sam))").client().Test(context.Background(), cfg, "sam", "right-password")
	if !rep.PasswordChecked || !rep.PasswordOK {
		t.Errorf("good password: %+v", rep)
	}
	rep, _ = samDirectory("(&(objectClass=person)(uid=sam))").client().Test(context.Background(), cfg, "sam", "nope")
	if !rep.PasswordChecked || rep.PasswordOK {
		t.Errorf("bad password: %+v", rep)
	}
	rep, err = samDirectory("x").client().Test(context.Background(), cfg, "ghost", "")
	if err != nil || rep.Found {
		t.Errorf("unknown user: %+v, %v", rep, err)
	}
	if _, err := samDirectory("x").client().Test(context.Background(), cfg, "", ""); err == nil {
		t.Error("tested without a user name")
	}
}

func TestHostPort(t *testing.T) {
	for in, want := range map[string]string{
		"ldaps://ldap.example.com": "ldap.example.com:636", "ldap://ldap.example.com": "ldap.example.com:389", "ldaps://ldap.example.com:1636": "ldap.example.com:1636",
	} {
		if got := (ldapauth.Config{URL: in}).HostPort(); got != want {
			t.Errorf("HostPort(%q) = %q, want %q", in, got, want)
		}
	}
}
