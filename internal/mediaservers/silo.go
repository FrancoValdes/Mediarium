package mediaservers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// KindSilo is Silo (siloserver.org), a self-hosted media server. Mediarium
// talks to its own API at the address you open it at, with an administrator's
// API key (Admin > API keys) sent as "Authorization: Bearer sa_…". Silo also
// has a Jellyfin-compatible port, but that one has no full-library scan and
// can't look titles up by their TMDB id, so it isn't used.
const KindSilo Kind = "silo"

type siloLibrary struct {
	ID      int      `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Paths   []string `json:"paths"`
	Enabled bool     `json:"enabled"`
}

type siloBuild struct {
	Display     string `json:"display"`
	BuildNumber int    `json:"build_number"`
}

func (c *Client) siloLibraries(ctx context.Context, s Server) ([]siloLibrary, error) {
	var libs []siloLibrary
	if err := c.do(ctx, s, http.MethodGet, "/api/v1/libraries/", nil, nil, &libs); err != nil {
		return nil, explain(s, err)
	}
	return libs, nil
}

func (c *Client) siloTest(ctx context.Context, s Server) (TestResult, error) {
	var build siloBuild
	err := c.do(ctx, s, http.MethodGet, "/api/v1/admin/system/build", nil, nil, &build)
	if errors.Is(err, errNotFound) {
		// The Jellyfin-compatible port answers /System/Info but not Silo's own API.
		var info embySystemInfo
		if c.do(ctx, s, http.MethodGet, "/System/Info/Public", nil, nil, &info) == nil && info.ID != "" {
			return TestResult{}, userErr(err, "That is Silo's Jellyfin-compatible port. Use the address you open Silo at in your browser (port 8090 unless you changed it).")
		}
	}
	if err != nil {
		if errors.Is(err, errUnauthorized) && s.Token != "" {
			return TestResult{}, userErr(err, "Silo refused the API key. It must be a key of an administrator (Admin > API keys), copied completely.")
		}
		return TestResult{}, explain(s, err)
	}
	libs, err := c.siloLibraries(ctx, s)
	if err != nil {
		return TestResult{}, err
	}
	res := TestResult{ServerName: "Silo", Libraries: []Library{}}
	switch {
	case build.BuildNumber > 0:
		res.Version = "build " + strconv.Itoa(build.BuildNumber)
	case build.Display != "":
		res.Version = build.Display
	}
	for _, l := range libs {
		locs := l.Paths
		if locs == nil {
			locs = []string{}
		}
		res.Libraries = append(res.Libraries, Library{ID: strconv.Itoa(l.ID), Title: l.Name, Type: l.Type, Locations: locs})
	}
	return res, nil
}

func (c *Client) siloScanLibrary(ctx context.Context, s Server, id int) error {
	return explain(s, c.do(ctx, s, http.MethodPost, "/api/v1/scan", nil, map[string]any{"library_id": id}, nil))
}

func (c *Client) siloRefreshAll(ctx context.Context, s Server) error {
	libs, err := c.siloLibraries(ctx, s)
	if err != nil {
		return err
	}
	for _, l := range libs {
		if !l.Enabled {
			continue
		}
		if err := c.siloScanLibrary(ctx, s, l.ID); err != nil {
			return err
		}
	}
	return nil
}

// siloRefreshFolders asks Silo to scan just the folders that changed. A movie
// or show folder outside every library gets a scan of all of them instead,
// as with Jellyfin and Emby: Silo would refuse the folder and the new title
// would never show up. Book folders outside every library are left alone.
func (c *Client) siloRefreshFolders(ctx context.Context, s Server, kind MediaKind, folders []string) ([]string, error) {
	libs, err := c.siloLibraries(ctx, s)
	if err != nil {
		return nil, err
	}
	var locations []string
	for _, l := range libs {
		if l.Enabled {
			locations = append(locations, l.Paths...)
		}
	}
	var done []string
	seen := map[string]bool{}
	outside := ""
	for _, f := range folders {
		p := MapPath(f, s.PathMap)
		if seen[p] {
			continue
		}
		seen[p] = true
		if !insideAny(p, locations) {
			if outside == "" {
				outside = p
			}
			continue
		}
		if err := c.do(ctx, s, http.MethodPost, "/api/v1/scan", nil, map[string]any{"path": p}, nil); err != nil {
			return done, explain(s, err)
		}
		done = append(done, p)
	}
	if outside != "" && kind != MediaBook {
		slog.Warn("media server: a new folder is not inside any of the server's libraries, so the whole library is scanned. Add a folder mapping for it in the media server settings.", "server", s.Name, "folder", outside)
		if err := c.siloRefreshAll(ctx, s); err != nil {
			return done, err
		}
		done = append(done, "whole library (a new folder is not inside any of this server's libraries)")
	}
	return done, nil
}

// siloIDs are the ids Silo may file a title under, best first. Silo names a
// movie after its TMDB id and a show after its TheTVDB id, falling back to
// TMDB and then IMDb ids.
func (c *Client) siloIDs(kind MediaKind, tmdbID int) []string {
	switch kind {
	case MediaMovie:
		return []string{"movie-tmdb-" + strconv.Itoa(tmdbID)}
	case MediaTV:
		var ids []string
		tvdb, imdb := 0, ""
		if c.SeriesIDs != nil {
			tvdb, imdb = c.SeriesIDs(tmdbID)
		}
		if tvdb > 0 {
			ids = append(ids, "series-tvdb-"+strconv.Itoa(tvdb))
		}
		ids = append(ids, "series-tmdb-"+strconv.Itoa(tmdbID))
		if imdb = strings.TrimSpace(imdb); imdb != "" {
			ids = append(ids, "series-imdb-"+imdb)
		}
		return ids
	}
	return nil
}

func (c *Client) siloFind(ctx context.Context, s Server, kind MediaKind, tmdbID int) (Item, bool, error) {
	for _, id := range c.siloIDs(kind, tmdbID) {
		var item struct {
			ContentID string `json:"content_id"`
		}
		err := c.do(ctx, s, http.MethodGet, "/api/v1/catalog/items/"+url.PathEscape(id), nil, nil, &item)
		if errors.Is(err, errNotFound) {
			continue
		}
		if err != nil {
			return Item{}, false, explain(s, err)
		}
		if item.ContentID == "" {
			item.ContentID = id
		}
		return Item{ID: item.ContentID, URL: s.WebURL() + "/item/" + url.PathEscape(item.ContentID)}, true, nil
	}
	return Item{}, false, nil
}
