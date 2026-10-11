package api

import (
	"time"

	"github.com/rdborg/mediarium/internal/ldapauth"
)

// TestRoutes lists every signed-in route with its access level ("member" or
// "admin"), for the tests that walk the whole route table.
func (s *Server) TestRoutes() map[string]string {
	out := map[string]string{}
	for p, level := range s.protectedRoutes().access {
		out[p] = string(level)
	}
	return out
}

// TestSetLDAPDial replaces how the directory is reached with a fake one.
func (s *Server) TestSetLDAPDial(d ldapauth.Dialer) {
	s.ssoS.ldap = &ldapauth.Client{Dial: d}
}

// TestCeremonyCount is how many passkey challenges are waiting.
func (s *Server) TestCeremonyCount() int {
	c := s.sso().ceremonies
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// TestExpireCeremonies makes every waiting passkey challenge too old.
func (s *Server) TestExpireCeremonies() {
	c := s.sso().ceremonies
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range c.m {
		v.expires = time.Now().Add(-time.Second)
	}
}

// PasskeyOriginOf exposes the origin and relying party id derivation.
func PasskeyOriginOf(base string) (origin, rpID string) { return passkeyOriginOf(base) }
