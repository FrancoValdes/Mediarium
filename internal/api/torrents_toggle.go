package api

import (
	"errors"
	"net/http"

	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/settings"
)

// errTorrentsDisabled is what a torrent grab is refused with while the
// torrent switch (Settings > Downloading > Torrents) is off. It is written for the person
// reading it, so it is passed to the UI as is.
var errTorrentsDisabled = errors.New("Torrents are switched off. Switch them on in Settings > Downloading > Torrents to download torrent releases.")

// torrentsEnabled reports the torrent on/off switch. On unless explicitly
// turned off, so upgrading never changes anyone's behavior underneath them.
// torrentReleasesOn reports whether torrent releases may be searched for and
// grabbed at all: with the built-in client on, or a debrid service or an
// external download client to send them to.
func (s *Server) torrentReleasesOn() bool {
	return s.torrentsEnabled() || s.debridOn() || s.downloadClientOn()
}

func (s *Server) torrentsEnabled() bool {
	v, _ := s.Settings.Get(settings.KeyTorrentEnabled)
	return v != "0"
}

// setTorrentsEnabled stores the switch and, when turning torrents off, shuts
// every running torrent client down (downloads in flight fail with
// errTorrentsDisabled; seeding stops).
func (s *Server) setTorrentsEnabled(enabled bool) error {
	value := "1"
	if !enabled {
		value = "0"
	}
	if err := s.Settings.Set(settings.KeyTorrentEnabled, value, false); err != nil {
		return err
	}
	if !enabled {
		s.torrents.stopAll()
	}
	return nil
}

// searchableIndexers is the indexer list searches and automation use: with
// nowhere to send torrents (the built-in client off and no debrid service or
// download client), torrent indexers are skipped so they are never queried.
func (s *Server) searchableIndexers() ([]indexers.Instance, error) {
	all, err := s.IndexerRepo.List()
	if err != nil || s.torrentReleasesOn() {
		return all, err
	}
	out := make([]indexers.Instance, 0, len(all))
	for _, inst := range all {
		if inst.Protocol != indexers.ProtocolTorrent {
			out = append(out, inst)
		}
	}
	return out, nil
}

// writeGrabError maps a grab failure onto a status: a refusal because torrents
// are off is a conflict with the current settings, anything else keeps
// fallback.
func writeGrabError(w http.ResponseWriter, err error, fallback int) {
	if isAlreadyDownloading(err) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, errTorrentsDisabled) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeError(w, fallback, err.Error())
}
