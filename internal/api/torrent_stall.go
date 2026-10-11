package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/settings"
	"github.com/rdborg/mediarium/internal/torrentclient"
)

// maxStallHours is the longest a stall limit can be set to.
const maxStallHours = 72

// stallHours is the setting as a number: -1 when unset (each client keeps its
// own default), 0 for never, else the hours.
func (s *Server) stallHours() int {
	v, _ := s.Settings.Get(settings.KeyStallHours)
	v = strings.TrimSpace(v)
	if v == "" {
		return -1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > maxStallHours {
		return -1
	}
	return n
}

// torrentStall is how long a torrent may get no data before it is given up on,
// blocklisted and searched for again. 0 means never. external is a torrent in a
// download client (qBittorrent and the like), which waits longer by default
// than the built-in client does.
func (s *Server) torrentStall(external bool) time.Duration {
	if h := s.stallHours(); h >= 0 {
		return time.Duration(h) * time.Hour
	}
	if external {
		return downloadClientStall
	}
	return torrentclient.StallAfter
}
