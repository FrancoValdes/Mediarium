package api

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/rdborg/mediarium/internal/settings"
	"github.com/rdborg/mediarium/internal/subtitles"
)

// Subtitle upgrades. When Mediarium picks a subtitle by its release name, it
// is usually in sync but not always. For a month after that, while subtitles
// download automatically, it looks again every few days for one made for the
// exact video file (OpenSubtitles matches it by the file's hash) and swaps
// it in. Subtitles picked by hand, or changed since (moved in time, edited),
// are left alone.

const (
	subtitleUpgradeWindow  = 30 * 24 * time.Hour
	subtitleUpgradeRecheck = 3 * 24 * time.Hour
	maxSubtitleUpgradeRun  = 5  // swaps per run
	maxSubtitleUpgradeLook = 40 // searches per run
	subtitleUpgradeReserve = 3  // daily downloads left for new imports
)

// subtitlePick is how a subtitle came to be chosen, kept with it.
type subtitlePick struct {
	fileID    int               // OpenSubtitles
	source    string            // subtitles.SourceSubDL, or empty for OpenSubtitles
	link      string            // SubDL's download path
	hashMatch bool              // made for the exact video file
	chosen    bool              // picked by hand
	videoHash string            // the video's hash, when known
	upgrade   bool              // replaces a subtitle Mediarium picked before
	lang      string            // the language it is saved for (set when it is written)
	variant   subtitles.Variant // plain, SDH or forced: part of the file's name
}

// subtitleUpgradesOn: on unless switched off, and only while subtitles
// download automatically. Only OpenSubtitles can match a subtitle to the exact
// video file, so with it switched off (or without a key) there is nothing to
// look for.
func (s *Server) subtitleUpgradesOn() bool {
	return s.autoSubtitlesEnabled() && s.subtitleUpgradeSetting() && s.useOpenSubtitles()
}

func (s *Server) subtitleUpgradeSetting() bool { return s.settingOn(settings.KeySubtitleUpgrade, true) }

// videoHash is the OpenSubtitles hash of a video, or "" when it can't be read.
func videoHash(path string) string {
	if path == "" {
		return ""
	}
	h, err := subtitles.FileHash(path)
	if err != nil {
		return ""
	}
	return h
}

// recordSubtitle remembers a saved subtitle, so an upgrade can find it.
func (s *Server) recordSubtitle(it subtitleItem, lang, path string, pick subtitlePick) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	err = s.SubtitleFiles.Record(subtitles.SavedFile{
		Kind: it.kind, MediaID: it.id, Language: lang, FileID: pick.fileID, HashMatch: pick.hashMatch, Chosen: pick.chosen,
		VideoHash: pick.videoHash, Path: path, Size: info.Size(), ModTime: info.ModTime().Unix(),
	})
	if err != nil {
		log.Printf("subtitles: %v", err)
	}
}

// unchangedSubtitle reports whether a saved subtitle is still where and as
// Mediarium left it, next to the same video file. f.Language is the slot key
// ("en" or "en.forced").
func unchangedSubtitle(f subtitles.SavedFile, it subtitleItem) bool {
	if it.filePath == "" {
		return false
	}
	lang, _ := subtitles.SplitSlotKey(f.Language)
	if _, ok := subtitles.VariantOfPath(it.filePath, lang, f.Path); !ok {
		return false
	}
	if f.VideoHash != "" && videoHash(it.filePath) != f.VideoHash {
		return false // the video was replaced
	}
	info, err := os.Stat(f.Path)
	return err == nil && info.Size() == f.Size && info.ModTime().Unix() == f.ModTime
}

type subtitleUpgradeResult struct {
	looked, swapped int
}

// upgradeSubtitles looks for subtitles made for the exact video file to
// replace ones Mediarium picked by name in the last month.
func (s *Server) upgradeSubtitles(ctx context.Context) (subtitleUpgradeResult, error) {
	var res subtitleUpgradeResult
	if !s.useOpenSubtitles() {
		return res, nil
	}
	now := time.Now()
	cands, err := s.SubtitleFiles.UpgradeCandidates(now.Add(-subtitleUpgradeWindow), now.Add(-subtitleUpgradeRecheck), maxSubtitleUpgradeLook)
	if err != nil {
		return res, err
	}
	for _, f := range cands {
		if ctx.Err() != nil || res.swapped >= maxSubtitleUpgradeRun {
			break
		}
		if st := s.subtitleQuota(); st.Remaining <= subtitleUpgradeReserve {
			break
		}
		it, err := s.loadSubtitleItem(f.Kind, f.MediaID)
		if errors.Is(err, errSubtitleItemNotFound) {
			_ = s.SubtitleFiles.Forget(f.Kind, f.MediaID, f.Language)
			continue
		}
		if err != nil {
			return res, err
		}
		if !unchangedSubtitle(f, it) {
			// Moved in time, edited, removed, or the video was replaced: not
			// Mediarium's to swap any more.
			_ = s.SubtitleFiles.Forget(f.Kind, f.MediaID, f.Language)
			continue
		}
		hash := videoHash(it.filePath)
		_ = s.SubtitleFiles.MarkChecked(f.Kind, f.MediaID, f.Language)
		if hash == "" {
			continue
		}
		lang, slot := subtitles.SplitSlotKey(f.Language)
		profile := s.subtitleProfiles().For(lang)
		// A better subtitle is the same kind as the one it replaces; where the
		// profile takes a normal or an SDH one, either kind will do (the old
		// file is then removed, since it is replaced).
		variant, _ := subtitles.VariantOfPath(it.filePath, lang, f.Path)
		q := profile.Apply(slot, it.query(lang))
		q.MovieHash = hash
		results, err := s.Subtitles().Find(ctx, q)
		res.looked++
		switch {
		case errors.Is(err, subtitles.ErrInvalidKey), errors.Is(err, subtitles.ErrLoginFailed), errors.Is(err, subtitles.ErrNoAPIKey):
			return res, err
		case err != nil:
			log.Printf("subtitles: look for a better %s subtitle for %s %d: %v", f.Language, f.Kind, f.MediaID, err)
			continue
		}
		var sameKind []subtitles.Result
		for _, r := range profile.Filter(slot, results) {
			if r.Variant() == variant || (slot == subtitles.SlotFull && profile.Normalize().Full == subtitles.FullEither) {
				sameKind = append(sameKind, r)
			}
		}
		best := subtitles.Pick(sameKind, pathBase(it.filePath))
		if best == nil || !best.HashMatch || best.FileID == f.FileID {
			continue
		}
		newVariant := variant
		if slot == subtitles.SlotFull {
			newVariant = best.Variant()
		}
		if newVariant != variant {
			if _, statErr := os.Lstat(subtitles.SidecarPath(it.filePath, lang, newVariant)); statErr == nil {
				continue // another subtitle of that kind is already there: not Mediarium's to overwrite
			}
		}
		newPath, err := s.writeSubtitle(ctx, it, lang, subtitlePick{fileID: best.FileID, hashMatch: true, videoHash: hash, upgrade: true, variant: newVariant})
		switch {
		case errors.Is(err, subtitles.ErrQuota):
			return res, nil
		case err != nil:
			log.Printf("subtitles: swap the %s subtitle for %s %d: %v", f.Language, f.Kind, f.MediaID, err)
			continue
		}
		if newPath != f.Path {
			// The better one has another name (plain against SDH): the old file is replaced.
			for _, old := range []string{f.Path, f.Path + ".bak"} {
				if rmErr := os.Remove(old); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
					log.Printf("subtitles: remove the replaced %s: %v", filepath.Base(old), rmErr)
				}
			}
		}
		res.swapped++
	}
	return res, nil
}
