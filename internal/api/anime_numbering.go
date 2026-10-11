package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/animemap"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/parser"
	"github.com/rdborg/mediarium/internal/settings"
)

// Scene and anime numbering. Release names don't always count a show's
// episodes the way TMDB does: scene names follow TheXEM's numbering, anime
// names count from the first episode, or from the first of a season's second
// part. animemap reads the services that say how (TheXEM and Fribb's anime
// lists, with names from AniList); this file keeps the result per show, maps
// release names with it, adds searches for the other names and shows it on the
// show's page.

const (
	// numberingRefreshEvery is how often a show's numbering is read again
	// without being asked.
	numberingRefreshEvery = 7 * 24 * time.Hour
	// numberingRetryAfter is the pause after a lookup that couldn't reach a service.
	numberingRetryAfter = 24 * time.Hour
	// numberingCacheFor is how long a show's numbering is kept in memory.
	numberingCacheFor = 10 * time.Minute
	// maxNumberingQueries is the most extra searches the numbering adds for one episode.
	maxNumberingQueries = 4
)

type numberingCached struct {
	num *animemap.Numbering // nil when the show has none
	at  time.Time
}

// animeState is what the server keeps for numbering. The zero value is ready.
type animeState struct {
	once sync.Once
	svc  *animemap.Service

	mu       sync.Mutex
	cache    map[int64]numberingCached
	inflight map[int64]bool
}

// animeService is the service that reads TheXEM, the anime lists and AniList.
func (s *Server) animeService() *animemap.Service {
	s.anime.once.Do(func() {
		if s.anime.svc != nil {
			return
		}
		ua := "Mediarium/" + s.version + " (+https://mediarium.app)"
		svc := animemap.NewService(s.db, ua, filepath.Join(s.cfg.ConfigDir, "anime-cache"))
		svc.Norm = normalizeTitle
		s.anime.svc = svc
	})
	return s.anime.svc
}

// numberingLookupOn reports whether Mediarium looks up numbering for anime on
// its own. It is on unless switched off; the "Refresh mapping" button on a
// show's page always works.
func (s *Server) numberingLookupOn() bool { return s.settingOn(settings.KeyNumberingLookup, true) }

func numberingKey(series library.Series) int64 {
	if series.CopyOf > 0 {
		return series.CopyOf
	}
	return series.ID
}

// numberingFor is the numbering of a show, nil when it has none. It is read
// from the database and kept for a few minutes.
func (s *Server) numberingFor(series library.Series) *animemap.Numbering {
	key := numberingKey(series)
	s.anime.mu.Lock()
	if c, ok := s.anime.cache[key]; ok && time.Since(c.at) < numberingCacheFor {
		s.anime.mu.Unlock()
		return c.num
	}
	s.anime.mu.Unlock()

	var num *animemap.Numbering
	rec, ok, err := s.animeService().Repo.Get(key)
	if err != nil {
		log.Printf("numbering: %v", err)
	} else if ok && !rec.Numbering.Empty() {
		n := rec.Numbering
		num = &n
	}
	s.anime.mu.Lock()
	if s.anime.cache == nil {
		s.anime.cache = map[int64]numberingCached{}
	}
	s.anime.cache[key] = numberingCached{num: num, at: time.Now()}
	s.anime.mu.Unlock()
	return num
}

func (s *Server) forgetNumbering(key int64) {
	s.anime.mu.Lock()
	delete(s.anime.cache, key)
	s.anime.mu.Unlock()
}

func animeShow(series library.Series) animemap.Show {
	return animemap.Show{ID: numberingKey(series), Title: series.Title, TMDBID: series.TMDBID, TVDBID: series.TVDBID}
}

// refreshNumbering reads a show's numbering now.
func (s *Server) refreshNumbering(ctx context.Context, series library.Series, manual bool) (animemap.Record, error) {
	rec, err := s.animeService().Refresh(ctx, animeShow(series), animemap.Options{Manual: manual})
	s.forgetNumbering(numberingKey(series))
	return rec, err
}

// noteSeriesNumbering reads the numbering of an anime in the background when
// it has none yet or is due. Shows that aren't anime are left alone unless
// someone presses the button on their page.
func (s *Server) noteSeriesNumbering(series library.Series) {
	if series.SeriesType != library.SeriesAnime || series.CopyOf > 0 || !s.numberingLookupOn() || series.TMDBID <= 0 {
		return
	}
	key := numberingKey(series)
	rec, ok, err := s.animeService().Repo.Get(key)
	if err != nil {
		log.Printf("numbering: %v", err)
		return
	}
	if ok {
		wait := numberingRefreshEvery
		if rec.XEMState == animemap.StateOffline || rec.ListsState == animemap.StateOffline {
			wait = numberingRetryAfter
		}
		if time.Since(rec.CheckedAt) < wait {
			return
		}
	}
	s.anime.mu.Lock()
	if s.anime.inflight == nil {
		s.anime.inflight = map[int64]bool{}
	}
	if s.anime.inflight[key] {
		s.anime.mu.Unlock()
		return
	}
	s.anime.inflight[key] = true
	s.anime.mu.Unlock()
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer func() {
			s.anime.mu.Lock()
			delete(s.anime.inflight, key)
			s.anime.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if _, err := s.refreshNumbering(ctx, series, false); err != nil {
			log.Printf("numbering: %q: %v", series.Title, err)
		}
	}()
}

// ---- mapping release names

func existingEpisodes(episodes []library.Episode) map[[2]int]bool {
	out := make(map[[2]int]bool, len(episodes))
	for _, e := range episodes {
		out[[2]int{e.Season, e.Episode}] = true
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mapNumbered gives a release the season and episodes it is for under the
// show's own numbering, using what is known about the show's other numberings:
//
//   - A name with a season and episodes is read as scene numbering where
//     TheXEM has those episodes (all of them, or the name is left as it is).
//     A season pack is mapped when its whole scene season is one season of the
//     show.
//   - A name that ends its title with a number ("Show Season 3 Part 2 - 05")
//     is first looked up as an episode of the AniDB entry its title names (the
//     fifth episode of that entry). That comes before anything else, since
//     the parser reads "Season 3" in such a name as a season pack. A name with
//     only a number ("Show - 14") is then looked up by the scene absolute
//     number or the show's own, where that names one episode.
//
// The second return value says whether anything was mapped. episodes is the
// show's episode list (only read for names with a bare number).
func mapNumbered(rel parser.Release, num *animemap.Numbering, episodes []library.Episode) (parser.Release, bool) {
	if num.Empty() || rel.Special {
		return rel, false
	}
	if mapped, ok := mapEntryByName(rel, num, episodes); ok {
		return mapped, true
	}
	switch {
	case rel.Season > 0 && len(rel.Episodes) > 0:
		season := 0
		eps := make([]int, 0, len(rel.Episodes))
		for _, e := range rel.Episodes {
			s, ep, ok := num.MapScene(rel.Season, e)
			if !ok || (season != 0 && s != season) {
				return rel, false
			}
			season = s
			eps = append(eps, ep)
		}
		if season == rel.Season && equalInts(eps, rel.Episodes) {
			return rel, false
		}
		rel.Season, rel.Episodes, rel.Episode = season, eps, eps[0]
		return rel, true
	case rel.Season > 0:
		s, ok := num.MapSceneSeason(rel.Season)
		if !ok || s == rel.Season {
			return rel, false
		}
		rel.Season = s
		return rel, true
	case len(rel.Absolute) > 0:
		return mapBareNumbers(rel, num, episodes)
	}
	return rel, false
}

// entryNumberRe reads an anime name that ends its title with an episode
// number: "[Group] Title - 05 (1080p)", "Title - 01-04", "Title - 05v2".
var entryNumberRe = regexp.MustCompile(`^\s*(?:\[[^\]]*\]\s*)*(.+?)\s+-\s+(\d{1,4})(?:v\d)?(?:\s*-\s*(\d{1,4})(?:v\d)?)?\s*(?:[(\[]|$)`)

// mapEntryByName maps a name whose title is one AniDB entry's name and whose
// number is that entry's episode number. The episode must exist in the show:
// a number past the end of the season isn't made up into one.
func mapEntryByName(rel parser.Release, num *animemap.Numbering, episodes []library.Episode) (parser.Release, bool) {
	if len(num.Entries) == 0 {
		return rel, false
	}
	name := strings.NewReplacer(".", " ", "_", " ").Replace(rel.Name)
	m := entryNumberRe.FindStringSubmatch(name)
	if m == nil {
		return rel, false
	}
	first, _ := strconv.Atoi(m[2])
	last := first
	if m[3] != "" {
		if n, _ := strconv.Atoi(m[3]); n >= first && n-first < 200 {
			last = n
		}
	}
	if first <= 0 {
		return rel, false
	}
	have := existingEpisodes(episodes)
	for _, en := range num.EntriesNamed(normalizeTitle, m[1]) {
		if en.Season <= 0 {
			continue
		}
		var eps []int
		for n := first; n <= last; n++ {
			if !have[[2]int{en.Season, n + en.Offset}] {
				eps = nil
				break
			}
			eps = append(eps, n+en.Offset)
		}
		if len(eps) > 0 {
			rel.Season, rel.Episodes, rel.Episode, rel.Absolute = en.Season, eps, eps[0], nil
			return rel, true
		}
	}
	return rel, false
}

func mapBareNumbers(rel parser.Release, num *animemap.Numbering, episodes []library.Episode) (parser.Release, bool) {
	have := existingEpisodes(episodes)
	// A scene absolute number, or the show's own.
	season := 0
	var eps []int
	for _, n := range rel.Absolute {
		s, e, ok := num.MapAbsolute(n)
		if !ok || !have[[2]int{s, e}] || (season != 0 && s != season) {
			break // a batch running into the next season: its first season only
		}
		season = s
		eps = append(eps, e)
	}
	if season > 0 && len(eps) > 0 {
		rel.Season, rel.Episodes, rel.Episode = season, eps, eps[0]
		return rel, true
	}
	return rel, false
}

// ---- searching under the other numberings and names

// numberingQueries are the extra searches for one episode that its other
// numbering and names call for: the scene numbering ("Show S01E21"), the
// show's own absolute number under the first alias, and the entry's name with
// the episode's number within it ("Show Season 3 Part 2 05").
func numberingQueries(title string, num *animemap.Numbering, season, episode, absolute int) []string {
	if num.Empty() || season <= 0 || episode <= 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(q string) {
		if k := strings.ToLower(strings.TrimSpace(q)); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, q)
		}
	}
	if r, ok := num.SceneOf(season, episode); ok {
		if r.SceneSeason != season || r.SceneEpisode != episode {
			add(tvSearchQuery(title, r.SceneSeason, r.SceneEpisode))
		}
		if r.SceneAbsolute > 0 && r.SceneAbsolute != absolute {
			if s, e, ok := num.MapAbsolute(r.SceneAbsolute); ok && s == season && e == episode {
				add(fmt.Sprintf("%s %02d", title, r.SceneAbsolute))
			}
		}
		if r.Absolute > 0 && r.Absolute != absolute {
			add(fmt.Sprintf("%s %02d", title, r.Absolute))
		}
	}
	if en, n, ok := num.EntryFor(season, episode); ok {
		names := 0
		for _, t := range en.Titles {
			if names == 2 {
				break
			}
			if normalizeTitle(t) == normalizeTitle(title) && en.Offset == 0 {
				continue // the same search the show's own title already makes
			}
			add(fmt.Sprintf("%s %02d", t, n))
			names++
		}
	}
	if p, ok := num.Primary(); ok && len(p.Titles) > 0 && absolute > 0 {
		add(fmt.Sprintf("%s %02d", p.Titles[0], absolute))
	}
	if len(out) > maxNumberingQueries {
		out = out[:maxNumberingQueries]
	}
	return out
}

// ---- the show's page

type numberingEntryPayload struct {
	AniDB   int    `json:"anidb"`
	AniList int    `json:"anilist,omitempty"`
	MAL     int    `json:"mal,omitempty"`
	Type    string `json:"type,omitempty"`
	Season  int    `json:"season"`
	Offset  int    `json:"offset,omitempty"`
	Name    string `json:"name,omitempty"`
}

type numberingPayload struct {
	// Lookup is whether Mediarium reads anime numbering on its own (Settings).
	Lookup     bool   `json:"lookup"`
	AniList    int    `json:"anilist,omitempty"`
	AniDB      int    `json:"anidb,omitempty"`
	MAL        int    `json:"mal,omitempty"`
	AniListURL string `json:"anilistUrl,omitempty"`
	AniDBURL   string `json:"anidbUrl,omitempty"`
	MALURL     string `json:"malUrl,omitempty"`
	// Scene says what TheXEM has: State is "mapped", "none", "offline" or empty
	// (never looked up). Rows is how many episodes it lists, Differing how many
	// of them are numbered differently from the show's own.
	SceneState   string                  `json:"sceneState"`
	SceneRows    int                     `json:"sceneRows"`
	SceneDiffers int                     `json:"sceneDiffers"`
	ListsState   string                  `json:"listsState"`
	Entries      []numberingEntryPayload `json:"entries"`
	Names        []string                `json:"names"`
	Note         string                  `json:"note,omitempty"`
	CheckedAt    string                  `json:"checkedAt,omitempty"`
	Refreshing   bool                    `json:"refreshing,omitempty"`
}

func (s *Server) numberingPayload(series library.Series, rec animemap.Record, have bool) numberingPayload {
	p := numberingPayload{Lookup: s.numberingLookupOn(), Entries: []numberingEntryPayload{}, Names: []string{}}
	s.anime.mu.Lock()
	p.Refreshing = s.anime.inflight[numberingKey(series)]
	s.anime.mu.Unlock()
	if !have {
		return p
	}
	p.AniList, p.AniDB, p.MAL = rec.AniList, rec.AniDB, rec.MAL
	if p.AniList > 0 {
		p.AniListURL = "https://anilist.co/anime/" + strconv.Itoa(p.AniList)
	}
	if p.AniDB > 0 {
		p.AniDBURL = "https://anidb.net/anime/" + strconv.Itoa(p.AniDB)
	}
	if p.MAL > 0 {
		p.MALURL = "https://myanimelist.net/anime/" + strconv.Itoa(p.MAL)
	}
	p.SceneState, p.ListsState, p.Note = rec.XEMState, rec.ListsState, rec.Note
	p.SceneRows = len(rec.Numbering.Scene)
	for _, r := range rec.Numbering.Scene {
		if r.SceneSeason != r.Season || r.SceneEpisode != r.Episode || (r.SceneAbsolute != 0 && r.Absolute != 0 && r.SceneAbsolute != r.Absolute) {
			p.SceneDiffers++
		}
	}
	for _, e := range rec.Numbering.Entries {
		name := ""
		if len(e.Titles) > 0 {
			name = e.Titles[0]
		}
		p.Entries = append(p.Entries, numberingEntryPayload{AniDB: e.AniDB, AniList: e.AniList, MAL: e.MAL, Type: e.Type, Season: e.Season, Offset: e.Offset, Name: name})
	}
	p.Names = append(p.Names, rec.Aliases...)
	if len(p.Names) > 12 {
		p.Names = p.Names[:12]
	}
	if !rec.CheckedAt.IsZero() {
		p.CheckedAt = rec.CheckedAt.UTC().Format(time.RFC3339)
	}
	return p
}

func (s *Server) seriesForNumbering(w http.ResponseWriter, r *http.Request) (library.Series, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid show ID.")
		return library.Series{}, false
	}
	series, err := s.MovieRepo.GetSeries(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "That show isn't in your library.")
		return library.Series{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return library.Series{}, false
	}
	return series, true
}

// handleSeriesNumbering answers GET /api/series/{id}/numbering: the ids and
// links, and what the numbering services say about the show.
func (s *Server) handleSeriesNumbering(w http.ResponseWriter, r *http.Request) {
	series, ok := s.seriesForNumbering(w, r)
	if !ok {
		return
	}
	rec, have, err := s.animeService().Repo.Get(numberingKey(series))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.numberingPayload(series, rec, have))
}

// handleRefreshSeriesNumbering answers POST /api/series/{id}/numbering/refresh:
// "Refresh mapping". It asks the services now, for any show, whether or not
// automatic lookups are on.
func (s *Server) handleRefreshSeriesNumbering(w http.ResponseWriter, r *http.Request) {
	series, ok := s.seriesForNumbering(w, r)
	if !ok {
		return
	}
	if series.TMDBID <= 0 {
		writeError(w, http.StatusConflict, "This show has no TMDB id, so there is nothing to look up.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	rec, err := s.refreshNumbering(ctx, series, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.numberingPayload(series, rec, true))
}
