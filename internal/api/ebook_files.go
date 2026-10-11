package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/books"
	"github.com/rdborg/mediarium/internal/mediafiles"
)

// What the e-reader routes (OPDS and Kobo sync) share: which books an
// e-reader can have, how to open a book's file safely, and the cover pictures.

// ebookExt is the file kind of the book: "epub", "azw3", "pdf"...
func ebookExt(b books.Book) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(b.EbookPath)), ".")
}

// hasEbook reports whether the book has an ebook file on record.
func hasEbook(b books.Book) bool {
	return b.EbookStatus == books.StatusDownloaded && b.EbookPath != ""
}

// koboKind reports whether a Kobo can be given this file: EPUBs, and Kindle
// books once they are turned into EPUB. PDFs stay out (a Kobo reads those from
// a cable, not through sync).
func koboKind(ext string) bool { return ext == "epub" || kindleFormat(ext) }

// ebookBooks lists the books that have an ebook, oldest first by number.
func (s *Server) ebookBooks() ([]books.Book, error) {
	all, err := s.BookRepo.List()
	if err != nil {
		return nil, err
	}
	out := make([]books.Book, 0, len(all))
	for _, b := range all {
		if hasEbook(b) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// openEbook opens a book's file the way the reader does: it has to be inside
// the ebooks folder.
func (s *Server) openEbook(b books.Book) (*os.File, os.FileInfo, string, error) {
	if b.EbookPath == "" {
		return nil, nil, "", fs.ErrNotExist
	}
	folder, err := mediafiles.ForFile(b.EbookPath, []string{s.ebooksRoot()}, false)
	if err != nil {
		return nil, nil, "", err
	}
	rel, ok := folder.Rel(b.EbookPath)
	if !ok {
		return nil, nil, "", mediafiles.ErrOutside
	}
	f, info, err := folder.Open(rel)
	if err != nil {
		return nil, nil, "", err
	}
	return f, info, filepath.Join(folder.Dir, filepath.FromSlash(rel)), nil
}

// epubOf returns the path of an EPUB version of the book's file: the file
// itself, or the cached conversion of a Kindle book.
func (s *Server) epubOf(b books.Book) (string, error) {
	f, info, full, err := s.openEbook(b)
	if err != nil {
		return "", err
	}
	f.Close()
	ext := ebookExt(b)
	switch {
	case ext == "epub":
		return full, nil
	case kindleFormat(ext):
		return s.convertedEPUB(full, info)
	}
	return "", fmt.Errorf("a %s book is not an EPUB", ext)
}

// fileSig says which version of the book's file this is (size and date).
func fileSig(info os.FileInfo) string {
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().Unix())
}

func (s *Server) ereaderCoverDir() string { return filepath.Join(s.cfg.ConfigDir, "cache", "covers") }

// A cover is cut down to one of these boxes, so a library does not fill the
// cache with a size for every device.
var coverBoxes = [][2]int{{200, 300}, {400, 600}, {800, 1200}, {1600, 2400}}

func coverBox(wantW, wantH int) (int, int) {
	for _, b := range coverBoxes {
		if (wantH > 0 && b[1] >= wantH) || (wantH <= 0 && wantW > 0 && b[0] >= wantW) {
			return b[0], b[1]
		}
	}
	last := coverBoxes[len(coverBoxes)-1]
	return last[0], last[1]
}

// ebookCover returns the book's cover as a JPEG no bigger than the box that
// fits wantW by wantH. It comes from the book's own file when that has one,
// otherwise from Open Library. books.ErrNoCover means there is none.
func (s *Server) ebookCover(ctx context.Context, b books.Book, wantW, wantH int) ([]byte, error) {
	bw, bh := coverBox(wantW, wantH)
	var stamp string
	if f, info, _, err := s.openEbook(b); err == nil {
		stamp = fileSig(info)
		f.Close()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%d|%dx%d", b.ID, b.EbookPath, stamp, b.CoverID, bw, bh)))
	cached := filepath.Join(s.ereaderCoverDir(), hex.EncodeToString(sum[:14])+".jpg")
	if data, err := os.ReadFile(cached); err == nil && len(data) > 0 {
		return data, nil
	}
	src, err := s.coverSource(ctx, b)
	if err != nil {
		return nil, err
	}
	data, err := books.ResizeCover(src, bw, bh)
	if err != nil {
		slog.Info("ereader: cover not usable", "book", b.ID, "err", err)
		return nil, books.ErrNoCover
	}
	s.maybePruneEReaderCovers()
	if err := os.MkdirAll(s.ereaderCoverDir(), 0o755); err == nil {
		tmp, err := os.CreateTemp(s.ereaderCoverDir(), "cover-*.tmp")
		if err == nil {
			_, werr := tmp.Write(data)
			cerr := tmp.Close()
			if werr == nil && cerr == nil {
				if os.Rename(tmp.Name(), cached) != nil {
					_ = os.Remove(tmp.Name())
				}
			} else {
				_ = os.Remove(tmp.Name())
			}
		}
	}
	return data, nil
}

// coverSource finds the largest picture there is for a book.
func (s *Server) coverSource(ctx context.Context, b books.Book) ([]byte, error) {
	fromOpenLibrary := func() ([]byte, error) {
		if b.CoverID <= 0 {
			return nil, books.ErrNoCover
		}
		path, err := s.cachedBookCover(ctx, b.CoverID, "L")
		if err != nil {
			if !errors.Is(err, books.ErrNotFound) {
				slog.Info("ereader: Open Library cover", "book", b.ID, "err", err)
			}
			return nil, books.ErrNoCover
		}
		return os.ReadFile(path)
	}
	fromFile := func(allowConvert bool) ([]byte, error) {
		if !allowConvert && kindleFormat(ebookExt(b)) {
			return nil, books.ErrNoCover
		}
		epub, err := s.epubOf(b)
		if err != nil {
			return nil, books.ErrNoCover
		}
		data, _, err := books.EPUBCover(epub)
		if err != nil {
			return nil, books.ErrNoCover
		}
		return data, nil
	}
	// An EPUB's own cover first: it is the cover of this very file and needs
	// no network. A Kindle book is only opened for it when Open Library has none.
	if kindleFormat(ebookExt(b)) {
		if data, err := fromOpenLibrary(); err == nil {
			return data, nil
		}
		return fromFile(true)
	}
	if data, err := fromFile(true); err == nil {
		return data, nil
	}
	return fromOpenLibrary()
}

// coverAge is how long a cover nobody asked for stays in the cache.
const coverAge = 180 * 24 * time.Hour

// maybePruneEReaderCovers clears out old covers at most once a day.
func (s *Server) maybePruneEReaderCovers() {
	rt := s.ereaderState()
	now := time.Now().Unix()
	last := rt.lastPrune.Load()
	if now-last > 24*3600 && rt.lastPrune.CompareAndSwap(last, now) {
		s.pruneEReaderCovers()
	}
}

// pruneEReaderCovers removes covers nobody asked for in a long time.
func (s *Server) pruneEReaderCovers() {
	entries, err := os.ReadDir(s.ereaderCoverDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() && time.Since(info.ModTime()) > coverAge {
			_ = os.Remove(filepath.Join(s.ereaderCoverDir(), e.Name()))
		}
	}
}
