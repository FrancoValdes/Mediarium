package torrentclient

import (
	"bytes"
	"fmt"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/rdborg/mediarium/internal/bencsafe"
)

// torrentMetaInfoFromFile reads a .torrent file. The bytes come from an
// indexer, so they are checked for absurd nesting first: the library's decoder
// has no limit and a crafted file would end the whole program.
func torrentMetaInfoFromFile(path string) (*metainfo.MetaInfo, error) {
	data, err := bencsafe.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load .torrent file %s: %w", path, err)
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("load .torrent file %s: %w", path, err)
	}
	return mi, nil
}
