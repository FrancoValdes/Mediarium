package api

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pgaskin/kepubify/v4/kepub"
	"github.com/rdborg/mediarium/internal/books"
)

// A Kobo reads its own book format, KEPUB: an EPUB with extra markup that
// gives it page numbers, reading position and statistics. Mediarium makes the
// KEPUB copy the first time a Kobo downloads a book (with kepubify) and keeps
// it in /config/cache/kepub. The library itself is never changed. A Kindle book
// is turned into an EPUB first (books_convert.go), then into a KEPUB.

// kepubRevision changes when the way books are converted does, so old copies
// are made again.
const kepubRevision = "1"

// kepubMaxAge is how long a copy nobody downloads is kept.
const kepubMaxAge = 90 * 24 * time.Hour

func (s *Server) kepubDir() string { return filepath.Join(s.cfg.ConfigDir, "cache", "kepub") }

// kepubFile returns the KEPUB copy of the book's file, making it when there
// is none for this version of the file yet.
func (s *Server) kepubFile(ctx context.Context, b books.Book) (string, error) {
	epubPath, err := s.epubOf(b)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(epubPath)
	if err != nil {
		return "", fmt.Errorf("find the book: %w", err)
	}
	real := epubPath
	if r, err := filepath.EvalSymlinks(epubPath); err == nil {
		real = r
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%s", real, info.Size(), info.ModTime().UnixNano(), kepubRevision)))
	out := filepath.Join(s.kepubDir(), hex.EncodeToString(sum[:12])+".kepub.epub")
	if _, err := os.Stat(out); err == nil {
		now := time.Now()
		_ = os.Chtimes(out, now, now) // still in use
		return out, nil
	}
	rt := s.ereaderState()
	rt.kepubMu.Lock()
	defer rt.kepubMu.Unlock()
	if _, err := os.Stat(out); err == nil {
		return out, nil // made while this call waited
	}
	if info.Size() > 300<<20 {
		return "", fmt.Errorf("the book is too big to convert (%d bytes)", info.Size())
	}
	if err := os.MkdirAll(s.kepubDir(), 0o755); err != nil {
		return "", fmt.Errorf("make the cache folder: %w", err)
	}
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return "", fmt.Errorf("open the book: %w", err)
	}
	defer zr.Close()
	// Only the compressed size is known so far. A few kilobytes can unpack to
	// gigabytes, so the sizes the file declares are checked before any of it is
	// read; the zip reader itself refuses an entry that turns out longer than it
	// declared.
	if err := checkEpubZip(zr.File, kepubZipLimits); err != nil {
		return "", err
	}
	// Making the copy takes a minute for a big book, not much more.
	ctx, cancel := context.WithTimeout(ctx, kepubTimeout)
	defer cancel()
	tmp, err := os.CreateTemp(s.kepubDir(), "converting-*.epub")
	if err != nil {
		return "", fmt.Errorf("make the KEPUB: %w", err)
	}
	started := time.Now()
	cerr := convertKepub(ctx, tmp, &zr.Reader)
	if err := tmp.Close(); err != nil && cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = os.Remove(tmp.Name())
		return "", cerr
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("save the KEPUB: %w", err)
	}
	slog.Info("ereader: made a KEPUB for a Kobo", "book", b.ID, "took", time.Since(started).Round(time.Millisecond))
	s.pruneKepub()
	return out, nil
}

// kepubTimeout is the longest a conversion may run.
const kepubTimeout = 10 * time.Minute

// zipLimits are the most an EPUB may unpack to before it is not converted.
type zipLimits struct {
	entries int    // files in the archive
	total   uint64 // all declared sizes together
	entry   uint64 // one file
	markup  uint64 // one page, package or style file: kepubify holds each of these in memory, several at once
}

// kepubZipLimits suit real books (the biggest are a few hundred megabytes of
// pictures, and the biggest single chapter a few megabytes) and stop a zip bomb.
var kepubZipLimits = zipLimits{entries: 20000, total: 1 << 30, entry: 512 << 20, markup: 32 << 20}

// checkEpubZip refuses an archive whose declared sizes are beyond limits.
func checkEpubZip(files []*zip.File, limits zipLimits) error {
	if len(files) > limits.entries {
		return fmt.Errorf("the book has too many files in it (%d)", len(files))
	}
	var total uint64
	for _, f := range files {
		size := f.UncompressedSize64
		limit := limits.entry
		switch strings.ToLower(filepath.Ext(f.Name)) {
		case ".xhtml", ".html", ".htm", ".xml", ".opf", ".ncx":
			limit = min(limit, limits.markup)
		}
		if size > limit {
			return fmt.Errorf("%q unpacks to %d bytes, which is too big", f.Name, size)
		}
		total += size
		if total > limits.total {
			return fmt.Errorf("the book unpacks to more than %d bytes", limits.total)
		}
	}
	return nil
}

// convertKepub runs kepubify. A book is written by strangers, so a file broken
// in a way kepubify did not foresee fails the conversion; it never takes
// Mediarium down.
func convertKepub(ctx context.Context, w *os.File, zr *zip.Reader) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("convert to KEPUB: %v", r)
		}
	}()
	if err := kepub.NewConverter().Convert(ctx, w, zr); err != nil {
		return fmt.Errorf("convert to KEPUB: %w", err)
	}
	return nil
}

// pruneKepub removes copies nobody downloaded for a long time.
func (s *Server) pruneKepub() {
	entries, err := os.ReadDir(s.kepubDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		if time.Since(info.ModTime()) > kepubMaxAge || (strings.HasPrefix(e.Name(), "converting-") && time.Since(info.ModTime()) > time.Hour) {
			_ = os.Remove(filepath.Join(s.kepubDir(), e.Name()))
		}
	}
}
