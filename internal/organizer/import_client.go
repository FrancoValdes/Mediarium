package organizer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rdborg/mediarium/internal/fileperms"
)

// ErrLinkNotAllowed is returned by ImportClientFile for a symbolic link whose
// file isn't inside the folder links may point into (or when no such folder
// is set).
var ErrLinkNotAllowed = errors.New("symbolic link to a place it may not point to")

// ImportClientFile imports src, a file in an external download client's
// folder. The client may still be seeding it, so src is never moved or
// changed.
//
// A plain file is imported exactly as Import does (hardlink, or a copy).
//
// A symbolic link is how some clients (Decypharr) hand over files that live
// on a mounted cloud drive. Such a link is imported as a new link to the same
// file, and only when that file, with every link on the way followed, is a
// plain file inside linkRoot. Anything else, including any link at all when
// linkRoot is empty, is refused with ErrLinkNotAllowed. Import itself keeps
// refusing links: this is the one place they are allowed, and only for the
// download client's folder.
func ImportClientFile(src, destPath string, policy ConflictPolicy, isBetter func() bool, linkRoot string) (ImportResult, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return ImportResult{}, fmt.Errorf("stat source %s: %w", src, err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return Import(src, destPath, policy, isBetter)
	}
	target, err := allowedLinkTarget(src, linkRoot)
	if err != nil {
		return ImportResult{}, err
	}
	return importLink(target, destPath, policy, isBetter)
}

// allowedLinkTarget resolves the link at src and returns the plain file it
// leads to, when that file is inside root.
func allowedLinkTarget(src, root string) (string, error) {
	name := filepath.Base(src)
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("%s is a symbolic link and no folder is set for links to point into: %w", name, ErrLinkNotAllowed)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("the folder links may point into (%s) can't be read: %w", root, err)
	}
	if realRoot, err = filepath.Abs(realRoot); err != nil {
		return "", err
	}
	// Every link on the way is followed, so "..", relative targets and
	// chains through other links end up as one real path to check.
	target, err := filepath.EvalSymlinks(src)
	if err != nil {
		return "", fmt.Errorf("%s is a symbolic link that leads nowhere (%v): %w", name, err, ErrLinkNotAllowed)
	}
	if target, err = filepath.Abs(target); err != nil {
		return "", err
	}
	if !insideDir(realRoot, target) {
		return "", fmt.Errorf("%s is a symbolic link to %s, which is outside %s: %w", name, target, root, ErrLinkNotAllowed)
	}
	st, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", target, err)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is a symbolic link to something that isn't a file: %w", name, ErrLinkNotAllowed)
	}
	return target, nil
}

// insideDir reports whether path is strictly inside dir. Both must be clean
// absolute paths.
func insideDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// importLink places a new symbolic link to target at destPath, with the same
// conflict rules as Import.
func importLink(target, destPath string, policy ConflictPolicy, isBetter func() bool) (ImportResult, error) {
	replacing := false
	if destInfo, err := os.Lstat(destPath); err == nil {
		if destInfo.Mode()&fs.ModeSymlink != 0 {
			if have, err := os.Readlink(destPath); err == nil && have == target {
				return ImportResult{DestPath: destPath}, nil // an earlier attempt already made it
			}
		}
		switch policy {
		case ConflictOverwrite:
			replacing = true
		case ConflictOverwriteIfBetter:
			if isBetter == nil || !isBetter() {
				return ImportResult{DestPath: destPath, Skipped: true}, nil
			}
			replacing = true
		default: // ConflictSkip, ConflictAsk
			return ImportResult{DestPath: destPath, Skipped: true}, nil
		}
	} else if !os.IsNotExist(err) {
		return ImportResult{}, fmt.Errorf("stat destination %s: %w", destPath, err)
	}
	if err := fileperms.MkdirAll(filepath.Dir(destPath)); err != nil {
		return ImportResult{}, fmt.Errorf("create destination dir: %w", err)
	}
	if !replacing {
		if err := os.Symlink(target, destPath); err != nil {
			return ImportResult{}, fmt.Errorf("link %s -> %s: %w", destPath, target, err)
		}
		return ImportResult{DestPath: destPath}, nil
	}
	// As with Import: made beside the old file, then renamed over it.
	staging := destPath + stagingSuffix
	_ = os.Remove(staging)
	if err := os.Symlink(target, staging); err != nil {
		return ImportResult{}, fmt.Errorf("link %s -> %s: %w", staging, target, err)
	}
	return finishReplace(staging, destPath, false)
}
