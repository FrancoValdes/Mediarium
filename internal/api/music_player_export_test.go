package api

import (
	"net/http"

	"github.com/rdborg/mediarium/internal/listenbrainz"
	"github.com/rdborg/mediarium/internal/lrclib"
)

// TestSetLRCLIB points the online lyrics lookup at a fake server.
func (s *Server) TestSetLRCLIB(baseURL string) {
	s.music.playerMu.Lock()
	defer s.music.playerMu.Unlock()
	s.music.lyrics = lrclib.New("test", lrclib.WithBaseURL(baseURL), lrclib.WithHTTPClient(http.DefaultClient))
}

// TestSetScrobbleServer points ListenBrainz scrobbling at a fake server.
func (s *Server) TestSetScrobbleServer(baseURL string) {
	s.music.playerMu.Lock()
	defer s.music.playerMu.Unlock()
	s.music.scrobble = listenbrainz.NewSubmitter("test", listenbrainz.WithSubmitBaseURL(baseURL), listenbrainz.WithSubmitHTTPClient(http.DefaultClient))
}
