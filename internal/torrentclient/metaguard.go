package torrentclient

import (
	"log/slog"

	"github.com/anacrolix/torrent"
	pp "github.com/anacrolix/torrent/peer_protocol"

	"github.com/rdborg/mediarium/internal/bencsafe"
)

// guardMetadataMessage is the engine's look at every extension message from a
// peer. A magnet link has no file list: the engine asks the peers for it, and
// the library decodes what they send as soon as the last piece is in, before
// it has checked that the data is the torrent the magnet named. So any peer in
// a swarm could feed it a nesting bomb (see package bencsafe) and end the whole
// program.
//
// The pieces cannot be checked one by one for depth, but a piece that is mostly
// list and dictionary openers is not real metadata. Such a piece is spoiled in
// place, which makes the library refuse the message and drop that peer, and the
// data is never stored.
func guardMetadataMessage(ev torrent.PeerConnReadExtensionMessageEvent) {
	if ev.PeerConn == nil || ev.PeerConn.LocalLtepProtocolMap == nil {
		return
	}
	name, builtin, err := ev.PeerConn.LocalLtepProtocolMap.LookupId(ev.ExtensionNumber)
	if err != nil || !builtin || name != pp.ExtensionNameMetadata {
		return
	}
	if spoilNestingBomb(ev.Payload) {
		slog.Warn("a peer sent torrent metadata that is mostly nesting; it was refused")
	}
}

// spoilNestingBomb overwrites the first byte of a metadata message that looks
// like a nesting bomb, so that it no longer decodes. It reports whether it did.
func spoilNestingBomb(payload []byte) bool {
	if len(payload) == 0 || !bencsafe.LooksLikeNestingBomb(payload) {
		return false
	}
	payload[0] = 0
	return true
}
