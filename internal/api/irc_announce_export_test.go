package api

import (
	"context"
	"strings"

	"github.com/rdborg/mediarium/internal/announce"
)

// What irc_announce_test.go (package api_test) uses to run the IRC announce
// engine against a fake IRC server on the local machine.

// TestAnnounceTweak changes the client settings of every IRC connection
// before it starts (a local address, short waits).
func (s *Server) TestAnnounceTweak(f func(*announce.Config)) { s.Announce.Tweak = f }

// TestStartAnnounce starts the saved, enabled connections.
func (s *Server) TestStartAnnounce() error { return s.Announce.Start(context.Background()) }

// TestStopAnnounce stops them.
func (s *Server) TestStopAnnounce() { s.Announce.Stop() }

// TestRawIRCRows is every text column of the IRC tables as stored, to check
// that no secret is in the clear.
func (s *Server) TestRawIRCRows() string {
	var out string
	rows, err := s.db.Query("SELECT name, server, nick, auth_account, auth_password_enc, invite_enc, channels, announcers, tracker_url, template, secrets_enc FROM irc_connections")
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	for rows.Next() {
		cols := make([]string, 11)
		ptrs := make([]any, len(cols))
		for i := range cols {
			ptrs[i] = &cols[i]
		}
		_ = rows.Scan(ptrs...)
		out += strings.Join(cols, "|") + "\n"
	}
	return out
}
