package animemap

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

// titlesFreshFor is how long the names read from AniList are reused: they
// hardly ever change.
const titlesFreshFor = 30 * 24 * time.Hour

// Show is what the services are asked about.
type Show struct {
	ID     int64
	Title  string
	TMDBID int
	TVDBID int
}

// Options change what a refresh asks.
type Options struct {
	// Manual is a person pressing "Refresh mapping": TheXEM is asked about the
	// show even if its list of mapped shows doesn't name it, GitHub is asked
	// whether the anime list changed, and the names are read again.
	Manual bool
}

// Service reads the services and keeps the result. One refresh runs at a
// time, which keeps a batch of shows from asking in parallel.
type Service struct {
	Repo    *Repo
	XEM     *XEM
	Lists   *Lists
	AniList *AniList
	// Norm reduces a title for comparison (the app's own rule).
	Norm func(string) string

	mu sync.Mutex
}

// NewService builds a service with clients that identify themselves with
// userAgent and keep their downloads in cacheDir.
func NewService(db *sql.DB, userAgent, cacheDir string) *Service {
	return &Service{
		Repo: NewRepo(db), XEM: NewXEM(userAgent, cacheDir), Lists: NewLists(userAgent, cacheDir), AniList: NewAniList(userAgent),
		Norm: defaultNorm,
	}
}

func defaultNorm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Refresh asks the services about a show and saves what they say. A service
// that can't be reached leaves what was known as it was and says so in the
// record's note; only a database problem is an error.
func (s *Service) Refresh(ctx context.Context, show Show, opt Options) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, _, err := s.Repo.Get(show.ID)
	if err != nil {
		return Record{}, err
	}
	rec := Record{SeriesID: show.ID, Numbering: prev.Numbering, XEMState: prev.XEMState, ListsState: prev.ListsState, TitlesAt: prev.TitlesAt}
	var notes []string

	// TheXEM.
	rows, state, note := s.scene(ctx, show, opt, prev)
	if state != "" {
		rec.XEMState = state
		if state != StateOffline {
			rec.Numbering.Scene = rows
		}
	}
	if note != "" {
		notes = append(notes, note)
	}

	// The anime lists, then the names of their entries.
	entries, state, note := s.entries(ctx, show, opt, prev)
	if state != "" {
		rec.ListsState = state
		if state != StateOffline {
			rec.Numbering.Entries = entries
		}
	}
	if note != "" {
		notes = append(notes, note)
	}
	if len(rec.Numbering.Entries) > 0 && state != StateOffline {
		var titleNote string
		rec.TitlesAt, titleNote = s.fillTitles(ctx, rec.Numbering.Entries, prev, opt)
		if titleNote != "" {
			notes = append(notes, titleNote)
		}
	}

	if p, ok := rec.Numbering.Primary(); ok {
		rec.AniDB, rec.AniList, rec.MAL = p.AniDB, p.AniList, p.MAL
	}
	rec.Aliases = rec.Numbering.Aliases(s.norm, show.Title)
	rec.Note = strings.Join(notes, " ")
	rec.CheckedAt = time.Now()
	if err := s.Repo.Save(rec); err != nil {
		return rec, err
	}
	return rec, nil
}

func (s *Service) norm(t string) string {
	if s.Norm != nil {
		return s.Norm(t)
	}
	return defaultNorm(t)
}

// scene asks TheXEM. state is "" when nothing was asked (no TheTVDB id is
// kept as "none").
func (s *Service) scene(ctx context.Context, show Show, opt Options, prev Record) (rows []SceneEpisode, state, note string) {
	if show.TVDBID <= 0 {
		return nil, StateNone, ""
	}
	if !opt.Manual {
		have, err := s.XEM.HaveMap(ctx, show.TVDBID)
		switch {
		case err != nil:
			return nil, StateOffline, offlineNote("TheXEM", prev.XEMState == StateMapped)
		case !have:
			return nil, StateNone, ""
		}
	}
	rows, found, err := s.XEM.All(ctx, show.TVDBID)
	switch {
	case err != nil:
		return nil, StateOffline, offlineNote("TheXEM", prev.XEMState == StateMapped)
	case !found:
		return nil, StateNone, ""
	}
	return rows, StateMapped, ""
}

func offlineNote(service string, keeps bool) string {
	if keeps {
		return service + " couldn't be reached, so the numbering it gave before is kept."
	}
	return service + " couldn't be reached. It will be tried again."
}

// entries asks the anime lists.
func (s *Service) entries(ctx context.Context, show Show, opt Options, prev Record) ([]Entry, string, string) {
	list, err := s.Lists.Lookup(ctx, show.TMDBID, show.TVDBID, opt.Manual)
	if err != nil {
		if errors.Is(err, ErrOffline) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, StateOffline, offlineNote("The anime lists", prev.ListsState == StateFound)
		}
		return nil, StateOffline, "The anime lists couldn't be read: " + err.Error() + "."
	}
	if len(list) == 0 {
		return nil, StateNone, ""
	}
	return list, StateFound, ""
}

// fillTitles puts the names of each entry in, reusing the ones read before
// when they are recent and the entry is the same. It returns when the names
// were read and a note when AniList could not be reached.
func (s *Service) fillTitles(ctx context.Context, entries []Entry, prev Record, opt Options) (time.Time, string) {
	old := map[int][]string{}
	known := map[int]bool{} // entries seen before, with names or without (AniList may not know them)
	for _, e := range prev.Numbering.Entries {
		known[e.AniList] = true
		if len(e.Titles) > 0 {
			old[e.AniList] = e.Titles
		}
	}
	reuse := !opt.Manual && !prev.TitlesAt.IsZero() && time.Since(prev.TitlesAt) < titlesFreshFor
	var need []int
	for i := range entries {
		id := entries[i].AniList
		if id <= 0 {
			continue
		}
		if reuse && known[id] {
			entries[i].Titles = old[id]
			continue
		}
		need = append(need, id)
	}
	if len(need) == 0 {
		return prev.TitlesAt, ""
	}
	got, err := s.AniList.Titles(ctx, need)
	if err != nil && len(got) == 0 {
		for i := range entries { // the older names are better than none
			if t, ok := old[entries[i].AniList]; ok && entries[i].Titles == nil {
				entries[i].Titles = t
			}
		}
		return prev.TitlesAt, "AniList couldn't be reached, so the other names will be added next time."
	}
	for i := range entries {
		if t, ok := got[entries[i].AniList]; ok {
			entries[i].Titles = t
		} else if t, ok := old[entries[i].AniList]; ok && entries[i].Titles == nil {
			entries[i].Titles = t
		}
	}
	return time.Now(), ""
}
