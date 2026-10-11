package download

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// UnsafeExtensions are the file endings that mean a release carries a program
// or a script instead of (or next to) the video, which is how fake releases
// spread malware: the .exe is meant to be opened by someone who thinks it is a
// codec or a "player". The list follows what Sonarr and Radarr treat as
// executable or dangerous (.exe .bat .cmd .sh, and .lnk .scr .vbs .ps1 .arj
// .lzh .zipx), plus the other Windows and script endings that turn up in
// those releases. A real movie or episode never has any of them.
//
// ".com" and ".url" are left out on purpose: files named after a web site
// ("www.example.com") and internet shortcuts come with plenty of honest
// releases.
//
// Written lower case with the dot.
var UnsafeExtensions = []string{
	".exe", ".scr", ".pif", ".msi", ".msp", ".bat", ".cmd", ".lnk",
	".vbs", ".vbe", ".js", ".jse", ".wsf", ".wsh", ".ps1", ".psm1", ".hta", ".cpl",
	".jar", ".sh", ".reg", ".scf", ".inf", ".gadget", ".application", ".msc", ".dll",
	// Archive formats Usenet and torrent clients cannot open, which is why
	// they are used to hide a payload.
	".arj", ".lzh", ".zipx",
}

var unsafeSet = func() map[string]bool {
	m := make(map[string]bool, len(UnsafeExtensions))
	for _, e := range UnsafeExtensions {
		m[e] = true
	}
	return m
}()

// UnsafeExtension reports whether a file name ends in one of the
// UnsafeExtensions, and which one. Trailing dots and spaces are ignored (the
// system drops them, so "setup.exe. " is still setup.exe), and so is case. A
// name with several endings is judged by its last one, so "film.mkv.exe" is
// caught and "film.exe.mkv" is not.
func UnsafeExtension(name string) (string, bool) {
	name = strings.TrimRight(strings.TrimSpace(name), ". ")
	// Take the last path element; names from a post or an archive can carry
	// folders written either way.
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext != "" && unsafeSet[ext] {
		return ext, true
	}
	return "", false
}

// UnsafeFilesError is the error for a release that holds programs or scripts.
// It is the release's fault, so callers blocklist it.
type UnsafeFilesError struct {
	Names []string // up to a few of the offending file names
	Total int      // how many there are in all
	After bool     // found after unpacking, not in the NZB's file list
}

func (e *UnsafeFilesError) Error() string {
	var list string
	switch {
	case len(e.Names) == 0:
	case e.Total > len(e.Names):
		list = fmt.Sprintf(" (%s and %d more)", strings.Join(e.Names, ", "), e.Total-len(e.Names))
	default:
		list = " (" + strings.Join(e.Names, ", ") + ")"
	}
	if e.After {
		return "the unpacked download contains programs or scripts" + list + ", so Mediarium did not import it"
	}
	return "the release contains programs or scripts" + list + ", so Mediarium did not download it"
}

// maxListedUnsafe is how many offending names are put into the message.
const maxListedUnsafe = 3

func newUnsafeFilesError(names []string, after bool) *UnsafeFilesError {
	e := &UnsafeFilesError{Total: len(names), After: after}
	e.Names = append(e.Names, names...)
	if len(e.Names) > maxListedUnsafe {
		e.Names = e.Names[:maxListedUnsafe]
	}
	return e
}

// UnsafeInNZB returns the error to stop an NZB whose file list names a
// program or a script, or nil when it names none. The names come from the
// posts' subjects (after the same clean-up the download itself applies), so it
// can be called before anything is downloaded. A release with random file
// names shows nothing here; it is checked again after unpacking.
func UnsafeInNZB(n *NZB) error {
	return UnsafeInNames(outputFileNames(n.Files))
}

// FileNames returns the names the NZB's files would be saved under.
func (n *NZB) FileNames() []string { return outputFileNames(n.Files) }

// UnsafeInNames is UnsafeInNZB for any list of file names before download,
// such as the files a .torrent file lists.
func UnsafeInNames(names []string) error {
	var bad []string
	for _, name := range names {
		if _, ok := UnsafeExtension(name); ok {
			bad = append(bad, name)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return newUnsafeFilesError(bad, false)
}

// UnsafeInDir walks a finished (unpacked) download and returns the error to
// stop it when any file in it is a program or a script, or nil. Symbolic links
// are judged by their own name and never followed. The downloader's own
// bookkeeping files (names starting ".mediarium-") are skipped.
func UnsafeInDir(dir string) error {
	var bad []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".mediarium-") {
			return nil
		}
		if _, ok := UnsafeExtension(d.Name()); ok {
			rel, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				rel = d.Name()
			}
			bad = append(bad, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("look for programs in the download: %w", err)
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return newUnsafeFilesError(bad, true)
}

// Fingerprint is a hash of the articles an NZB lists, independent of the
// order of its files and segments and of everything around them (the name,
// the posting group, the extra data an indexer adds). Two NZBs with the same
// fingerprint download the same bytes. SABnzbd tells duplicate NZBs apart the
// same way: by a combined hash of the article ids.
func (n *NZB) Fingerprint() string {
	var ids []string
	for _, f := range n.Files {
		for _, s := range f.Segments {
			ids = append(ids, strings.TrimSuffix(strings.TrimPrefix(s.MessageID, "<"), ">"))
		}
	}
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
