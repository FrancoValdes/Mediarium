package torrentclient

import (
	"bytes"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	pp "github.com/anacrolix/torrent/peer_protocol"
)

func TestSpoilNestingBomb(t *testing.T) {
	header, err := bencode.Marshal(pp.ExtendedMetadataRequestMsg{
		Type:      pp.DataMetadataExtensionMsgType,
		Piece:     3,
		TotalSize: 40000,
	})
	if err != nil {
		t.Fatal(err)
	}
	realData := make([]byte, 16384)
	for i := range realData {
		realData[i] = byte(i*131 + i/7) // stands in for piece hashes
	}

	bomb := append(append([]byte{}, header...), bytes.Repeat([]byte("l"), 16384)...)
	if !spoilNestingBomb(bomb) {
		t.Fatal("a chunk of nothing but lists was let through")
	}
	var d pp.ExtendedMetadataRequestMsg
	if err := bencode.Unmarshal(bomb, &d); err == nil {
		t.Fatal("a spoiled message must no longer decode")
	}

	real := append(append([]byte{}, header...), realData...)
	before := append([]byte{}, real...)
	if spoilNestingBomb(real) {
		t.Fatal("real metadata was spoiled")
	}
	if !bytes.Equal(real, before) {
		t.Fatal("real metadata was changed")
	}
	if spoilNestingBomb(nil) {
		t.Fatal("nothing to spoil")
	}
}
