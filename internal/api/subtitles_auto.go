package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/fileperms"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/notify"
	"github.com/rdborg/mediarium/internal/settings"
	"github.com/rdborg/mediarium/internal/subtitles"
)

const (
	subtitleSweepInterval   = 6 * time.Hour
	subtitleRetryAfter      = 3 * 24 * time.Hour
	maxSubtitleDownloadsRun = 20
)

var errNoSubtitle = errors.New("no subtitle found")

// errSubtitlesOff means subtitles are switched off in Settings, so nothing is
// searched for or downloaded.
var errSubtitlesOff = errors.New("Subtitles are switched off. Turn them on in Settings > Info, lists and subtitles > Subtitles.")

func pathBase(p string) string { return filepath.Base(p) }
func sortSubtitleResults(r []subtitleResultPayload) {
	sort.SliceStable(r, func(i, j int) bool { return r[i].Score > r[j].Score })
}

// subtitleItem is a downloaded movie or episode that subtitles attach to.
type subtitleItem struct {
	kind       string // "movie" or "episode"
	id         int64
	title      string
	subtitle   string // "S01E02 · Name" for episodes
	tmdbID     int    // movie
	parentTMDB int    // series (episodes)
	season     int
	episode    int
	filePath   string
}

func (it subtitleItem) ref() subtitles.Item { return subtitles.Item{Kind: it.kind, ID: it.id} }

func movieSubtitleItem(m library.Movie) subtitleItem {
	return subtitleItem{kind: "movie", id: m.ID, title: m.Title, tmdbID: m.TMDBID, filePath: m.FilePath}
}

func episodeSubtitleItem(sr library.Series, ep library.Episode) subtitleItem {
	label := fmt.Sprintf("S%02dE%02d", ep.Season, ep.Episode)
	if ep.Title != "" {
		label += " · " + ep.Title
	}
	return subtitleItem{
		kind: "episode", id: ep.ID, title: sr.Title, subtitle: label,
		parentTMDB: sr.TMDBID, season: ep.Season, episode: ep.Episode, filePath: ep.FilePath,
	}
}

// subtitleLanguages are the languages to keep subtitles for (setting,
// comma-separated OpenSubtitles codes; default English).
func (s *Server) subtitleLanguages() []string {
	v, _ := s.Settings.Get(settings.KeySubtitleLanguages)
	var out []string
	for _, l := range strings.Split(v, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return []string{"en"}
	}
	return out
}

// subtitlesEnabled reports whether the subtitles switch in Settings is on. It
// is off unless the person turned it on ("1"). While it is off nothing
// searches for or downloads subtitles, and nothing in the app mentions them
// (subtitles that come inside a download are still imported: that is import
// handling, not downloading).
func (s *Server) subtitlesEnabled() bool {
	v, _ := s.Settings.Get(settings.KeySubtitlesEnabled)
	return v == "1"
}

// refuseIfSubtitlesOff answers 409 and returns true while subtitles are
// switched off; the subtitle endpoints call it first.
func (s *Server) refuseIfSubtitlesOff(w http.ResponseWriter) bool {
	if s.subtitlesEnabled() {
		return false
	}
	writeError(w, http.StatusConflict, errSubtitlesOff.Error())
	return true
}

// autoSubtitleSetting is the stored "download automatically" choice, whether
// or not the subtitles switch is on.
func (s *Server) autoSubtitleSetting() bool {
	v, _ := s.Settings.Get(settings.KeySubtitleAutoDownload)
	return v == "1"
}

// autoSubtitlesEnabled reports whether Mediarium fetches subtitles on its own:
// subtitles are switched on and the person also chose automatic downloading
// ("1"). By default subtitles are offered, not fetched, so nothing spends
// OpenSubtitles' small daily download allowance without being asked.
func (s *Server) autoSubtitlesEnabled() bool {
	return s.subtitlesEnabled() && s.autoSubtitleSetting()
}

func (it subtitleItem) query(lang string) subtitles.Query {
	if it.kind == "episode" {
		return queryFileInfo(subtitles.Query{ParentTMDBID: it.parentTMDB, Season: it.season, Episode: it.episode, Type: "episode", Language: lang}, it.filePath)
	}
	return queryFileInfo(subtitles.Query{TMDBID: it.tmdbID, Type: "movie", Language: lang}, it.filePath)
}

// subtitleBase is the video path without its extension; external subtitles
// live next to it as "<base>.<lang>.srt", the convention Plex, Jellyfin and
// Kodi all recognise.
func subtitleBase(videoPath string) string {
	return strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
}

// subtitleProfiles are the per-language choices (normal, SDH, forced) from
// Settings; a language with none gets the default.
func (s *Server) subtitleProfiles() subtitles.Profiles {
	v, _ := s.Settings.Get(settings.KeySubtitleProfiles)
	return subtitles.ParseProfiles(v)
}

// countEmbeddedSubtitles: subtitle tracks inside the video count as there
// (on unless switched off).
func (s *Server) countEmbeddedSubtitles() bool {
	return s.settingOn(settings.KeySubtitleCountEmbedded, true)
}

// diskSubtitles lists what is saved next to a video, per language and kind
// (a ".forced" file is a forced track, ".sdh" one for the hard of hearing).
func diskSubtitles(videoPath string) subtitles.HaveMap { return subtitles.DiskHave(videoPath) }

// embeddedSubtitleLanguages are the full subtitle tracks inside the video,
// when those count; none otherwise.
func (s *Server) embeddedSubtitleLanguages(videoPath string) map[string]bool {
	if videoPath == "" || !s.countEmbeddedSubtitles() {
		return map[string]bool{}
	}
	return subtitles.EmbeddedLanguages(videoPath)
}

// embeddedSubtitles is what the tracks inside the video make, per language
// and kind, when those count; nothing otherwise.
func (s *Server) embeddedSubtitles(videoPath string) subtitles.HaveMap {
	if videoPath == "" || !s.countEmbeddedSubtitles() {
		return subtitles.HaveMap{}
	}
	return subtitles.EmbeddedHave(videoPath)
}

// haveSubtitles is everything that counts as already there for a video: the
// files next to it and, unless switched off, the tracks inside it.
func (s *Server) haveSubtitles(videoPath string) subtitles.HaveMap {
	have := diskSubtitles(videoPath)
	have.Merge(s.embeddedSubtitles(videoPath))
	return have
}

// missingSubtitleLanguages lists the subtitles a downloaded video still needs,
// as slot keys: "en" for a language's full subtitle (as before profiles
// existed) and "en.forced" for its forced track.
func (s *Server) missingSubtitleLanguages(it subtitleItem) []string {
	have := s.haveSubtitles(it.filePath)
	profiles := s.subtitleProfiles()
	var missing []string
	for _, l := range s.subtitleLanguages() {
		p := profiles.For(l)
		for _, slot := range p.Missing(have.For(l)) {
			missing = append(missing, slot.Key(l))
		}
	}
	return missing
}

// dismissedSubtitles returns the titles marked "no subtitles wanted". A read
// error is logged and treated as none dismissed.
func (s *Server) dismissedSubtitles() map[subtitles.Item]bool {
	m, err := s.SubtitleDismissed.All()
	if err != nil {
		log.Printf("subtitles: read dismissed titles: %v", err)
		return map[subtitles.Item]bool{}
	}
	return m
}

// noteSubtitleDownload records a download OpenSubtitles counted against the
// daily limit, and the quota figures its response carried.
func (s *Server) noteSubtitleDownload(info subtitles.DownloadInfo) {
	now := time.Now()
	if err := s.SubtitleQuota.RecordDownload(now); err != nil {
		log.Printf("subtitles: %v", err)
	}
	if info.HasRemaining {
		s.saveSubtitleQuotaReport(info, now)
		if info.Remaining == 0 {
			// That was the last download the limit allows.
			s.Usage.RecordLimitHit(serviceOpenSubtitles)
		}
	}
}

// noteSubtitleQuotaError remembers what a refused download said about the limit.
func (s *Server) noteSubtitleQuotaError(info subtitles.DownloadInfo) {
	if info.HasRemaining {
		s.saveSubtitleQuotaReport(info, time.Now())
	}
}

func (s *Server) saveSubtitleQuotaReport(info subtitles.DownloadInfo, at time.Time) {
	rep := subtitles.QuotaReport{
		Remaining: info.Remaining, Requests: info.Requests, HasRequests: info.HasRequests,
		ResetAt: info.ResetAt, ObservedAt: at,
	}
	if err := s.SubtitleQuota.SaveReport(rep); err != nil {
		log.Printf("subtitles: %v", err)
	}
}

// subtitleQuota is the current best knowledge of today's download allowance.
func (s *Server) subtitleQuota() subtitles.QuotaState {
	now := time.Now()
	rep, err := s.SubtitleQuota.LoadReport()
	if err != nil {
		log.Printf("subtitles: %v", err)
	}
	downloads, err := s.SubtitleQuota.DownloadsSince(now.Add(-subtitles.QuotaWindow))
	if err != nil {
		log.Printf("subtitles: %v", err)
	}
	return subtitles.ComputeQuota(now, s.Subtitles().HasCredentials(), rep, downloads)
}

// subtitleData fetches the chosen subtitle from where it was found.
func (s *Server) subtitleData(ctx context.Context, it subtitleItem, pick subtitlePick) ([]byte, error) {
	if pick.source == subtitles.SourceSubDL {
		season, episode := 0, 0
		if it.kind == "episode" {
			season, episode = it.season, it.episode
		}
		return s.SubDL().Download(ctx, pick.link, season, episode)
	}
	if pick.source == subtitles.SourceWizdom {
		season, episode := 0, 0
		if it.kind == "episode" {
			season, episode = it.season, it.episode
		}
		return s.wizdomClient().Download(ctx, pick.link, season, episode)
	}
	if pick.source == subtitles.SourceAddon {
		link, ok := s.openAddonLink(pick.link)
		if !ok {
			return nil, errors.New("that subtitle's link isn't valid any more. Search again and pick it from the list")
		}
		season, episode := 0, 0
		if it.kind == "episode" {
			season, episode = it.season, it.episode
		}
		return s.addons.Download(ctx, link, pick.lang, season, episode)
	}
	info, err := s.Subtitles().RequestDownloadInfo(ctx, pick.fileID)
	if err != nil {
		var qe *subtitles.QuotaError
		if errors.As(err, &qe) {
			s.noteSubtitleQuotaError(qe.Info)
		}
		return nil, err
	}
	// OpenSubtitles counts the download once it hands out the link, whether or
	// not the file fetch below then succeeds.
	s.noteSubtitleDownload(info)
	return s.Subtitles().DownloadFile(ctx, info.Link)
}

func (s *Server) writeSubtitle(ctx context.Context, it subtitleItem, lang string, pick subtitlePick) (string, error) {
	pick.lang = lang
	data, err := s.subtitleData(ctx, it, pick)
	if err != nil {
		return "", err
	}
	path := subtitles.SidecarPath(it.filePath, lang, pick.variant)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write subtitle file: %w", err)
	}
	_ = fileperms.ApplyFile(path)
	// A copy kept by the timing tool belonged to the subtitle just replaced:
	// "Put the original back" must not bring that one back.
	if err := os.Remove(path + ".bak"); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("subtitles: remove the old backup of %s: %v", filepath.Base(path), err)
	}
	slot := subtitles.SlotFull
	if pick.variant == subtitles.VariantForced {
		slot = subtitles.SlotForced
	}
	s.recordSubtitle(it, slot.Key(lang), path, pick)
	movieID := int64(0)
	if it.kind == "movie" {
		movieID = it.id
	}
	name := it.title
	if it.subtitle != "" {
		name += " " + strings.SplitN(it.subtitle, " ", 2)[0]
	}
	what := lang
	switch pick.variant {
	case subtitles.VariantForced:
		what += " forced"
	case subtitles.VariantSDH:
		what += " SDH"
	}
	subtitleMessage := fmt.Sprintf("Downloaded %s subtitle for %s", what, name)
	if pick.upgrade {
		subtitleMessage = fmt.Sprintf("Swapped the %s subtitle for %s for one made for this exact file", what, name)
	}
	_ = s.QueueRepo.LogActivity(movieID, "subtitle", subtitleMessage)
	subItem := notify.Item{Media: it.kind, Title: it.title, Language: subtitles.LanguageLabel(lang), Path: path}
	if it.kind == "movie" {
		subItem.LinkPath = fmt.Sprintf("/title/%d", it.tmdbID)
	} else {
		subItem.Episode = strings.SplitN(it.subtitle, " ", 2)[0]
	}
	s.notifyItem("subtitle", subItem)
	return path, nil
}

// findSubtitles searches every source that is switched on: OpenSubtitles
// (unless useOpenSubtitles is false, after its daily limit), SubDL and the
// add-ons. A source that is switched off is never asked. One source failing
// is only logged when another answered; with all failing, the first error
// comes back.
func (s *Server) findSubtitles(ctx context.Context, q subtitles.Query, useOpenSubtitles bool) ([]subtitles.Result, error) {
	src := s.subtitleSources()
	var results []subtitles.Result
	var errs []error
	asked := 0
	if useOpenSubtitles && src.OpenSubtitles.On() {
		asked++
		r, err := s.Subtitles().Find(ctx, q)
		if err != nil {
			errs = append(errs, err)
		}
		results = append(results, r...)
	}
	if src.SubDL.On() {
		asked++
		r, err := s.SubDL().Find(ctx, q)
		if err != nil {
			errs = append(errs, err)
		}
		results = append(results, r...)
	}
	if r, ok, err := s.findWizdomSubtitles(ctx, q); ok {
		asked++
		if err != nil {
			errs = append(errs, err)
		}
		results = append(results, r...)
	}
	if src.Addons.On() {
		if r, n, es := s.findAddonSubtitles(ctx, q); n > 0 {
			asked += n
			errs = append(errs, es...)
			results = append(results, r...)
		}
	}
	switch {
	case asked == 0 && !useOpenSubtitles && src.OpenSubtitles.On():
		return nil, subtitles.ErrQuota
	case asked == 0 && src.AnySetUp():
		return nil, subtitles.ErrNoSource
	case asked == 0:
		return nil, subtitles.ErrNoAPIKey
	case len(errs) == asked:
		return nil, errs[0]
	}
	for _, err := range errs {
		log.Printf("subtitles: one source failed, using the other: %v", err)
	}
	return results, nil
}

// fetchBestSubtitle finds the subtitle that best fits the video file and
// saves it next to it. key is the slot being filled: a language ("en") for
// its full subtitle, "en.forced" for its forced track. useOpenSubtitles is
// false once its daily limit is used up, so only SubDL is asked.
func (s *Server) fetchBestSubtitle(ctx context.Context, it subtitleItem, key string, useOpenSubtitles bool) (string, error) {
	lang, slot := subtitles.SplitSlotKey(key)
	profile := s.subtitleProfiles().For(lang)
	q := profile.Apply(slot, it.query(lang))
	q.MovieHash = videoHash(it.filePath)
	results, err := s.findSubtitles(ctx, q, useOpenSubtitles)
	if err != nil {
		return "", err
	}
	best := subtitles.Pick(profile.Filter(slot, results), filepath.Base(it.filePath))
	if best == nil {
		return "", errNoSubtitle
	}
	variant := best.Variant()
	if slot == subtitles.SlotForced {
		variant = subtitles.VariantForced
	}
	return s.writeSubtitle(ctx, it, lang, subtitlePick{fileID: best.FileID, source: best.Source, link: best.Link, hashMatch: best.HashMatch, videoHash: q.MovieHash, variant: variant})
}

// downloadedSubtitleItems lists every downloaded movie and episode that has
// a file on disk.
func (s *Server) downloadedSubtitleItems() ([]subtitleItem, error) {
	var items []subtitleItem
	movies, err := s.MovieRepo.ListWithCopies()
	if err != nil {
		return nil, err
	}
	for _, m := range movies {
		if m.Status == library.StatusDownloaded && m.FilePath != "" {
			items = append(items, movieSubtitleItem(m))
		}
	}
	seriesList, err := s.MovieRepo.ListSeriesWithCopies()
	if err != nil {
		return nil, err
	}
	for _, sr := range seriesList {
		eps, err := s.MovieRepo.ListEpisodes(sr.ID)
		if err != nil {
			return nil, err
		}
		for _, ep := range eps {
			if ep.Status == library.StatusDownloaded && ep.FilePath != "" {
				items = append(items, episodeSubtitleItem(sr, ep))
			}
		}
	}
	return items, nil
}

// subtitleBacklog counts the downloaded titles still missing a wanted-language
// subtitle (leaving out titles marked "no subtitles wanted") and how many
// subtitle files that comes to (title x language).
func (s *Server) subtitleBacklog() (titles, files int, err error) {
	items, err := s.downloadedSubtitleItems()
	if err != nil {
		return 0, 0, err
	}
	dismissed := s.dismissedSubtitles()
	for _, it := range items {
		if dismissed[it.ref()] {
			continue
		}
		if _, err := os.Stat(it.filePath); err != nil {
			continue
		}
		if n := len(s.missingSubtitleLanguages(it)); n > 0 {
			titles++
			files += n
		}
	}
	return titles, files, nil
}

type subtitleFetchOptions struct {
	force bool // ignore the "looked for it recently" back-off
	limit int  // stop after this many downloads; 0 means no limit
}

type subtitleFetchResult struct {
	downloaded int
	notFound   int  // asked OpenSubtitles, which had nothing
	skipped    int  // wanted subtitle files not tried: dismissed title, missing video file, or looked for recently
	remaining  int  // wanted subtitle files not tried because the run stopped early
	quota      bool // stopped because OpenSubtitles' daily limit is used up
	timedOut   bool // stopped because the time allowed ran out
}

// fetchSubtitles downloads the missing wanted-language subtitles for items,
// one per (title, language), pacing itself through the client. It leaves out
// titles marked "no subtitles wanted", stops cleanly at the first quota error
// (or when the context ends) and counts what it did not get to. Errors that
// mean the setup is wrong (bad key, bad login) end the run with that error.
func (s *Server) fetchSubtitles(ctx context.Context, items []subtitleItem, opts subtitleFetchOptions) (subtitleFetchResult, error) {
	var res subtitleFetchResult
	if !s.subtitlesEnabled() {
		return res, errSubtitlesOff
	}
	dismissed := s.dismissedSubtitles()
	stopped := false
	src := s.subtitleSources()
	if !src.Any() {
		if src.AnySetUp() {
			return res, subtitles.ErrNoSource
		}
		return res, subtitles.ErrNoAPIKey
	}
	// useOS turns false once OpenSubtitles' daily limit is used up; SubDL or
	// the add-ons, when they are on, carry on alone. With OpenSubtitles
	// switched off its limit does not matter at all.
	useOS := src.OpenSubtitles.On()
	osOut := func() {
		useOS = false
		if !src.OthersThanOpenSubtitles() {
			stopped, res.quota = true, true
		}
	}
	if st := s.subtitleQuota(); useOS && st.Source == subtitles.QuotaReported && st.Exceeded {
		// OpenSubtitles already said the limit is used up until its reset.
		osOut()
	}
	for _, it := range items {
		langs := s.missingSubtitleLanguages(it)
		if len(langs) == 0 {
			continue
		}
		if dismissed[it.ref()] || it.filePath == "" {
			res.skipped += len(langs)
			continue
		}
		if _, err := os.Stat(it.filePath); err != nil {
			res.skipped += len(langs) // file moved or deleted outside Mediarium
			continue
		}
		for _, lang := range langs {
			switch {
			case stopped:
				res.remaining++
				continue
			case opts.limit > 0 && res.downloaded >= opts.limit:
				stopped = true
				res.remaining++
				continue
			case ctx.Err() != nil:
				stopped, res.timedOut = true, true
				res.remaining++
				continue
			}
			if !opts.force {
				if recent, err := s.SubtitleAttempts.Recent(it.kind, it.id, lang, subtitleRetryAfter); err == nil && recent {
					res.skipped++
					continue
				}
			}
			_, err := s.fetchBestSubtitle(ctx, it, lang, useOS)
			if errors.Is(err, subtitles.ErrQuota) && useOS {
				osOut()
				if !stopped {
					_, err = s.fetchBestSubtitle(ctx, it, lang, false) // the same subtitle from SubDL, if it has one
				}
			}
			switch {
			case err == nil:
				res.downloaded++
			case errors.Is(err, subtitles.ErrQuota):
				stopped, res.quota = true, true
				res.remaining++
			case ctx.Err() != nil:
				stopped, res.timedOut = true, true
				res.remaining++
			case errors.Is(err, subtitles.ErrInvalidKey), errors.Is(err, subtitles.ErrLoginFailed), errors.Is(err, subtitles.ErrNoAPIKey), errors.Is(err, subtitles.ErrNoSource), errors.Is(err, subtitles.ErrSubDLKey):
				return res, err
			default:
				if errors.Is(err, errNoSubtitle) {
					res.notFound++
				} else {
					log.Printf("subtitles: %s %d (%s): %v", it.kind, it.id, lang, err)
				}
				_ = s.SubtitleAttempts.Record(it.kind, it.id, lang)
			}
		}
	}
	return res, nil
}

// subtitleSweep fetches missing subtitles for the whole library, at most
// limit downloads per run (OpenSubtitles meters downloads per day). Items it
// already failed to find something for recently are skipped unless force is
// set. It stops early when the daily quota is reached, returning
// subtitles.ErrQuota.
func (s *Server) subtitleSweep(ctx context.Context, force bool, limit int) (int, error) {
	if !s.subtitlesEnabled() {
		return 0, errSubtitlesOff
	}
	if src := s.subtitleSources(); !src.Any() {
		if src.AnySetUp() {
			return 0, subtitles.ErrNoSource
		}
		return 0, subtitles.ErrNoAPIKey
	}
	items, err := s.downloadedSubtitleItems()
	if err != nil {
		return 0, err
	}
	res, err := s.fetchSubtitles(ctx, items, subtitleFetchOptions{force: force, limit: limit})
	if err != nil {
		return res.downloaded, err
	}
	if res.quota {
		return res.downloaded, subtitles.ErrQuota
	}
	return res.downloaded, nil
}

func (s *Server) subtitleSweepJob(ctx context.Context) {
	if !s.hasSubtitleSource() || !s.autoSubtitlesEnabled() {
		return
	}
	if _, err := s.subtitleSweep(ctx, false, maxSubtitleDownloadsRun); err != nil && !errors.Is(err, subtitles.ErrQuota) {
		log.Printf("subtitles: sweep: %v", err)
	}
	if s.subtitleUpgradesOn() {
		if _, err := s.upgradeSubtitles(ctx); err != nil {
			log.Printf("subtitles: look for better subtitles: %v", err)
		}
	}
}

// autoSubtitlesFor fetches the configured languages for a just-imported
// item, in the background, when automatic downloading is on.
func (s *Server) autoSubtitlesFor(items ...subtitleItem) {
	if !s.autoSubtitlesEnabled() || !s.hasSubtitleSource() {
		return
	}
	go func() {
		if !s.autoSubtitlesEnabled() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := s.fetchSubtitles(ctx, items, subtitleFetchOptions{force: true}); err != nil {
			log.Printf("subtitles: fetch after import: %v", err)
		}
	}()
}

// sidecarsIn lists the subtitles that came with a finished download. A problem
// reading the folder is logged and means none.
func (s *Server) sidecarsIn(dir string) []subtitles.Sidecar {
	if isClientView(dir) {
		return s.clientSidecars(dir)
	}
	scs, err := subtitles.FindSidecars(dir)
	if err != nil {
		log.Printf("subtitles: %v", err)
		return nil
	}
	return scs
}

// importSidecarSubtitles copies the subtitles that came with a release next to
// an imported video (see subtitles.ImportSidecars) and notes it in the
// activity log. It never fails the import: a problem is only logged.
func (s *Server) importSidecarSubtitles(scs []subtitles.Sidecar, videoPath string, movieID int64, name string) {
	if len(scs) == 0 || videoPath == "" {
		return
	}
	imported, err := subtitles.ImportSidecars(videoPath, scs)
	if err != nil {
		log.Printf("subtitles: import subtitles that came with %s: %v", name, err)
	}
	if len(imported) == 0 {
		return
	}
	files := 0
	var labels []string
	seen := map[string]bool{}
	for _, sc := range imported {
		files += len(sc.Files)
		if l := sc.Label(); !seen[l] {
			seen[l] = true
			labels = append(labels, l)
		}
	}
	sort.Strings(labels)
	_ = s.QueueRepo.LogActivity(movieID, "subtitle", fmt.Sprintf("%s imported with %s: %s", name, plural(files, "subtitle file"), strings.Join(labels, ", ")))
}
