package mediaservers

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// A Plex account's watchlist lives in Plex's catalogue service, not on a
// server. Reading it needs the account's own token (from the PIN sign-in),
// not a server's access token.

// PlexDiscoverURL is Plex's catalogue service.
const PlexDiscoverURL = "https://discover.provider.plex.tv"

// WatchlistItem is one title on a Plex watchlist.
type WatchlistItem struct {
	Key   string `json:"key"`  // Plex's id for the title (ratingKey)
	Kind  string `json:"kind"` // movie or tv
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
}

// WatchlistIDs are a watchlist title's ids elsewhere.
type WatchlistIDs struct {
	TMDB int
	TVDB int
	IMDB string
}

// watchlistMax keeps a runaway list from turning into thousands of lookups.
const watchlistMax = 500

func (p *PlexTV) discover(ctx context.Context, path string, query url.Values, token string, out any) error {
	base := strings.TrimRight(p.DiscoverURL, "/")
	if base == "" {
		base = PlexDiscoverURL
	}
	return p.callAt(ctx, base, http.MethodGet, path, query, token, out)
}

type plexDiscoverContainer struct {
	MediaContainer struct {
		TotalSize int `json:"totalSize"`
		Metadata  []struct {
			RatingKey string `json:"ratingKey"`
			Type      string `json:"type"`
			Title     string `json:"title"`
			Year      int    `json:"year"`
			GUID      string `json:"guid"`
			Guid      []struct {
				ID string `json:"id"`
			} `json:"Guid"`
		} `json:"Metadata"`
	} `json:"MediaContainer"`
}

// Watchlist lists the movies and shows on the account's watchlist, newest
// first, as Plex keeps it.
func (p *PlexTV) Watchlist(ctx context.Context, accountToken string) ([]WatchlistItem, error) {
	var out []WatchlistItem
	for start := 0; start < watchlistMax; start += 100 {
		q := url.Values{"X-Plex-Container-Start": {strconv.Itoa(start)}, "X-Plex-Container-Size": {"100"}}
		var page plexDiscoverContainer
		if err := p.discover(ctx, "/library/sections/watchlist/all", q, accountToken, &page); err != nil {
			return nil, err
		}
		for _, m := range page.MediaContainer.Metadata {
			kind := ""
			switch m.Type {
			case "movie":
				kind = "movie"
			case "show":
				kind = "tv"
			}
			if kind == "" || m.RatingKey == "" {
				continue
			}
			out = append(out, WatchlistItem{Key: m.RatingKey, Kind: kind, Title: m.Title, Year: m.Year})
		}
		if len(page.MediaContainer.Metadata) < 100 || start+100 >= page.MediaContainer.TotalSize {
			break
		}
	}
	return out, nil
}

// WatchlistTitleIDs looks up the TMDB, TheTVDB and IMDb ids of one title.
func (p *PlexTV) WatchlistTitleIDs(ctx context.Context, accountToken, key string) (WatchlistIDs, error) {
	var res plexDiscoverContainer
	if err := p.discover(ctx, "/library/metadata/"+url.PathEscape(key), nil, accountToken, &res); err != nil {
		return WatchlistIDs{}, err
	}
	var ids WatchlistIDs
	for _, m := range res.MediaContainer.Metadata {
		for _, g := range m.Guid {
			scheme, value, ok := strings.Cut(g.ID, "://")
			if !ok {
				continue
			}
			switch scheme {
			case "tmdb":
				ids.TMDB, _ = strconv.Atoi(value)
			case "tvdb":
				ids.TVDB, _ = strconv.Atoi(value)
			case "imdb":
				ids.IMDB = value
			}
		}
	}
	return ids, nil
}

// PlexAccount is who a Plex account token belongs to.
type PlexAccount struct {
	Username string `json:"username"`
	Title    string `json:"title"`
}

// Account reads the name of the account a token belongs to.
func (p *PlexTV) Account(ctx context.Context, accountToken string) (PlexAccount, error) {
	var a PlexAccount
	if err := p.call(ctx, http.MethodGet, "/api/v2/user", nil, accountToken, &a); err != nil {
		return PlexAccount{}, err
	}
	return a, nil
}
