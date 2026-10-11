package organizer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/fileperms"
)

// Moving a title's files to another library folder.
//
// On the same drive a move is a rename: instant, and either done or not. To
// another drive the files are copied, every copy is read back and compared with
// the original, and only then are the originals removed. Nothing is ever
// overwritten, a move that fails half-way is undone, and the originals are only
// removed after the caller has recorded the new places (Commit).

// ErrMoveTargetExists is returned when something is already at the place a
// file or folder would be moved to.
var ErrMoveTargetExists = errors.New("something is already at the new place")

// MoveUnit is one file or one folder (with everything in it) to move.
type MoveUnit struct {
	Src, Dst string
	Dir      bool
}

// MoveOptions tunes a move.
type MoveOptions struct {
	// Progress is told how many bytes have just been copied.
	Progress func(n int64)
	// ForceCopy copies even where a rename would do (for tests).
	ForceCopy bool
}

type moveRecord struct {
	unit    MoveUnit
	renamed bool
	// copied: the destination was made by copying, so the source is still there
	copied    bool
	srcFiles  []string // files of the source that were copied (for Commit)
	srcDirs   []string // folders of the source tree, parents first
	madeDirs  []string // folders made above the destination, parents first
	dstExists bool     // the destination is in place and ours
}

// Move is a move in progress: every unit is in its new place, and the caller
// decides to Commit or Rollback.
type Move struct {
	recs []*moveRecord
}

// StartMove puts every unit in its new place. If any of them cannot be moved,
// the ones already moved are put back and the error says which failed.
func StartMove(ctx context.Context, units []MoveUnit, opt MoveOptions) (*Move, error) {
	m := &Move{}
	for _, u := range units {
		rec, err := moveUnit(ctx, u, opt)
		if rec != nil {
			m.recs = append(m.recs, rec)
		}
		if err != nil {
			m.Rollback()
			return nil, fmt.Errorf("move %s: %w", filepath.Base(u.Src), err)
		}
	}
	return m, nil
}

// Commit finishes a move: the originals of everything that was copied are
// removed (only the files that were copied; anything else in those folders is
// left). It returns the paths it could not remove. A move by rename has
// nothing left to do.
func (m *Move) Commit() []string {
	var left []string
	for _, rec := range m.recs {
		if !rec.copied {
			continue
		}
		for _, f := range rec.srcFiles {
			if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
				left = append(left, f)
			}
		}
		// Folders deepest first; one that still holds something stays.
		for i := len(rec.srcDirs) - 1; i >= 0; i-- {
			_ = os.Remove(rec.srcDirs[i])
		}
	}
	return left
}

// Rollback puts everything back as it was. It returns the paths it could not
// clean up (copies that could not be removed); nothing is ever lost, since the
// originals are untouched until Commit.
func (m *Move) Rollback() []string {
	var left []string
	for i := len(m.recs) - 1; i >= 0; i-- {
		rec := m.recs[i]
		switch {
		case rec.renamed:
			if err := os.Rename(rec.unit.Dst, rec.unit.Src); err != nil {
				left = append(left, rec.unit.Dst)
			}
		case rec.copied && rec.dstExists:
			var err error
			if rec.unit.Dir {
				err = os.RemoveAll(rec.unit.Dst) // a folder this move made, whole
			} else {
				err = os.Remove(rec.unit.Dst)
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				left = append(left, rec.unit.Dst)
			}
		}
		for j := len(rec.madeDirs) - 1; j >= 0; j-- {
			_ = os.Remove(rec.madeDirs[j]) // only when empty
		}
	}
	m.recs = nil
	return left
}

// Destinations lists where every unit now is (for the caller's records).
func (m *Move) Destinations() []MoveUnit {
	out := make([]MoveUnit, 0, len(m.recs))
	for _, r := range m.recs {
		out = append(out, r.unit)
	}
	return out
}

func moveUnit(ctx context.Context, u MoveUnit, opt MoveOptions) (*moveRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(u.Src)
	if err != nil {
		return nil, fmt.Errorf("look at the original: %w", err)
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, errors.New("it is a link, and links are not moved")
	case u.Dir && !info.IsDir():
		return nil, errors.New("it is not a folder")
	case !u.Dir && !info.Mode().IsRegular():
		return nil, errors.New("it is not a plain file")
	}
	if _, err := os.Lstat(u.Dst); err == nil {
		return nil, fmt.Errorf("%s: %w", u.Dst, ErrMoveTargetExists)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("look at the new place: %w", err)
	}

	rec := &moveRecord{unit: u}
	// The folders above the new place that do not exist yet.
	var missing []string
	for d := filepath.Dir(u.Dst); ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil || filepath.Dir(d) == d {
			break
		}
		missing = append(missing, d)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		rec.madeDirs = append(rec.madeDirs, missing[i])
	}
	if err := fileperms.MkdirAll(filepath.Dir(u.Dst)); err != nil {
		return rec, fmt.Errorf("make the folder for it: %w", err)
	}

	if !opt.ForceCopy {
		err := renameNoReplace(u.Src, u.Dst, u.Dir)
		if err == nil {
			rec.renamed = true
			return rec, nil
		}
		if !isCrossDevice(err) {
			return rec, err
		}
		// Another drive: carry on below with a copy.
	}

	rec.copied = true
	if u.Dir {
		err = copyTreeVerified(ctx, rec, opt)
	} else {
		err = copyFileVerified(ctx, u.Src, u.Dst, info, opt)
		if err == nil {
			rec.srcFiles = []string{u.Src}
		}
	}
	if err != nil {
		rec.dstExists = false // whatever was made has been cleaned up
		return rec, err
	}
	rec.dstExists = true
	return rec, nil
}

// renameNoReplace moves src to dst, failing when dst exists.
func renameNoReplace(src, dst string, dir bool) error {
	if dir {
		// Rename onto an existing, empty folder would succeed: look first.
		if _, err := os.Lstat(dst); err == nil {
			return fmt.Errorf("%s: %w", dst, ErrMoveTargetExists)
		}
		return os.Rename(src, dst)
	}
	// A hard link fails if the name is taken, which is the no-overwrite we
	// want; the original name is removed after.
	err := os.Link(src, dst)
	if err == nil {
		if rerr := os.Remove(src); rerr != nil {
			_ = os.Remove(dst)
			return rerr
		}
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s: %w", dst, ErrMoveTargetExists)
	}
	if isCrossDevice(err) {
		return err
	}
	// No hard links here: rename, after a last look.
	if _, serr := os.Lstat(dst); serr == nil {
		return fmt.Errorf("%s: %w", dst, ErrMoveTargetExists)
	}
	return os.Rename(src, dst)
}

const (
	movingSuffix = ".mediarium-moving"
	movingMarker = ".mediarium-moving"
)

// copyTreeVerified copies a folder into a staging folder next to its new place,
// checks every file, and renames the staging folder into place.
func copyTreeVerified(ctx context.Context, rec *moveRecord, opt MoveOptions) error {
	src, dst := rec.unit.Src, rec.unit.Dst
	staging := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+movingSuffix)
	if _, err := os.Lstat(staging); err == nil {
		// Left by an earlier move that stopped half-way. Ours only if it says so.
		if _, merr := os.Lstat(filepath.Join(staging, movingMarker)); merr != nil {
			return fmt.Errorf("%s is in the way and is not from Mediarium", staging)
		}
		if err := os.RemoveAll(staging); err != nil {
			return fmt.Errorf("clear an earlier attempt: %w", err)
		}
	}
	if err := os.Mkdir(staging, 0o755); err != nil {
		return fmt.Errorf("make the staging folder: %w", err)
	}
	fileperms.ApplyDir(staging)
	cleanup := func() { _ = os.RemoveAll(staging) }
	if err := os.WriteFile(filepath.Join(staging, movingMarker), nil, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("mark the staging folder: %w", err)
	}

	var files, dirs []string
	walkErr := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(staging, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a link, and links are not moved", rel)
		case d.IsDir():
			dirs = append(dirs, path)
			if rel == "." {
				return nil
			}
			if err := os.Mkdir(target, 0o755); err != nil {
				return err
			}
			fileperms.ApplyDir(target)
			return nil
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			if err := copyFileVerified(ctx, path, target, info, opt); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			files = append(files, path)
			return nil
		default:
			return fmt.Errorf("%s is not a plain file", rel)
		}
	})
	if walkErr != nil {
		cleanup()
		return walkErr
	}
	if err := os.Remove(filepath.Join(staging, movingMarker)); err != nil {
		cleanup()
		return fmt.Errorf("finish the staging folder: %w", err)
	}
	if _, err := os.Lstat(dst); err == nil {
		cleanup()
		return fmt.Errorf("%s: %w", dst, ErrMoveTargetExists)
	}
	if err := os.Rename(staging, dst); err != nil {
		cleanup()
		return fmt.Errorf("put the copy in place: %w", err)
	}
	rec.srcFiles, rec.srcDirs = files, dirs
	return nil
}

// copyFileVerified copies src to dst (which must not exist). The copy is
// written under a temporary name, flushed, read back and compared with what was
// read from src, and only then given its name. Nothing is left behind on failure.
func copyFileVerified(ctx context.Context, src, dst string, info fs.FileInfo, opt MoveOptions) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open the original: %w", err)
	}
	defer in.Close()

	tmp := dst + stagingSuffix
	_ = os.Remove(tmp) // ours by name
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return fmt.Errorf("create the copy: %w", err)
	}
	defer func() {
		if err != nil {
			out.Close()
			_ = os.Remove(tmp)
		}
	}()

	sum := sha256.New()
	reader := &ctxReader{ctx: ctx, r: io.TeeReader(in, sum), progress: opt.Progress}
	n, err := io.Copy(out, reader)
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if n != info.Size() {
		return fmt.Errorf("copy: read %d bytes, expected %d (the file changed while it was copied)", n, info.Size())
	}
	if err = out.Sync(); err != nil {
		return fmt.Errorf("flush the copy: %w", err)
	}
	if err = out.Close(); err != nil {
		return fmt.Errorf("close the copy: %w", err)
	}
	if err = verifyFile(ctx, tmp, n, sum.Sum(nil)); err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if cfg := fileperms.Get(); cfg.File != 0 {
		mode = cfg.File
	}
	_ = os.Chmod(tmp, mode)
	_ = os.Chtimes(tmp, time.Now(), info.ModTime())
	if err = publishNew(tmp, dst); err != nil {
		return err
	}
	return nil
}

// verifyFile reads path back and checks its size and SHA-256.
func verifyFile(ctx context.Context, path string, size int64, want []byte) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("check the copy: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, &ctxReader{ctx: ctx, r: f})
	if err != nil {
		return fmt.Errorf("check the copy: %w", err)
	}
	if n != size {
		return fmt.Errorf("check the copy: it is %d bytes, expected %d", n, size)
	}
	if string(h.Sum(nil)) != string(want) {
		return errors.New("check the copy: it does not match the original")
	}
	return nil
}

// ctxReader stops reading when the context ends and reports what it read.
type ctxReader struct {
	ctx      context.Context
	r        io.Reader
	progress func(int64)
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	if n > 0 && c.progress != nil {
		c.progress(int64(n))
	}
	return n, err
}

// MapPath says where a path went after a move, for the caller's records. The
// units are those of the move; ok is false for a path none of them covers.
func MapPath(units []MoveUnit, p string) (string, bool) {
	// The longest source wins, so a file unit inside a folder unit is exact.
	sorted := append([]MoveUnit(nil), units...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i].Src) > len(sorted[j].Src) })
	for _, u := range sorted {
		if p == u.Src {
			return u.Dst, true
		}
		if u.Dir && strings.HasPrefix(p, u.Src+string(filepath.Separator)) {
			return filepath.Join(u.Dst, strings.TrimPrefix(p, u.Src+string(filepath.Separator))), true
		}
	}
	return "", false
}
