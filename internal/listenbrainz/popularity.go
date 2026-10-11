package listenbrainz

import (
	"context"
	"fmt"
	"net/url"
)

// RecordingStat is one recording in an artist's top list.
type RecordingStat struct {
	Name        string
	ListenCount int
}

// TopRecordings returns the most listened-to recordings of an artist, most
// listened first (GET /1/popularity/top-recordings-for-artist/{artist_mbid}).
// It needs no account. The answers are kept in memory like the other lists.
func (c *Client) TopRecordings(ctx context.Context, artistMBID string) ([]RecordingStat, error) {
	if artistMBID == "" {
		return nil, nil
	}
	var resp []struct {
		Name        string `json:"recording_name"`
		ListenCount int    `json:"total_listen_count"`
	}
	if err := c.get(ctx, "/1/popularity/top-recordings-for-artist/"+url.PathEscape(artistMBID), nil, &resp); err != nil {
		return nil, fmt.Errorf("top recordings: %w", err)
	}
	out := make([]RecordingStat, 0, len(resp))
	for _, r := range resp {
		if r.Name != "" {
			out = append(out, RecordingStat{Name: r.Name, ListenCount: r.ListenCount})
		}
	}
	return out, nil
}
