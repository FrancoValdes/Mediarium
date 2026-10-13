package mediaservers

import (
	"context"
	"encoding/json"
	"net/http"
)

// ActiveStreams says how many people are watching something on the server
// right now (a paused stream counts: it is likely to carry on). Servers that
// are not a media server in that sense answer 0.
func (c *Client) ActiveStreams(ctx context.Context, s Server) (int, error) {
	switch s.Kind {
	case KindPlex:
		sessions, err := c.plexGet(ctx, s, "/status/sessions", nil)
		if err != nil {
			return 0, explain(s, err)
		}
		return len(sessions.Metadata) + len(sessions.Video), nil
	case KindJellyfin, KindEmby:
		var list []struct {
			NowPlayingItem json.RawMessage `json:"NowPlayingItem"`
		}
		if err := c.do(ctx, s, http.MethodGet, "/Sessions", nil, nil, &list); err != nil {
			return 0, explain(s, err)
		}
		n := 0
		for _, it := range list {
			if len(it.NowPlayingItem) > 0 && string(it.NowPlayingItem) != "null" {
				n++
			}
		}
		return n, nil
	}
	return 0, nil
}
