package api

import (
	"context"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/automation"
	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/parser"
	"github.com/rdborg/mediarium/internal/quality"
	"github.com/rdborg/mediarium/internal/settings"
)

const (
	// startupDelay is how long after the app starts before the automatic
	// loops run for the first time.
	startupDelay = 60 * time.Second
)

// StartAutomation wires the missing/upgrade hunting loop and RSS-style
// latest-releases sync into the generic scheduler from
// internal/automation. Called once from cmd/app/main.go after the server
// is constructed.
func (s *Server) StartAutomation() *automation.Scheduler {
	s.applySpeedLimit(time.Now())
	_, _ = s.QueueRepo.ClearSettledFailures()
	if s.cfg.PauseAutomation {
		log.Print("Safe mode: MEDIARIUM_PAUSE_AUTOMATION is set, so automatic searching, downloading and refreshing are switched off until you remove it.")
		// Two checks still run, as they start nothing by themselves: the
		// download line (only what a person started goes ahead in safe mode,
		// for example once the download hours begin) and the speed schedule.
		sched := automation.NewScheduler(
			automation.Job{Name: "download-line", Interval: downloadLineCheck, Run: func(context.Context) { s.kickDownloads() }},
			automation.Job{Name: "speed-limit", Interval: speedCheck, Run: s.speedJob},
		)
		sched.Start()
		return sched
	}
	sched := automation.NewScheduler(
		automation.Job{Name: "hunt", Interval: s.huntInterval(), IntervalFunc: s.huntInterval, Run: s.heavy(s.hunt)},
		automation.Job{Name: "rss-sync", Interval: s.releaseCheckInterval(), IntervalFunc: s.releaseCheckInterval, Run: s.heavy(s.rssSync)},
		automation.Job{Name: "delayed-grabs", Interval: time.Minute, Run: s.delayedGrabsJob}, // searches again when a held release has waited long enough
		automation.Job{Name: "download-line", Interval: downloadLineCheck, Run: func(context.Context) { s.kickDownloads() }},
		automation.Job{Name: "series-refresh", Interval: seriesRefreshInterval, Run: s.heavy(s.refreshAllSeries)},
		automation.Job{Name: "music-refresh", Interval: musicRefreshInterval, Run: s.heavy(s.refreshMusicArtists)},
		automation.Job{Name: "subtitle-sweep", Interval: subtitleSweepInterval, Run: s.heavy(s.subtitleSweepJob)},
		automation.Job{Name: "genre-backfill", Interval: genreBackfillInterval, Run: s.heavy(s.backfillGenres)},
		automation.Job{Name: "collections", Interval: collectionsInterval, Run: s.heavy(s.collectionsJob)}, // finds the collection of each movie; adds new films of followed collections
		automation.Job{Name: "cleanup", Interval: cleanupCheckInterval, Run: s.cleanupJob},
		automation.Job{Name: "stats-snapshot", Interval: statsSnapshotEvery, Run: s.statsSnapshotJob},
		automation.Job{Name: "backup", Interval: backupCheck, Run: s.backupJob},
		automation.Job{Name: "speed-limit", Interval: speedCheck, Run: s.speedJob},
		automation.Job{Name: "quiet-hours", Interval: speedCheck, Run: s.quietHoursJob},
		automation.Job{Name: "calendar-art", Interval: artworkEvery, Run: s.heavy(s.artworkJob)}, // fetches the backdrops and logos of what is on the calendar
		automation.Job{Name: "weekly-summary", Interval: digestCheck, Run: s.digestJob},          // does nothing until a day is chosen
		automation.Job{Name: "import-details", Interval: importDetailsInterval, Run: s.heavy(s.importDetailsJob)},
		automation.Job{Name: "episode-quality", Interval: 24 * time.Hour, Run: s.heavy(func(context.Context) { s.backfillEpisodeQuality() })},
		automation.Job{Name: "import-added-dates", Interval: 24 * time.Hour, Run: s.heavy(func(context.Context) { s.backfillImportedAddedDates() })},
		automation.Job{Name: "watched", Interval: watchedJobTick, Run: s.heavy(s.watchedJob)},                     // does nothing until switched on
		automation.Job{Name: "plex-watchlist", Interval: watchlistInterval, Run: s.heavy(s.watchlistJob)},         // does nothing until an account connects one
		automation.Job{Name: "import-lists", Interval: listsCheckInterval, Run: s.heavy(s.importListsJob)},        // does nothing until a list is added
		automation.Job{Name: "request-available", Interval: requestAvailableInterval, Run: s.requestAvailableJob}, // does nothing until request messages are switched on
	)
	// The first minute belongs to the pages: the loops start once the app has
	// had time to answer its first requests.
	sched.DelayFirstRuns(startupDelay)
	sched.Start()
	return sched
}

func (s *Server) automationEnabled() bool {
	if s.cfg.PauseAutomation {
		return false
	}
	v, _ := s.Settings.Get(settings.KeyAutomationEnabled)
	return v != "0"
}

// tierKnown reports whether a stored quality is a real tier. Files imported
// from an existing library often carry nothing in their names to classify
// them; with no baseline to compare against, automation leaves them alone
// rather than replacing a library the user already has.
func tierKnown(q string) bool { return q != "" && quality.Tier(q) != quality.TierUnknown }

// outgrown reports whether a file of tier t is below what profile is for and
// not one of its qualities at all: a 1080p file on a 4K profile. Someone put
// the title on that profile to get that quality, so the file is replaced
// whatever the upgrade switch says. A file above the profile (a 4K file on a
// 1080p profile) is never "downgraded".
func outgrown(profile quality.Profile, t quality.Tier) bool {
	return !profile.AllowsTier(t) && !profile.ReachedCutoffTier(t)
}

// wantsUpgrade reports whether a downloaded item with stored quality q
// should be searched for a better release under profile. A file that is
// there only because of a fallback profile is always searched for a
// replacement, even with upgrades off.
// release is the release the file came from ("" when unknown): a file below
// the profile's custom-format cutoff is searched for a better-scoring one.
func wantsUpgrade(profile quality.Profile, q, release string) bool {
	if !tierKnown(q) {
		return false
	}
	if profile.OnFallback(quality.Tier(q)) || outgrown(profile, quality.Tier(q)) {
		return true
	}
	return profile.UpgradeAllowed && (!profile.ReachedCutoffTier(quality.Tier(q)) || profile.WantsFormatUpgrade(release))
}

// hunt actively searches by title for every monitored movie that's either
// missing outright or downloaded-but-below-cutoff
// ("missing/upgrade hunting loop"). Each movie is judged against its own
// quality profile (falling back to the default). Missing movies search
// unconditionally; downloaded movies only search once they haven't already
// reached the profile's cutoff (and only if the profile allows upgrades),
// and only grab a result that's an actual upgrade over what's already on
// disk — never a same-or-lower quality re-grab.
func (s *Server) hunt(ctx context.Context) {
	if !s.automationEnabled() {
		return
	}
	if !s.autoGrabRoom() {
		holdBack("hunt")
		return
	}
	movies, err := s.MovieRepo.ListWithCopies()
	if err != nil {
		log.Printf("automation: hunt: list movies: %v", err)
		return
	}
	instances, err := s.searchableIndexers()
	if err != nil || len(instances) == 0 {
		return
	}
	profiles, err := s.loadProfiles()
	if err != nil {
		log.Printf("automation: hunt: load quality profiles: %v", err)
		return
	}

	blocked := s.blockedKeys()
	if s.moviesEnabled() {
		for _, m := range movies {
			if s.huntMovie(ctx, m, instances, profiles, blocked, false) && !s.autoGrabRoom() {
				holdBack("hunt")
				return
			}
		}
	}
	if s.tvEnabled() {
		s.huntTV(ctx, instances, profiles)
	}
	s.huntMusic(ctx, instances) // only while the music module is on
	s.huntBooks(ctx)            // only while ebooks or audiobooks are on
}

// huntMovie searches for one movie if it is monitored (or force is set, for
// a manual "search now") and wanted (missing, or downloaded below its
// profile cutoff), grabbing the best release that is not blocklisted. It
// reports whether something was grabbed.
func (s *Server) huntMovie(ctx context.Context, m library.Movie, instances []indexers.Instance, profiles profileSet, blocked map[string]bool, force bool) bool {
	if !m.Monitored && !force {
		return false
	}
	profile := s.profileForMovie(profiles, m)
	sources := s.sourcesFor(m.SourcePref)
	switch m.Status {
	case library.StatusMissing:
		if !force && unreleased(m.ReleaseDate) {
			return false // nothing to download yet
		}
		if !force {
			if note, hold := s.homeReleaseHold(ctx, m, profile); hold {
				s.noteHomeHold(m, note)
				return false // only in cinemas so far: wait for the digital or disc release
			}
		}
		outcomes := indexers.SearchAll(ctx, instances, m.Title, movieCategory)
		// A title search also returns other films with similar words; only
		// releases of this movie may be grabbed.
		best := pickBestResult(s.limitMovieResults(ctx, m, profile, !force, filterSources(matchingResults(dropBlocked(s.freshOnly(indexers.MergeResults(outcomes)), blocked), m), sources)), profile, m.Year)
		s.noteMovieSearch(ctx, noteMode(force), m, outcomes, blocked, sources, profile, best, "")
		if best == nil {
			return false
		}
		if _, err := s.grabMovie(m, best.Title, best.DownloadURL, best.SizeBytes, best.Protocol, searchKind(force)); err != nil {
			log.Printf("automation: hunt: grab %q for %q: %v", best.Title, m.Title, err)
			return false
		}
		s.noteFallbackGrab(m.ID, 0, m.Title, profile, best.Title)
		return true
	case library.StatusDownloaded:
		if m.NoUpgrade || !wantsUpgrade(profile, m.Quality, m.ReleaseTitle) {
			return false // left alone, upgrades off, unknown baseline, or already at/above cutoff: skip the search entirely
		}
		currentTier := quality.Tier(m.Quality)
		outcomes := indexers.SearchAll(ctx, instances, m.Title, movieCategory)
		best := pickUpgradeResult(s.limitMovieResults(ctx, m, profile, !force, filterSources(matchingResults(dropBlocked(s.freshOnly(indexers.MergeResults(outcomes)), blocked), m), sources)), profile, currentTier, m.ReleaseTitle, m.Year)
		s.noteMovieSearch(ctx, noteMode(force), m, outcomes, blocked, sources, profile, best, currentTier)
		if best == nil {
			return false
		}
		if _, err := s.grabMovie(m, best.Title, best.DownloadURL, best.SizeBytes, best.Protocol, searchKind(force)); err != nil {
			log.Printf("automation: hunt: upgrade grab %q for %q: %v", best.Title, m.Title, err)
			return false
		}
		return true
	}
	return false
}

// rssSync checks each indexer's "latest releases" feed (an empty-query
// search — Newznab/Torznab return their most recent posts rather than an
// error when q is omitted) against the wanted list, so a release posted
// moments ago can be grabbed without waiting for the next targeted search
// ("RSS sync"). Covers both missing movies and upgrade
// candidates, same as hunt — a release that just appeared could easily be
// a quality upgrade for something already downloaded, not just a fill for
// something missing.
func (s *Server) rssSync(ctx context.Context) {
	if !s.automationEnabled() {
		return
	}
	if !s.autoGrabRoom() {
		holdBack("rss-sync")
		return
	}
	movies, err := s.MovieRepo.ListWithCopies()
	if err != nil {
		log.Printf("automation: rss-sync: list movies: %v", err)
		return
	}
	profiles, err := s.loadProfiles()
	if err != nil {
		log.Printf("automation: rss-sync: load quality profiles: %v", err)
		return
	}

	var missing, upgradeCandidates []library.Movie
	for _, m := range movies {
		if !m.Monitored {
			continue
		}
		switch m.Status {
		case library.StatusMissing:
			if !unreleased(m.ReleaseDate) {
				if note, hold := s.homeReleaseHold(context.Background(), m, profiles.resolve(m.ProfileID)); hold {
					s.noteHomeHold(m, note)
					continue
				}
				missing = append(missing, m)
			}
		case library.StatusDownloaded:
			if !m.NoUpgrade && wantsUpgrade(profiles.resolve(m.ProfileID), m.Quality, m.ReleaseTitle) {
				upgradeCandidates = append(upgradeCandidates, m)
			}
		}
	}

	instances, err := s.searchableIndexers()
	if err != nil || len(instances) == 0 {
		return
	}
	if s.tvEnabled() {
		s.rssSyncTV(ctx, instances, profiles)
	}
	if !s.moviesEnabled() || (len(missing) == 0 && len(upgradeCandidates) == 0) {
		return
	}

	outcomes := indexers.SearchAll(ctx, instances, "", movieCategory)
	results := dropBlocked(s.freshOnly(indexers.MergeResults(outcomes)), s.blockedKeys())

	for _, m := range missing {
		best := pickBestResult(s.limitMovieResults(ctx, m, s.profileForMovie(profiles, m), true, filterSources(matchingResults(results, m), s.sourcesFor(m.SourcePref))), s.profileForMovie(profiles, m), m.Year)
		if best == nil {
			continue
		}
		if _, err := s.grabMovie(m, best.Title, best.DownloadURL, best.SizeBytes, best.Protocol, grabAutomatic); err != nil {
			log.Printf("automation: rss-sync: grab %q for %q: %v", best.Title, m.Title, err)
			continue
		}
		s.noteFallbackGrab(m.ID, 0, m.Title, s.profileForMovie(profiles, m), best.Title)
		if !s.autoGrabRoom() {
			holdBack("rss-sync")
			return
		}
	}
	for _, m := range upgradeCandidates {
		currentTier := quality.Tier(m.Quality)
		best := pickUpgradeResult(s.limitMovieResults(ctx, m, s.profileForMovie(profiles, m), true, filterSources(matchingResults(results, m), s.sourcesFor(m.SourcePref))), s.profileForMovie(profiles, m), currentTier, m.ReleaseTitle, m.Year)
		if best == nil {
			continue
		}
		if _, err := s.grabMovie(m, best.Title, best.DownloadURL, best.SizeBytes, best.Protocol, grabAutomatic); err != nil {
			log.Printf("automation: rss-sync: upgrade grab %q for %q: %v", best.Title, m.Title, err)
			continue
		}
		if !s.autoGrabRoom() {
			holdBack("rss-sync")
			return
		}
	}
}

func matchingResults(results []indexers.Result, m library.Movie) []indexers.Result {
	var out []indexers.Result
	for _, res := range results {
		if matchesMovie(res.Title, m) {
			out = append(out, res)
		}
	}
	return out
}

// pickBestResult returns the best result acceptable to profile or, when
// nothing is, the best result acceptable to the first of its fallback
// profiles (in order) that accepts anything. Returns nil if no profile in
// the chain accepts a result.
func pickBestResult(results []indexers.Result, profile quality.Profile, wantYear int) *indexers.Result {
	for _, p := range profile.Chain() {
		if best := pickBestFor(results, p, wantYear); best != nil {
			return best
		}
	}
	return nil
}

// pickBestFor returns the highest-quality-tier result one profile accepts,
// preferring a matching release year when the movie's year is known.
// Returns nil if nothing in results is acceptable.
func pickBestFor(results []indexers.Result, profile quality.Profile, wantYear int) *indexers.Result {
	var best *indexers.Result
	var bestKey pickKey
	for i := range results {
		release := parser.Parse(results[i].Title)
		if !profile.Accepts(release) {
			continue
		}
		if ok, _ := profile.TitleAllowed(results[i].Title); !ok {
			continue
		}
		if wantYear != 0 && release.Year != 0 && release.Year != wantYear {
			continue
		}
		if !quality.SizePlausible(quality.Classify(release), results[i].SizeBytes, 0) || !profile.SizeAllowed(results[i].SizeBytes) {
			continue
		}
		key := keyFor(profile, release, &results[i])
		if best == nil || key.above(bestKey) || (key == bestKey && preferOver(best, &results[i])) {
			bestKey = key
			best = &results[i]
		}
	}
	return best
}

// pickUpgradeResult is pickBestResult with the extra requirement that the
// result must actually be a quality upgrade over currentTier (and that
// currentTier hasn't already reached the profile's cutoff) — used for
// re-checking already-downloaded movies, as opposed to pickBestResult's
// "anything profile-accepted" bar for movies with nothing downloaded yet.
func pickUpgradeResult(results []indexers.Result, profile quality.Profile, currentTier quality.Tier, currentRelease string, wantYear int) *indexers.Result {
	var best *indexers.Result
	var bestKey pickKey
	for i := range results {
		release := parser.Parse(results[i].Title)
		if !profile.IsUpgradeOver(currentTier, currentRelease, results[i].Title) {
			continue
		}
		if ok, _ := profile.TitleAllowed(results[i].Title); !ok {
			continue
		}
		if wantYear != 0 && release.Year != 0 && release.Year != wantYear {
			continue
		}
		if !quality.SizePlausible(quality.Classify(release), results[i].SizeBytes, 0) || !profile.SizeAllowed(results[i].SizeBytes) {
			continue
		}
		key := keyFor(profile, release, &results[i])
		if best == nil || key.above(bestKey) || (key == bestKey && preferOver(best, &results[i])) {
			bestKey = key
			best = &results[i]
		}
	}
	return best
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// matchesMovie does a normalized (lowercased, punctuation-stripped) title
// comparison plus a year check when both sides have one. This is a
// simplification, not fuzzy/edit-distance matching — a known
// approximation worth revisiting if it mismatches often enough to matter
// in practice.
func matchesMovie(releaseTitle string, movie library.Movie) bool {
	release := parser.Parse(releaseTitle)
	if release.Year != 0 && movie.Year != 0 && release.Year != movie.Year {
		return false
	}
	return normalizeTitle(release.Title) == normalizeTitle(movie.Title)
}

// normalizeTitle reduces a title to letters and digits for comparison,
// treating "&" as "and" and ignoring a leading "The", "A" or "An", so
// "The Lord of the Rings" matches "Lord.of.the.Rings" and "Fast & Furious"
// matches "Fast.and.Furious".
func normalizeTitle(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "&", " and ")
	s = strings.TrimSpace(nonAlnum.ReplaceAllString(s, " ")) // dots, dashes and the like count as spaces
	for _, article := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(s, article) {
			s = s[len(article):]
			break
		}
	}
	return nonAlnum.ReplaceAllString(s, "")
}
