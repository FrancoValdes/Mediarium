package animemap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/netguard"
)

const (
	// listsURL is Fribb's anime-lists, the minified merge of the Anime-Lists
	// project and the anime-offline-database (about 6 MB; GitHub sends it
	// compressed).
	listsURL = "https://raw.githubusercontent.com/Fribb/anime-lists/master/anime-list-mini.json"

	// listsFreshFor is how long the saved copy is used without asking GitHub
	// whether it changed. After that the check is a request that carries the
	// copy's ETag and gets a short "not modified" answer when nothing changed.
	listsFreshFor = 24 * time.Hour
	// listsIndexFor is how long the parsed index is kept in memory, so a batch
	// of shows reads the file once.
	listsIndexFor = time.Hour
)

// Lists reads Fribb's anime-lists. The file is saved in the cache folder and
// kept up to date with conditional requests; if GitHub can't be reached the
// saved copy is used, however old.
type Lists struct {
	url       string
	userAgent string
	http      *http.Client
	gate      gate
	cacheDir  string

	mu    sync.Mutex
	idx   *listIndex
	idxAt time.Time
}

// NewLists returns a reader that keeps its copy of the list in cacheDir.
func NewLists(userAgent, cacheDir string) *Lists {
	return &Lists{
		url: listsURL, userAgent: userAgent, http: netguard.Client(2 * time.Minute),
		gate: gate{gap: time.Second}, cacheDir: cacheDir,
	}
}

// SetURL points the reader at a test server.
func (l *Lists) SetURL(u string) {
	l.url = u
	l.gate.gap = 0
}

func (l *Lists) path() string { return filepath.Join(l.cacheDir, "anime-list-mini.json") }

// listEntry is what is kept of one line of the list.
type listEntry struct {
	AniDB, AniList, MAL int
	Type                string
	TMDBTV, TVDB        int
	SeasonTMDB          int // -1 when the list has none
	SeasonTVDB          int // -1 when the list has none
	OffsetTMDB          int // -1 when the list has none
	OffsetTVDB          int // -1 when the list has none
}

type listIndex struct {
	byTMDB map[int][]listEntry
	byTVDB map[int][]listEntry
}

type rawPair struct {
	TVDB *int `json:"tvdb"`
	TMDB *int `json:"tmdb"`
}

type rawListEntry struct {
	Type    string          `json:"type"`
	AniDB   int             `json:"anidb_id"`
	AniList int             `json:"anilist_id"`
	MAL     int             `json:"mal_id"`
	TMDB    json.RawMessage `json:"themoviedb_id"`
	TVDB    int             `json:"tvdb_id"`
	Season  *rawPair        `json:"season"`
	Offset  *rawPair        `json:"episode_offset"`
}

func (r rawListEntry) toEntry() (listEntry, bool) {
	e := listEntry{AniDB: r.AniDB, AniList: r.AniList, MAL: r.MAL, Type: r.Type, TVDB: r.TVDB, SeasonTMDB: -1, SeasonTVDB: -1, OffsetTMDB: -1, OffsetTVDB: -1}
	if len(r.TMDB) > 0 {
		var t struct {
			TV int `json:"tv"`
		}
		if json.Unmarshal(r.TMDB, &t) == nil {
			e.TMDBTV = t.TV
		}
	}
	if r.Season != nil {
		if r.Season.TMDB != nil {
			e.SeasonTMDB = *r.Season.TMDB
		}
		if r.Season.TVDB != nil {
			e.SeasonTVDB = *r.Season.TVDB
		}
	}
	if r.Offset != nil {
		if r.Offset.TMDB != nil {
			e.OffsetTMDB = *r.Offset.TMDB
		}
		if r.Offset.TVDB != nil {
			e.OffsetTVDB = *r.Offset.TVDB
		}
	}
	return e, e.AniDB > 0 && (e.TMDBTV > 0 || e.TVDB > 0)
}

// parseList reads the list's JSON array one entry at a time. It keeps only the
// entries that belong to a series (a movie's entry has no TMDB show id and,
// in this list, a TVDB id of the series it is about, which stays: its season
// is 0).
func parseList(r io.Reader) (*listIndex, error) {
	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("read the anime list: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, errors.New("read the anime list: it isn't a list")
	}
	idx := &listIndex{byTMDB: map[int][]listEntry{}, byTVDB: map[int][]listEntry{}}
	for dec.More() {
		var raw rawListEntry
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("read the anime list: %w", err)
		}
		e, ok := raw.toEntry()
		if !ok {
			continue
		}
		if e.TMDBTV > 0 {
			idx.byTMDB[e.TMDBTV] = append(idx.byTMDB[e.TMDBTV], e)
		}
		if e.TVDB > 0 {
			idx.byTVDB[e.TVDB] = append(idx.byTVDB[e.TVDB], e)
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("read the anime list: %w", err)
	}
	return idx, nil
}

// ensureFile makes sure the saved copy is there and fresh. It returns the path.
// An error means there is no copy at all.
func (l *Lists) ensureFile(ctx context.Context, force bool) (string, error) {
	p := l.path()
	info, statErr := os.Stat(p)
	have := statErr == nil
	if have && !force && time.Since(info.ModTime()) < listsFreshFor {
		return p, nil
	}
	if err := l.download(ctx, p, have); err != nil {
		if have {
			return p, nil // offline: the copy we have is still good
		}
		return "", err
	}
	return p, nil
}

func (l *Lists) download(ctx context.Context, p string, have bool) error {
	if err := l.gate.wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.url, nil)
	if err != nil {
		return netguard.CleanError(err)
	}
	req.Header.Set("User-Agent", l.userAgent)
	if etag, err := os.ReadFile(p + ".etag"); err == nil && have {
		req.Header.Set("If-None-Match", strings.TrimSpace(string(etag)))
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: the anime list: %v", ErrOffline, netguard.CleanError(err))
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		now := time.Now()
		_ = os.Chtimes(p, now, now)
		return nil
	case http.StatusOK:
	default:
		return fmt.Errorf("%w: the anime list answered HTTP %d", ErrOffline, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("save the anime list: %w", err)
	}
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("save the anime list: %w", err)
	}
	// 64 MB is far more than the list will ever be; a larger answer is not it.
	n, err := io.Copy(f, io.LimitReader(resp.Body, 64<<20+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil || n > 64<<20 {
		_ = os.Remove(tmp)
		if err == nil {
			err = errors.New("the answer is too big to be the anime list")
		}
		return fmt.Errorf("%w: the anime list: %v", ErrOffline, err)
	}
	// Check it reads before it replaces the good copy.
	if err := checkList(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: the anime list: %v", ErrOffline, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("save the anime list: %w", err)
	}
	if etag := resp.Header.Get("ETag"); etag != "" {
		_ = os.WriteFile(p+".etag", []byte(etag), 0o644)
	} else {
		_ = os.Remove(p + ".etag")
	}
	return nil
}

func checkList(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	if tok, err := dec.Token(); err != nil {
		return err
	} else if d, ok := tok.(json.Delim); !ok || d != '[' {
		return errors.New("it isn't a list")
	}
	if !dec.More() {
		return errors.New("the list is empty")
	}
	return nil
}

func (l *Lists) index(ctx context.Context, force bool) (*listIndex, error) {
	l.mu.Lock()
	idx, at := l.idx, l.idxAt
	l.mu.Unlock()
	if idx != nil && !force && time.Since(at) < listsIndexFor {
		return idx, nil
	}
	p, err := l.ensureFile(ctx, force)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open the anime list: %w", err)
	}
	defer f.Close()
	idx, err = parseList(f)
	if err != nil {
		// A copy that can't be read is thrown away so the next try downloads it again.
		_ = os.Remove(p)
		return nil, err
	}
	l.mu.Lock()
	l.idx, l.idxAt = idx, time.Now()
	l.mu.Unlock()
	return idx, nil
}

// Lookup lists the AniDB entries of a show, found by its TMDB show id and,
// failing that, by its TheTVDB id (an entry that names a different TMDB show
// is not this show's). force asks GitHub whether the list changed even if the
// saved copy is recent.
func (l *Lists) Lookup(ctx context.Context, tmdbID, tvdbID int, force bool) ([]Entry, error) {
	idx, err := l.index(ctx, force)
	if err != nil {
		return nil, err
	}
	var cands []listEntry
	if tmdbID > 0 {
		cands = idx.byTMDB[tmdbID]
	}
	if len(cands) == 0 && tvdbID > 0 {
		for _, e := range idx.byTVDB[tvdbID] {
			if e.TMDBTV == 0 {
				cands = append(cands, e)
			}
		}
	}
	return entriesOf(cands), nil
}

// entriesOf turns list entries into the show's entries. Only a series' own
// episodes are kept (not films). An entry with no TMDB season (TMDB is the
// numbering Mediarium holds) has Season -1: it gives ids and names, and does
// not number episodes.
func entriesOf(cands []listEntry) []Entry {
	seen := map[int]bool{}
	var out []Entry
	for _, c := range cands {
		switch c.Type {
		case "TV", "ONA", "OVA", "SPECIAL":
		default:
			continue
		}
		if seen[c.AniDB] {
			continue
		}
		seen[c.AniDB] = true
		off := 0
		switch {
		case c.SeasonTMDB < 0:
			// No TMDB season (a long show kept as one entry): its ids are
			// kept, but it can't be used to number episodes.
		case c.OffsetTMDB >= 0:
			off = c.OffsetTMDB
		case c.OffsetTVDB >= 0 && c.SeasonTVDB == c.SeasonTMDB:
			off = c.OffsetTVDB
		}
		out = append(out, Entry{AniDB: c.AniDB, AniList: c.AniList, MAL: c.MAL, Type: c.Type, Season: c.SeasonTMDB, Offset: off})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Season != out[j].Season {
			return out[i].Season < out[j].Season
		}
		if out[i].Offset != out[j].Offset {
			return out[i].Offset < out[j].Offset
		}
		return out[i].AniDB < out[j].AniDB
	})
	return out
}
