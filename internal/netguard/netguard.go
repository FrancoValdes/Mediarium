// Package netguard limits where Mediarium's own outgoing connections may go.
//
// Indexer, media server, webhook and download addresses are typed in by
// people (and, for download links, come from indexers). Without a check a
// crafted address could make the server talk to something it should never
// reach on its own: above all the cloud metadata service that hands out
// machine credentials (169.254.169.254 and friends), which is the classic
// server-side request forgery target.
//
// The check runs in the dialer, on the address that is actually about to be
// connected to, after DNS. That covers host names that resolve to a blocked
// address, redirects to one, and DNS rebinding, which a check on the typed
// URL cannot.
//
// Loopback and private (LAN) addresses stay reachable on purpose: Radarr-style
// setups keep their indexers, FlareSolverr, media servers and download clients
// on localhost or the local network, and only administrators can configure
// those addresses.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is wrapped by the error a refused connection returns.
var ErrBlocked = errors.New("address not allowed")

var (
	// Addresses of cloud metadata services outside the link-local range
	// (169.254.0.0/16 and fe80::/10 are refused as a whole).
	metadata = []netip.Addr{
		netip.MustParseAddr("fd00:ec2::254"),   // AWS, IPv6
		netip.MustParseAddr("100.100.100.200"), // Alibaba Cloud
		netip.MustParseAddr("192.0.0.192"),     // Oracle Cloud
		netip.MustParseAddr("168.63.129.16"),   // Azure's host service (WireServer)
	}
	thisNetwork = netip.MustParsePrefix("0.0.0.0/8")

	// Ways an IPv4 address is written inside an IPv6 one. A network that
	// translates them (NAT64) or an old stack that accepts them would carry a
	// connection to the IPv4 address inside, so it is judged as that address.
	nat64      = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local = netip.MustParsePrefix("64:ff9b:1::/48") // local-use NAT64 (RFC 8215), usually with the IPv4 address last
	sixToFour  = netip.MustParsePrefix("2002::/16")
	teredo     = netip.MustParsePrefix("2001::/32") // the client's IPv4 address is the last 32 bits, inverted
	ipv4Compat = netip.MustParsePrefix("::/96")
	siit       = netip.MustParsePrefix("::ffff:0:0:0/96") // IPv4-translated (RFC 2765)
)

// embeddedIPv4 returns the IPv4 address hidden inside an IPv6 address written
// as NAT64 (64:ff9b::a.b.c.d, or the local-use 64:ff9b:1::/48), 6to4
// (2002:aabb:ccdd::), Teredo (2001::/32) or IPv4-compatible (::a.b.c.d).
func embeddedIPv4(addr netip.Addr) (netip.Addr, bool) {
	if !addr.Is6() {
		return netip.Addr{}, false
	}
	b := addr.As16()
	switch {
	case nat64.Contains(addr), nat64Local.Contains(addr), ipv4Compat.Contains(addr), siit.Contains(addr):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case sixToFour.Contains(addr):
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	case teredo.Contains(addr):
		return netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]}), true
	}
	return netip.Addr{}, false
}

// Blocked reports whether Mediarium refuses to connect to addr: link-local
// addresses (which include the AWS, Azure, Google Cloud and DigitalOcean
// metadata address), the other known metadata addresses, the unspecified
// address (which reaches the local machine), multicast and broadcast.
func Blocked(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	if inner, ok := embeddedIPv4(addr); ok && !addr.IsUnspecified() && addr != netip.IPv6Loopback() && Blocked(inner) {
		return true
	}
	switch {
	case !addr.IsValid(),
		addr.IsUnspecified(),
		addr.IsLinkLocalUnicast(),
		addr.IsLinkLocalMulticast(),
		addr.IsMulticast(),
		addr == netip.AddrFrom4([4]byte{255, 255, 255, 255}),
		thisNetwork.Contains(addr):
		return true
	}
	for _, m := range metadata {
		if addr == m {
			return true
		}
	}
	return false
}

// Control is a net.Dialer.Control function that refuses blocked addresses.
func Control(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		// Control is always called with a resolved IP. Something that is not
		// one is not let through on the network types this guards.
		if strings.HasPrefix(network, "tcp") || strings.HasPrefix(network, "udp") || strings.HasPrefix(network, "ip") {
			return fmt.Errorf("%w: %q is not an address", ErrBlocked, address)
		}
		return nil
	}
	if Blocked(addr) {
		return fmt.Errorf("%w: connecting to %s is blocked (link-local and cloud metadata addresses are never used)", ErrBlocked, addr.Unmap())
	}
	return nil
}

// CheckHost is the Blocked check for a connection that Mediarium doesn't
// make itself: one a proxy makes for it, where the dial-time check only sees
// the proxy. hostport is "host:port" or a host. An IP address is checked as
// it is. A name is looked up here and refused when any of its addresses is
// blocked, unless resolve is false (a socks5h proxy, where names are only ever
// resolved by the proxy so they don't leak); a name that can't be looked up
// here is left to the proxy, which may know names this machine doesn't.
func CheckHost(ctx context.Context, hostport string, resolve bool) error {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return nil
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if Blocked(addr) {
			return fmt.Errorf("%w: connecting to %s is blocked (link-local and cloud metadata addresses are never used)", ErrBlocked, addr.Unmap().WithZone(""))
		}
		return nil
	}
	if !resolve {
		return nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if Blocked(a) {
			return fmt.Errorf("%w: %s points to %s, which is blocked (link-local and cloud metadata addresses are never used)", ErrBlocked, host, a.Unmap().WithZone(""))
		}
	}
	return nil
}

// cgnat is the shared address space (100.64.0.0/10) providers use inside their networks.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// ControlPublic is Control for places that only ever talk to the public
// internet (a browser's push service, for example): private, loopback and
// shared addresses are refused as well, so an address a person typed in can't
// make the server call something on the home network.
func ControlPublic(network, address string, c syscall.RawConn) error {
	if err := Control(network, address, c); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil
	}
	if NotPublic(addr) {
		return fmt.Errorf("%w: %s is not a public address", ErrBlocked, addr.Unmap())
	}
	return nil
}

// NotPublic reports whether addr is a private, loopback or shared-space
// address: somewhere on the home network or the machine itself. An IPv4
// address written inside an IPv6 one (see embeddedIPv4) is judged as itself,
// and the local-use NAT64 range is never public.
func NotPublic(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	if addr.IsPrivate() || addr.IsLoopback() || cgnat.Contains(addr) || nat64Local.Contains(addr) {
		return true
	}
	if inner, ok := embeddedIPv4(addr); ok && !addr.IsUnspecified() {
		return NotPublic(inner)
	}
	return false
}

// PublicClient is Client for public-internet-only destinations.
func PublicClient(timeout time.Duration) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: ControlPublic}).DialContext
	return &http.Client{Timeout: timeout, Transport: t, CheckRedirect: checkRedirect}
}

// Dialer is a net.Dialer with the address check.
func Dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second, Control: Control}
}

// Transport is a copy of http.DefaultTransport that dials through the check.
func Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = Dialer(30 * time.Second).DialContext
	return t
}

// Client is an http.Client with the given timeout whose connections go
// through the check. When a server redirects to another host, the request that
// follows carries only the plain headers (see stripCredentials): media servers,
// Gotify and others authenticate with a header such as X-Plex-Token, which
// Go itself keeps across a redirect to another site.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Transport(), CheckRedirect: checkRedirect}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if len(via) > 0 && (!strings.EqualFold(req.URL.Host, via[0].URL.Host) || downgraded(via, req)) {
		stripCredentials(req.Header)
	}
	return nil
}

// downgraded reports whether a redirect chain that began (or passed) over
// https goes on over plain http, where a token header would travel in clear
// text even on the same host.
func downgraded(via []*http.Request, req *http.Request) bool {
	if !strings.EqualFold(req.URL.Scheme, "http") {
		return false
	}
	for _, v := range via {
		if strings.EqualFold(v.URL.Scheme, "https") {
			return true
		}
	}
	return false
}

// plainHeaders are the headers that are safe to send to any site.
var plainHeaders = map[string]bool{
	"User-Agent": true, "Accept": true, "Accept-Encoding": true, "Accept-Language": true,
	"Content-Type": true, "Content-Length": true,
}

// stripCredentials removes every header but the plain ones, so a token or key
// sent in a header of the original request does not follow a redirect to
// another host.
func stripCredentials(h http.Header) {
	for k := range h {
		if !plainHeaders[http.CanonicalHeaderKey(k)] {
			delete(h, k)
		}
	}
}

// RedactURL removes what must not be shown or logged from a URL: any
// user:password and the values of query parameters (Newznab links carry the
// indexer key as apikey=...). The path stays, since it says what was
// requested. Anything that is not a URL is returned unchanged.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Not a URL Go can read (a bad escape, a control character, no host),
		// but it may still carry a key: hide what can be hidden by hand.
		return redactText(raw)
	}
	u.User = nil
	u.Fragment, u.RawFragment = "", "" // never sent to a server, but can hold a token
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			q.Set(k, "REDACTED")
		}
		u.RawQuery = q.Encode()
	}
	// Some indexers put the key in the path (/api/<key>/...): hide long opaque segments.
	segs := strings.Split(u.Path, "/")
	for i, s := range segs {
		if len(s) >= 24 && isKeyLike(s) {
			segs[i] = "REDACTED"
		}
	}
	u.Path = strings.Join(segs, "/")
	u.RawPath = ""
	return u.String()
}

// redactText is RedactURL for text that does not parse as a URL: it drops
// user:password@, the fragment and every query value, and hides long opaque
// path segments.
func redactText(raw string) string {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	head, query, hasQuery := strings.Cut(raw, "?")
	if i := strings.Index(head, "//"); i >= 0 {
		authority := head[i+2:]
		end := strings.IndexByte(authority, '/')
		if end < 0 {
			end = len(authority)
		}
		if at := strings.LastIndexByte(authority[:end], '@'); at >= 0 {
			head = head[:i+2] + authority[at+1:]
		}
	}
	segs := strings.Split(head, "/")
	for i, s := range segs {
		if len(s) >= 24 && isKeyLike(s) {
			segs[i] = "REDACTED"
		}
	}
	head = strings.Join(segs, "/")
	if !hasQuery {
		return head
	}
	pairs := strings.FieldsFunc(query, func(r rune) bool { return r == '&' || r == ';' })
	for i, p := range pairs {
		if k, _, ok := strings.Cut(p, "="); ok {
			pairs[i] = k + "=REDACTED"
		}
	}
	return head + "?" + strings.Join(pairs, "&")
}

func isKeyLike(s string) bool {
	digits := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
		default:
			return false
		}
	}
	return digits > 0
}

// CleanError returns err with any URL inside a *url.Error redacted (see
// RedactURL). net/http puts the full request URL, query string included, in
// the errors it returns, which would leak API keys into logs and messages.
func CleanError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return &url.Error{Op: ue.Op, URL: RedactURL(ue.URL), Err: ue.Err}
	}
	return err
}
