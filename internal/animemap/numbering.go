// Package animemap works out how a show's episodes are numbered in release
// names when that differs from the numbering Mediarium keeps.
//
// Two community services say how:
//
//   - TheXEM (thexem.info) lists, for shows that have scene numbering, which
//     episode of the show each scene season and episode (and scene absolute
//     number) is. Sonarr uses the same data.
//   - Fribb's anime-lists (github.com/Fribb/anime-lists, built from the
//     Anime-Lists project and the anime-offline-database) pairs every AniDB
//     entry with its AniList and MyAnimeList ids and with the TMDB season and
//     episode offset it sits at. A show that TMDB keeps as one season can be
//     two AniDB entries ("Season 3" and "Season 3 Part 2"), each counted from
//     episode 1 in release names.
//
// AniList (anilist.co) then gives each entry's titles, which are the names
// releases use.
//
// Nothing here talks to the database or the rest of the app: the package
// holds the data model, the clients and the matching rules.
package animemap

import (
	"strings"
)

// SceneEpisode is one episode as TheXEM lists it: how releases name it (scene)
// and where it sits in the show's own numbering (the TheTVDB order, which is
// the order TMDB uses for nearly every show).
type SceneEpisode struct {
	SceneSeason   int `json:"ss"`
	SceneEpisode  int `json:"se"`
	SceneAbsolute int `json:"sa,omitempty"`
	Season        int `json:"s"`
	Episode       int `json:"e"`
	Absolute      int `json:"a,omitempty"`
}

// Entry is one AniDB entry of the show: a season, a part of a season, or a
// special. Releases count its episodes from 1.
type Entry struct {
	AniDB   int    `json:"anidb"`
	AniList int    `json:"anilist,omitempty"`
	MAL     int    `json:"mal,omitempty"`
	Type    string `json:"type,omitempty"` // TV, ONA, OVA, SPECIAL
	// Season is the season of the show the entry's episodes are in (0 for
	// specials) and Offset what is added to an entry episode number to get the
	// episode number in that season.
	Season int `json:"season"`
	Offset int `json:"offset,omitempty"`
	// Titles are the names the entry is known by (romaji, English, synonyms).
	Titles []string `json:"titles,omitempty"`
}

// Numbering is everything known about how one show is numbered.
type Numbering struct {
	Scene   []SceneEpisode `json:"scene,omitempty"`
	Entries []Entry        `json:"entries,omitempty"`
}

// Empty reports whether there is nothing to map with.
func (n *Numbering) Empty() bool { return n == nil || (len(n.Scene) == 0 && len(n.Entries) == 0) }

// MapScene gives the episode a scene season and episode stands for. ok is false
// when the scene numbering has no such episode or names two different ones.
func (n *Numbering) MapScene(season, episode int) (s, e int, ok bool) {
	if n == nil {
		return 0, 0, false
	}
	found := false
	for _, r := range n.Scene {
		if r.SceneSeason != season || r.SceneEpisode != episode {
			continue
		}
		if found && (s != r.Season || e != r.Episode) {
			return 0, 0, false
		}
		s, e, found = r.Season, r.Episode, true
	}
	return s, e, found
}

// MapSceneSeason gives the season a whole scene season (a season pack) stands
// for. ok is false unless every episode of that scene season is in one season
// of the show, so a scene season that spreads over two is left as it is.
func (n *Numbering) MapSceneSeason(season int) (int, bool) {
	if n == nil {
		return 0, false
	}
	dest, found := 0, false
	for _, r := range n.Scene {
		if r.SceneSeason != season {
			continue
		}
		if found && dest != r.Season {
			return 0, false
		}
		dest, found = r.Season, true
	}
	return dest, found
}

// MapAbsolute gives the episode a bare number ("Show - 105") stands for: the
// one episode with that scene absolute number, or failing that the one with
// that absolute number in the show's own numbering. ok is false when neither
// names exactly one episode (scene absolute numbers that start over every
// season name several).
func (n *Numbering) MapAbsolute(abs int) (s, e int, ok bool) {
	if n == nil || abs <= 0 {
		return 0, 0, false
	}
	if s, e, ok = n.uniqueAbsolute(abs, func(r SceneEpisode) int { return r.SceneAbsolute }); ok {
		return s, e, true
	}
	return n.uniqueAbsolute(abs, func(r SceneEpisode) int { return r.Absolute })
}

func (n *Numbering) uniqueAbsolute(abs int, of func(SceneEpisode) int) (s, e int, ok bool) {
	count := 0
	for _, r := range n.Scene {
		if of(r) != abs {
			continue
		}
		count++
		s, e = r.Season, r.Episode
	}
	return s, e, count == 1
}

// SceneOf is the scene numbering of an episode of the show, to search for it
// by the name releases give it. ok is false when TheXEM has no row for it.
func (n *Numbering) SceneOf(season, episode int) (SceneEpisode, bool) {
	if n == nil {
		return SceneEpisode{}, false
	}
	for _, r := range n.Scene {
		if r.Season == season && r.Episode == episode {
			return r, true
		}
	}
	return SceneEpisode{}, false
}

// EntryFor is the entry an episode of the show sits in, and its number within
// that entry: the entry for that season with the biggest offset that is still
// before the episode. ok is false when no entry covers it.
func (n *Numbering) EntryFor(season, episode int) (Entry, int, bool) {
	if n == nil || season <= 0 {
		return Entry{}, 0, false
	}
	var best Entry
	found := false
	for _, en := range n.Entries {
		if en.Season != season || !regularEntry(en) || episode-en.Offset < 1 {
			continue
		}
		if !found || en.Offset > best.Offset {
			best, found = en, true
		}
	}
	if !found {
		return Entry{}, 0, false
	}
	return best, episode - best.Offset, true
}

// regularEntry is an entry whose episodes are the show's regular ones.
func regularEntry(e Entry) bool { return e.Type == "TV" || e.Type == "ONA" || e.Type == "" }

// EntriesNamed lists the entries a release title names, by comparing titles
// with norm (the same reduction the app applies to every title). A title that
// two entries share does not name either of them: it can't say which part of
// the show the release is for. Titles that reduce to nothing or to a couple of
// letters are ignored.
func (n *Numbering) EntriesNamed(norm func(string) string, title string) []Entry {
	if n == nil {
		return nil
	}
	want := norm(title)
	if len(want) < 3 {
		return nil
	}
	owners := map[string]map[int]bool{}
	for _, en := range n.Entries {
		for _, t := range en.Titles {
			k := norm(t)
			if len(k) < 3 {
				continue
			}
			if owners[k] == nil {
				owners[k] = map[int]bool{}
			}
			owners[k][en.AniDB] = true
		}
	}
	ids := owners[want]
	if len(ids) != 1 {
		return nil
	}
	var out []Entry
	for _, en := range n.Entries {
		if ids[en.AniDB] {
			out = append(out, en)
		}
	}
	return out
}

// Aliases lists the other names of the show: every title of every entry, once
// each, those of the regular seasons first. norm decides what counts as the
// same name; the names in skip (the show's own title) are left out.
func (n *Numbering) Aliases(norm func(string) string, skip ...string) []string {
	if n == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, s := range skip {
		seen[norm(s)] = true
	}
	var out []string
	for _, regular := range []bool{true, false} {
		for _, en := range n.Entries {
			if (en.Season > 0 && regularEntry(en)) != regular {
				continue
			}
			for _, t := range en.Titles {
				t = strings.TrimSpace(t)
				k := norm(t)
				if len(k) < 3 || seen[k] {
					continue
				}
				seen[k] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// Primary is the entry the show page links to: the first regular entry of the
// first season that has one, else the first entry there is.
func (n *Numbering) Primary() (Entry, bool) {
	if n == nil || len(n.Entries) == 0 {
		return Entry{}, false
	}
	var best Entry
	found := false
	for _, en := range n.Entries {
		if !regularEntry(en) || en.Season <= 0 {
			continue
		}
		if !found || en.Season < best.Season || (en.Season == best.Season && en.Offset < best.Offset) {
			best, found = en, true
		}
	}
	if found {
		return best, true
	}
	return n.Entries[0], true
}
