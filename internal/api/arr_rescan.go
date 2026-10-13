package api

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/mediaservers"
	"github.com/rdborg/mediarium/internal/organizer"
)

// Apps that change a file after the import (Tdarr and the like) tell Radarr
// or Sonarr to look again with a "rescan" or "refresh" command. Mediarium does
// the same: it checks that each downloaded file is still where it was (and
// finds it again when only the extension changed, for example .mp4 to .mkv),
// and asks the media servers to scan the folder, so the new file shows up
// there. It never marks anything missing or deletes anything.

// stillThere returns where the file is now: the same path when it is still
// there, or the one video in its folder that has the same name apart from the
// extension. "" means it can't be found.
func stillThere(path string) string {
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	dir := filepath.Dir(path)
	want := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !organizer.IsVideoFile(name) || organizer.IsSample(name) {
			continue
		}
		if strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))) == want {
			found = append(found, filepath.Join(dir, name))
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return ""
}

// rescanReport says what a rescan found.
type rescanReport struct {
	Found   int `json:"found"`   // files that are where they should be
	Renamed int `json:"renamed"` // files found again under another extension
	Missing int `json:"missing"` // files that are gone (now wanted again when markMissing was asked for)
	Skipped int `json:"skipped"` // files left alone because the library folder looks unavailable
}

// libraryAvailable says the library folder is there and has something in it. A
// drive that isn't plugged in leaves an empty folder (or none), and a rescan
// must never read that as "every file is gone".
func libraryAvailable(root string) bool {
	if root == "" {
		return false
	}
	entries, err := os.ReadDir(root)
	return err == nil && len(entries) > 0
}

// rescanMovieFiles looks at one movie's file again. With markMissing, a file
// that is gone (and has no look-alike) makes the movie wanted again; the apps
// that ask by themselves (Tdarr) never get that, only a person pressing the button.
func (s *Server) rescanMovieFiles(id int64, markMissing bool) rescanReport {
	var rep rescanReport
	m, err := s.MovieRepo.Get(id)
	if err != nil || m.Status != library.StatusDownloaded || m.FilePath == "" {
		return rep
	}
	now := stillThere(m.FilePath)
	if now == "" {
		if !markMissing {
			slog.Warn("rescan: a movie's file is not where it was and no file with the same name was found", "movie", m.Title, "path", m.FilePath)
			return rep
		}
		if !libraryAvailable(s.movieRootFor(m)) {
			rep.Skipped++
			return rep
		}
		if err := s.MovieRepo.ClearMovieFile(id); err == nil {
			rep.Missing++
			_ = s.QueueRepo.LogActivity(id, "removed", fmt.Sprintf("%s: its file %s is not there any more, so the movie is wanted again.", movieLabel(m), filepath.Base(m.FilePath)))
		}
		return rep
	}
	if now != m.FilePath {
		if err := s.MovieRepo.SetStatus(id, m.Status, "", now); err != nil {
			slog.Warn("rescan: couldn't save a movie's new file name", "movie", m.Title, "err", err)
			return rep
		}
		slog.Info("rescan: a movie's file has a new name", "movie", m.Title, "from", filepath.Base(m.FilePath), "to", filepath.Base(now))
		rep.Renamed++
	} else {
		rep.Found++
	}
	s.mediaServersImported(mediaservers.MediaMovie, now)
	return rep
}

// rescanSeriesFiles does the same for every downloaded episode of a show.
func (s *Server) rescanSeriesFiles(id int64, markMissing bool) rescanReport {
	var rep rescanReport
	eps, err := s.MovieRepo.ListEpisodes(id)
	if err != nil {
		return rep
	}
	series, serr := s.MovieRepo.GetSeries(id)
	var files []string
	for _, e := range eps {
		if e.Status != library.StatusDownloaded || e.FilePath == "" {
			continue
		}
		now := stillThere(e.FilePath)
		if now == "" {
			if !markMissing {
				slog.Warn("rescan: an episode's file is not where it was and no file with the same name was found", "series", id, "season", e.Season, "episode", e.Episode, "path", e.FilePath)
				continue
			}
			if serr != nil || !libraryAvailable(s.seriesRootFor(series)) {
				rep.Skipped++
				continue
			}
			if err := s.MovieRepo.ClearEpisodeFile(e.ID); err == nil {
				rep.Missing++
			}
			continue
		}
		if now != e.FilePath {
			if err := s.MovieRepo.SetEpisodeStatus(e.ID, e.Status, "", now); err != nil {
				slog.Warn("rescan: couldn't save an episode's new file name", "series", id, "err", err)
				continue
			}
			slog.Info("rescan: an episode's file has a new name", "series", id, "season", e.Season, "episode", e.Episode, "from", filepath.Base(e.FilePath), "to", filepath.Base(now))
			rep.Renamed++
		} else {
			rep.Found++
		}
		files = append(files, now)
	}
	if rep.Missing > 0 && serr == nil {
		_ = s.QueueRepo.LogSeriesActivity(id, "removed", fmt.Sprintf("%s: %d episode file(s) are not on the disk any more, so those episodes are wanted again.", seriesLabel(series), rep.Missing))
	}
	if len(files) > 0 {
		s.mediaServersImported(mediaservers.MediaTV, files...)
	}
	return rep
}
