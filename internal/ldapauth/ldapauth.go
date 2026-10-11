// Package ldapauth checks a person's user name and password against a
// directory: OpenLDAP, Active Directory, FreeIPA, LLDAP, GLAuth and the like.
//
// The way it works is the usual "search, then bind": a service account looks
// the person up with a filter, then the server is asked to check the person's
// own password by binding as the entry that was found. No password is ever
// stored; the one the person types is used for that one bind and dropped.
//
// Things this package is strict about, because this is a sign-in:
//   - the user name goes into the search filter escaped (RFC 4515), so
//     characters like * ( ) \ cannot change what is searched for;
//   - an empty password is refused before any network call (many servers treat
//     a bind with an empty password as an anonymous, successful one);
//   - the connection is encrypted (ldaps:// or StartTLS) and the certificate is
//     checked, unless the administrator explicitly switches that off;
//   - a search that finds no entry or more than one entry signs nobody in;
//   - every network step has a time limit.
package ldapauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-ldap/ldap/v3"
)

// Config is the saved setup of the directory sign-in.
type Config struct {
	Enabled bool `json:"enabled"`

	URL      string `json:"url"`      // ldaps://ldap.example.com:636 or ldap://ldap.example.com:389
	StartTLS bool   `json:"startTls"` // encrypt an ldap:// connection with StartTLS
	// AllowPlain lets an ldap:// connection stay unencrypted. Passwords then
	// cross the network in the clear, so it has to be switched on on purpose.
	AllowPlain bool `json:"allowPlain"`
	// CACert is an extra certificate authority to trust (PEM), for a directory
	// with its own certificate.
	CACert string `json:"caCert"`
	// SkipVerify turns the certificate check off. Not recommended.
	SkipVerify bool `json:"skipVerify"`

	BindDN       string `json:"bindDn"`       // the service account that looks people up
	BindPassword string `json:"bindPassword"` // its password (kept encrypted)
	BaseDN       string `json:"baseDn"`       // where to look for people

	// UserFilter finds one person; {username} stands for what was typed.
	UserFilter   string `json:"userFilter"`
	UsernameAttr string `json:"usernameAttr"` // the account's user name, default uid
	NameAttr     string `json:"nameAttr"`     // default cn
	EmailAttr    string `json:"emailAttr"`    // default mail
	IDAttr       string `json:"idAttr"`       // an id that never changes, default entryUUID

	// AdminGroupFilter, when set, makes the people it finds administrators.
	// {dn} stands for the person's DN and {username} for their user name.
	// It is searched under GroupBaseDN (the base DN when empty).
	AdminGroupFilter string `json:"adminGroupFilter"`
	GroupBaseDN      string `json:"groupBaseDn"`

	AutoCreate  bool   `json:"autoCreate"`  // make the account on the first sign-in
	DefaultRole string `json:"defaultRole"` // "member" (default) or "viewer"

	TimeoutSeconds int `json:"timeoutSeconds"` // for each network step, default 10
}

// Defaults fills what was left empty.
func (c Config) Defaults() Config {
	c.URL = strings.TrimSpace(c.URL)
	c.BindDN = strings.TrimSpace(c.BindDN)
	c.BaseDN = strings.TrimSpace(c.BaseDN)
	c.GroupBaseDN = strings.TrimSpace(c.GroupBaseDN)
	if strings.TrimSpace(c.UserFilter) == "" {
		c.UserFilter = "(&(objectClass=person)(uid={username}))"
	}
	fill := func(p *string, def string) {
		if *p = strings.TrimSpace(*p); *p == "" {
			*p = def
		}
	}
	fill(&c.UsernameAttr, "uid")
	fill(&c.NameAttr, "cn")
	fill(&c.EmailAttr, "mail")
	fill(&c.IDAttr, "entryUUID")
	if c.GroupBaseDN == "" {
		c.GroupBaseDN = c.BaseDN
	}
	if c.DefaultRole != "viewer" {
		c.DefaultRole = "member"
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = 10
	}
	if c.TimeoutSeconds > 60 {
		c.TimeoutSeconds = 60
	}
	return c
}

func (c Config) timeout() time.Duration {
	return time.Duration(c.Defaults().TimeoutSeconds) * time.Second
}

// Check says what is wrong with the setup, in words for the settings page.
func (c Config) Check() error {
	c = c.Defaults()
	u, err := url.Parse(c.URL)
	switch {
	case c.URL == "":
		return errors.New("enter the server address, for example ldaps://ldap.example.com:636")
	case err != nil || u.Hostname() == "" || (u.Scheme != "ldap" && u.Scheme != "ldaps"):
		return errors.New("the server address should start with ldaps:// or ldap://, for example ldaps://ldap.example.com:636")
	case u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.User != nil:
		return errors.New("the server address should only be the scheme, host and port")
	case u.Scheme == "ldap" && !c.StartTLS && !c.AllowPlain:
		return errors.New("an ldap:// address needs StartTLS switched on, or ldaps:// instead. Passwords would cross the network unencrypted otherwise")
	case c.BaseDN == "":
		return errors.New("enter the base DN, where people are looked up, for example dc=example,dc=com")
	case c.BindDN != "" && c.BindPassword == "":
		return errors.New("enter the password of the bind account, or leave the bind account empty to search without signing in")
	}
	f := c.UserFilter
	if !strings.Contains(f, "{username}") {
		return errors.New("the user filter has to contain {username}, for example (&(objectClass=person)(uid={username}))")
	}
	if _, err := ldap.CompileFilter(strings.ReplaceAll(f, "{username}", "x")); err != nil {
		return fmt.Errorf("the user filter isn't a valid LDAP filter: %v", err)
	}
	if g := strings.TrimSpace(c.AdminGroupFilter); g != "" {
		// Without {dn} or {username} the filter says nothing about the person, so every
		// sign-in would be an administrator whenever any such group exists.
		if !strings.Contains(g, "{dn}") && !strings.Contains(g, "{username}") {
			return errors.New("the administrator group filter has to contain {dn} or {username}, for example (&(objectClass=groupOfNames)(cn=admins)(member={dn}))")
		}
		if _, err := ldap.CompileFilter(groupReplacer("x", "cn=x").Replace(g)); err != nil {
			return fmt.Errorf("the administrator group filter isn't a valid LDAP filter: %v", err)
		}
	}
	if c.CACert != "" {
		if _, err := certPool(c.CACert); err != nil {
			return err
		}
	}
	return nil
}

// Entry is the person the directory found.
type Entry struct {
	DN       string
	Username string
	Name     string
	Email    string
	ID       string // the stable id the account is tied to
	// IDIsDN says the directory gave no unchanging id, so the DN stands in for
	// it. The DN changes when a person is moved or renamed, and the account
	// would then not be found again.
	IDIsDN bool
	Admin  bool // in the administrator group
}

// Errors. ErrInvalidCredentials covers a user that does not exist, a wrong
// password and an empty or odd input alike: the caller cannot tell them apart,
// and neither can the person typing.
var (
	ErrInvalidCredentials = errors.New("wrong user name or password")
	ErrAmbiguous          = errors.New("more than one directory entry matches that user name")
	// ErrUnavailable wraps problems with the directory itself (cannot connect,
	// the bind account is refused, the base DN is wrong), which are the
	// administrator's to fix.
	ErrUnavailable = errors.New("the directory can't be used")
)

// Conn is the part of an LDAP connection this package uses.
type Conn interface {
	Bind(dn, password string) error
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	StartTLS(*tls.Config) error
	Close() error
}

// Dialer opens a connection. Mediarium passes one that refuses the cloud
// metadata addresses; tests pass a fake directory.
type Dialer func(cfg Config) (Conn, error)

// Client talks to the directory.
type Client struct {
	Dial Dialer
}

// NewClient makes a Client that connects through the given net.Dialer.
func NewClient(d *net.Dialer) *Client {
	return &Client{Dial: func(cfg Config) (Conn, error) {
		cfg = cfg.Defaults()
		tc, err := tlsConfig(cfg)
		if err != nil {
			return nil, err
		}
		dd := *d
		dd.Timeout = cfg.timeout()
		conn, err := ldap.DialURL(cfg.URL, ldap.DialWithDialer(&dd), ldap.DialWithTLSConfig(tc))
		if err != nil {
			return nil, err
		}
		conn.SetTimeout(cfg.timeout())
		return conn, nil
	}}
}

func certPool(pem string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM([]byte(pem)) {
		return nil, errors.New("the certificate authority isn't a PEM certificate (it should start with -----BEGIN CERTIFICATE-----)")
	}
	return pool, nil
}

func tlsConfig(cfg Config) (*tls.Config, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, err
	}
	tc := &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.SkipVerify} //nolint:gosec // only when the administrator switched the check off
	if strings.TrimSpace(cfg.CACert) != "" {
		pool, err := certPool(cfg.CACert)
		if err != nil {
			return nil, err
		}
		tc.RootCAs = pool
	}
	return tc, nil
}

// session opens a connection, encrypts it when asked and binds as the service account.
func (c *Client) session(cfg Config) (Conn, error) {
	conn, err := c.Dial(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, explain(cfg, err))
	}
	u, _ := url.Parse(cfg.URL)
	if u != nil && u.Scheme == "ldap" && cfg.StartTLS {
		tc, err := tlsConfig(cfg)
		if err == nil {
			err = conn.StartTLS(tc)
		}
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("%w: %s", ErrUnavailable, explain(cfg, err))
		}
	}
	if cfg.BindDN != "" {
		if cfg.BindPassword == "" {
			conn.Close()
			return nil, fmt.Errorf("%w: the bind account has no password", ErrUnavailable)
		}
		if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			conn.Close()
			if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
				return nil, fmt.Errorf("%w: the bind account was refused. Check its DN and password", ErrUnavailable)
			}
			return nil, fmt.Errorf("%w: %s", ErrUnavailable, explain(cfg, err))
		}
	}
	return conn, nil
}

// explain turns a connection error into a sentence that names the likely cause.
func explain(cfg Config, err error) string {
	host := cfg.URL
	if u, e := url.Parse(cfg.URL); e == nil {
		host = u.Host
	}
	var cert x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &cert):
		return "the directory's certificate isn't trusted. Add its certificate authority in the setup"
	case errors.As(err, &hostErr):
		return "the directory's certificate isn't made for " + host + ". Use the name the certificate is made for"
	case errors.As(err, &invalid):
		return "the directory's certificate isn't valid (it may have expired)"
	case ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject):
		return "the base DN wasn't found. Check it"
	case ldap.IsErrorWithCode(err, ldap.LDAPResultInsufficientAccessRights):
		return "the bind account isn't allowed to search there"
	}
	msg := err.Error()
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "no answer from " + host + " in time"
	}
	return "couldn't talk to " + host + " (" + trimMessage(msg) + ")"
}

func trimMessage(s string) string {
	if len(s) > 160 {
		s = s[:160]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

// validInput rejects what can never be a real user name or password before
// any network call.
func validInput(username, password string, needPassword bool) bool {
	if username == "" || len(username) > 256 || !utf8.ValidString(username) {
		return false
	}
	for _, r := range username {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	if needPassword && (password == "" || len(password) > 1024 || strings.ContainsRune(password, 0)) {
		return false
	}
	return true
}

// userFilter puts the typed name, escaped, wherever the template has {username}
// (it can appear more than once, for example to match a user name or an email address).
func userFilter(cfg Config, username string) string {
	return strings.NewReplacer("{username}", ldap.EscapeFilter(username)).Replace(cfg.UserFilter)
}

// groupReplacer substitutes the placeholders in one pass, so a value that
// itself contains the text "{dn}" is not expanded again.
func groupReplacer(username, dn string) *strings.Replacer {
	return strings.NewReplacer("{username}", ldap.EscapeFilter(username), "{dn}", ldap.EscapeFilter(dn))
}

func attrString(e *ldap.Entry, name string) string {
	if name == "" {
		return ""
	}
	raw := e.GetRawAttributeValue(name)
	if len(raw) == 0 {
		return ""
	}
	if utf8.Valid(raw) && !hasControl(raw) {
		return strings.TrimSpace(string(raw))
	}
	// Binary ids (Active Directory's objectGUID): keep them as hex.
	const hexd = "0123456789abcdef"
	b := make([]byte, 0, len(raw)*2)
	for _, c := range raw {
		b = append(b, hexd[c>>4], hexd[c&0xf])
	}
	return string(b)
}

func hasControl(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// find looks the person up with the service account's connection. It returns
// nil when nobody matches.
func (c *Client) find(conn Conn, cfg Config, username string) (*Entry, error) {
	attrs := []string{"dn", cfg.UsernameAttr, cfg.NameAttr, cfg.EmailAttr, cfg.IDAttr}
	req := ldap.NewSearchRequest(cfg.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, int(cfg.timeout().Seconds()), false,
		userFilter(cfg, username), attrs, nil)
	res, err := conn.Search(req)
	if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
		return nil, ErrAmbiguous
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, explain(cfg, err))
	}
	switch len(res.Entries) {
	case 0:
		return nil, nil
	case 1:
	default:
		return nil, ErrAmbiguous
	}
	e := res.Entries[0]
	out := &Entry{DN: e.DN, Username: attrString(e, cfg.UsernameAttr), Name: attrString(e, cfg.NameAttr),
		Email: attrString(e, cfg.EmailAttr), ID: attrString(e, cfg.IDAttr)}
	if out.DN == "" {
		return nil, fmt.Errorf("%w: the directory returned an entry without a DN", ErrUnavailable)
	}
	if out.Username == "" {
		out.Username = username
	}
	if out.ID == "" {
		out.ID = strings.ToLower(out.DN) // no stable id attribute: the DN is the best there is
		out.IDIsDN = true
	}
	if g := strings.TrimSpace(cfg.AdminGroupFilter); g != "" {
		req := ldap.NewSearchRequest(cfg.GroupBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, int(cfg.timeout().Seconds()), false,
			groupReplacer(out.Username, out.DN).Replace(g), []string{"dn"}, nil)
		gres, err := conn.Search(req)
		if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
			err = nil // more than one group matched: still a member
			if gres == nil {
				gres = &ldap.SearchResult{Entries: []*ldap.Entry{{}}}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%w: the administrator group search failed: %s", ErrUnavailable, explain(cfg, err))
		}
		out.Admin = len(gres.Entries) > 0
	}
	return out, nil
}

// Authenticate checks the user name and password against the directory and
// returns the person's entry. ErrInvalidCredentials covers a missing user and
// a wrong password.
func (c *Client) Authenticate(ctx context.Context, cfg Config, username, password string) (*Entry, error) {
	cfg = cfg.Defaults()
	if !validInput(username, password, true) {
		return nil, ErrInvalidCredentials
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.session(cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	entry, err := c.find(conn, cfg, username)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, ErrInvalidCredentials
	}
	// The password is checked by the directory itself, by binding as the person.
	if err := conn.Bind(entry.DN, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, explain(cfg, err))
	}
	return entry, nil
}

// Probe connects and binds as the service account, nothing more. It is what
// saving the setup does before the directory sign-in is switched on.
func (c *Client) Probe(cfg Config) error {
	cfg = cfg.Defaults()
	if err := cfg.Check(); err != nil {
		return err
	}
	conn, err := c.session(cfg)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// Report is what the test button shows.
type Report struct {
	Found           bool   `json:"found"`
	DN              string `json:"dn,omitempty"`
	Username        string `json:"username,omitempty"`
	Name            string `json:"name,omitempty"`
	Email           string `json:"email,omitempty"`
	ID              string `json:"id,omitempty"`
	IDIsDN          bool   `json:"idIsDn"` // no unchanging id was found, so the DN is used
	Admin           bool   `json:"admin"`
	PasswordChecked bool   `json:"passwordChecked"`
	PasswordOK      bool   `json:"passwordOk"`
}

// Test connects, binds the service account and looks the user up, without
// signing anybody in. With a password it also checks it.
func (c *Client) Test(ctx context.Context, cfg Config, username, password string) (*Report, error) {
	cfg = cfg.Defaults()
	if err := cfg.Check(); err != nil {
		return nil, err
	}
	if !validInput(username, password, false) {
		return nil, errors.New("enter a user name to look up")
	}
	conn, err := c.session(cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	entry, err := c.find(conn, cfg, username)
	if err != nil {
		return nil, err
	}
	rep := &Report{}
	if entry == nil {
		return rep, nil
	}
	rep.Found, rep.DN, rep.Username, rep.Name, rep.Email, rep.ID, rep.IDIsDN, rep.Admin = true, entry.DN, entry.Username, entry.Name, entry.Email, entry.ID, entry.IDIsDN, entry.Admin
	if password != "" && validInput(username, password, true) {
		rep.PasswordChecked = true
		if err := conn.Bind(entry.DN, password); err == nil {
			rep.PasswordOK = true
		} else if !ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, fmt.Errorf("%w: %s", ErrUnavailable, explain(cfg, err))
		}
	}
	return rep, nil
}

// HostPort is the host and port of the server, for logs.
func (c Config) HostPort() string {
	u, err := url.Parse(c.URL)
	if err != nil {
		return ""
	}
	if u.Port() == "" {
		p := 389
		if u.Scheme == "ldaps" {
			p = 636
		}
		return net.JoinHostPort(u.Hostname(), strconv.Itoa(p))
	}
	return u.Host
}
