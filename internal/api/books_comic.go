package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/books"
	"github.com/rdborg/mediarium/internal/organizer"
)

// Comics in the reader: a comic is a .cbz (ZIP), .cbr (RAR) or .cb7 (7z) of
// pictures, but the name often lies (plenty of .cbr files are ZIPs), so the
// format is read from the file's first bytes. A ZIP is read straight from the
// file, one picture at a time. A RAR or 7z can't be read at random, so it is
// unpacked once into the cache folder (with the same safety checks as a
// download) and its pictures served from there.

const (
	maxComicPicture = 60 << 20 // one page, in bytes
	maxComicUnpack  = 4 << 30  // everything in one unpacked comic
	maxComicEntries = 20_000   // files in one comic
	comicCacheAge   = 21 * 24 * time.Hour
	comicListCount  = 64 // comics whose page lists are kept in memory
)

// comicCacheLimit is how much all unpacked comics may take together; the
// ones opened longest ago go first.
var comicCacheLimit int64 = 2 << 30

var (
	// comicLocks holds one lock per cache folder: readers share it, and an
	// unpack or a removal has it alone, so a folder is never removed while a
	// page is read from it.
	comicLocks sync.Map // cache key -> *sync.RWMutex
	// comicUnpacks limits how many comics are unpacked at the same time.
	comicUnpacks = make(chan struct{}, 2)
	// comicLists keeps the page lists of recently opened comics, so turning a
	// page doesn't sort every name in the file again.
	comicLists = &comicListCache{max: comicListCount}
)

func comicLock(key string) *sync.RWMutex {
	mu, _ := comicLocks.LoadOrStore(key, &sync.RWMutex{})
	return mu.(*sync.RWMutex)
}

func (s *Server) comicCacheDir() string { return filepath.Join(s.cfg.ConfigDir, "cache", "comics") }

// comicBook is an open comic: its pages in reading order and a way to read one.
type comicBook struct {
	pages []string
	open  func(i int) (io.ReadCloser, error)
	close func()
	rtl   bool
}

func (c *comicBook) Close() {
	if c.close != nil {
		c.close()
	}
}

// comicList is what is kept about a comic between requests.
type comicList struct {
	pages []string
	rtl   bool
}

// comicListCache is a small least-recently-used cache of page lists, keyed by
// the file's path, size and modification time.
type comicListCache struct {
	mu    sync.Mutex
	max   int
	items map[string]comicList
	order []string // least recently used first
}

func (c *comicListCache) get(key string) (comicList, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.items[key]
	if ok {
		c.touch(key)
	}
	return l, ok
}

func (c *comicListCache) put(key string, l comicList) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]comicList{}
	}
	if _, ok := c.items[key]; !ok && len(c.items) >= c.max {
		delete(c.items, c.order[0])
		c.order = c.order[1:]
	}
	c.items[key] = l
	c.touch(key)
}

func (c *comicListCache) touch(key string) {
	if i := slices.Index(c.order, key); i >= 0 {
		c.order = slices.Delete(c.order, i, i+1)
	}
	c.order = append(c.order, key)
}

var (
	errComicTooManyFiles = fmt.Errorf("it has more than %d files in it", maxComicEntries)
	errComicPageTooBig   = errors.New("this page is too big")
)

// openComic opens the comic file of a book.
func (s *Server) openComic(b books.Book) (*comicBook, error) {
	f, info, full, err := s.openEbook(b)
	if err != nil {
		return nil, err
	}
	ext := ebookExt(b)
	if !books.ComicFormat(ext) {
		f.Close()
		return nil, fmt.Errorf("a %s book is not a comic", ext)
	}
	format, ok := organizer.DetectFormat(f)
	if !ok {
		f.Close()
		return nil, errors.New("it isn't a ZIP, RAR or 7z file")
	}
	key := full + "|" + fileSig(info)
	if format == organizer.FormatZip {
		return openComicZip(f, info.Size(), key)
	}
	f.Close()
	dir, release, err := s.unpackedComic(full, info, format)
	if err != nil {
		return nil, err
	}
	list, ok := comicLists.get(key)
	if !ok {
		var names []string
		err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if len(names) >= maxComicEntries {
				return errComicTooManyFiles
			}
			if rel, rerr := filepath.Rel(dir, p); rerr == nil {
				names = append(names, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			release()
			return nil, err
		}
		list = comicList{pages: books.ComicPages(names)}
		comicLists.put(key, list)
	}
	return &comicBook{
		pages: list.pages,
		open: func(i int) (io.ReadCloser, error) {
			f, err := os.Open(filepath.Join(dir, filepath.FromSlash(list.pages[i])))
			if err != nil {
				return nil, err
			}
			if st, err := f.Stat(); err != nil || st.Size() > maxComicPicture {
				f.Close()
				return nil, errComicPageTooBig
			}
			return f, nil
		},
		close: release,
	}, nil
}

// openComicZip reads a ZIP comic from f, which is closed with the comic.
func openComicZip(f *os.File, size int64, key string) (*comicBook, error) {
	zr, err := zip.NewReader(f, size)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("open the comic: %w", err)
	}
	if len(zr.File) > maxComicEntries {
		f.Close()
		return nil, errComicTooManyFiles
	}
	byName := make(map[string]*zip.File, len(zr.File))
	for _, zf := range zr.File {
		byName[zf.Name] = zf
	}
	list, ok := comicLists.get(key)
	if !ok {
		names := make([]string, 0, len(zr.File))
		for _, zf := range zr.File {
			names = append(names, zf.Name)
		}
		list = comicList{pages: books.ComicPages(names)}
		if ci, ok := books.ReadComicInfoFrom(zr); ok {
			list.rtl = strings.EqualFold(ci.Manga, "YesAndRightToLeft")
		}
		comicLists.put(key, list)
	}
	return &comicBook{
		pages: list.pages,
		rtl:   list.rtl,
		open: func(i int) (io.ReadCloser, error) {
			zf := byName[list.pages[i]]
			if zf == nil {
				return nil, fs.ErrNotExist
			}
			if zf.UncompressedSize64 > maxComicPicture {
				return nil, errComicPageTooBig
			}
			return zf.Open()
		},
		close: func() { f.Close() },
	}, nil
}

// unpackedComic unpacks a RAR or 7z comic into the cache (once per version of
// the file) and returns the folder, and a release func to call once the pages
// have been read: until then the folder is not removed.
func (s *Server) unpackedComic(full string, info os.FileInfo, format organizer.Format) (string, func(), error) {
	sum := sha256.Sum256([]byte(full + "|" + fileSig(info)))
	key := hex.EncodeToString(sum[:12])
	dir := filepath.Join(s.comicCacheDir(), key)
	done := filepath.Join(dir, ".done")
	mu := comicLock(key)
	// A folder removed between the unpack and the read is unpacked again; a
	// second miss in a row is very unlikely, a third means something is wrong.
	for range 3 {
		mu.RLock()
		if _, err := os.Stat(done); err == nil {
			now := time.Now()
			_ = os.Chtimes(done, now, now) // last opened, for the clean-up
			return dir, mu.RUnlock, nil
		}
		mu.RUnlock()
		if err := s.unpackComic(full, dir, key, format, mu); err != nil {
			return "", nil, err
		}
	}
	return "", nil, errors.New("the comic is busy, try again in a moment")
}

// unpackComic unpacks one comic into dir, holding its lock alone.
func (s *Server) unpackComic(full, dir, key string, format organizer.Format, mu *sync.RWMutex) error {
	comicUnpacks <- struct{}{}
	defer func() { <-comicUnpacks }()
	mu.Lock()
	defer mu.Unlock()
	done := filepath.Join(dir, ".done")
	if _, err := os.Stat(done); err == nil {
		return nil // unpacked by another request in the meantime
	}
	_ = os.RemoveAll(dir)
	ex := organizer.NewExtractor()
	ex.MaxTotalBytes = maxComicUnpack
	if err := ex.ExtractAs(full, dir, format); err != nil {
		_ = os.RemoveAll(dir)
		if errors.Is(err, organizer.ErrPasswordProtected) {
			return errors.New("this comic is password protected")
		}
		return fmt.Errorf("unpack the comic: %w", err)
	}
	if err := os.WriteFile(done, nil, 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("unpack the comic: %w", err)
	}
	pruneComicCache(s.comicCacheDir(), key, comicCacheLimit)
	return nil
}

// pruneComicCache removes unpacked comics nobody opened in a few weeks, then,
// while the cache holds more than limit bytes, the ones opened longest ago.
// keep (the comic just unpacked) and comics being read right now are never
// removed, and neither is a folder being unpacked; one left half-unpacked by a
// crash is.
func pruneComicCache(root, keep string, limit int64) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type unpacked struct {
		name string
		used time.Time
		size int64
	}
	var (
		list  []unpacked
		total int64
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		size := folderSize(dir)
		total += size
		if e.Name() == keep {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, ".done"))
		if err != nil {
			// Being unpacked (its lock is taken), or left half-done by a crash.
			if mu := comicLock(e.Name()); mu.TryLock() {
				_ = os.RemoveAll(dir)
				mu.Unlock()
				total -= size
			}
			continue
		}
		list = append(list, unpacked{e.Name(), st.ModTime(), size})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].used.Before(list[j].used) })
	for _, u := range list {
		if time.Since(u.used) <= comicCacheAge && total <= limit {
			break
		}
		mu := comicLock(u.name)
		if !mu.TryLock() {
			continue // being read
		}
		_ = os.RemoveAll(filepath.Join(root, u.name))
		mu.Unlock()
		total -= u.size
	}
}

// folderSize adds up the sizes of the files in dir.
func folderSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// openComicPage opens page i of a comic and checks from its first bytes that
// it is a picture a browser can show. The reader gives at most
// maxComicPicture bytes; call closePage when done.
func openComicPage(c *comicBook, i int) (page io.Reader, closePage func() error, contentType string, err error) {
	rc, err := c.open(i)
	if err != nil {
		return nil, nil, "", err
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(rc, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		rc.Close()
		return nil, nil, "", err
	}
	ct := http.DetectContentType(head[:n])
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return io.LimitReader(io.MultiReader(bytes.NewReader(head[:n]), rc), maxComicPicture), rc.Close, ct, nil
	}
	rc.Close()
	return nil, nil, "", fmt.Errorf("page %d is not a picture", i+1)
}

// comicPicture reads page i of a comic into memory.
func comicPicture(c *comicBook, i int) ([]byte, string, error) {
	page, closePage, ct, err := openComicPage(c, i)
	if err != nil {
		return nil, "", err
	}
	defer closePage()
	data, err := io.ReadAll(page)
	if err != nil {
		return nil, "", err
	}
	return data, ct, nil
}

// comicCoverSource is the first page of a comic, for its cover.
func (s *Server) comicCoverSource(b books.Book) ([]byte, error) {
	c, err := s.openComic(b)
	if err != nil {
		return nil, books.ErrNoCover
	}
	defer c.Close()
	if len(c.pages) == 0 {
		return nil, books.ErrNoCover
	}
	data, _, err := comicPicture(c, 0)
	if err != nil {
		return nil, books.ErrNoCover
	}
	return data, nil
}

type comicAnswer struct {
	Pages  int    `json:"pages"`
	Manga  bool   `json:"rtl,omitempty"` // read from right to left
	Format string `json:"format"`
}

// handleComic says how many pages the comic of a book has.
func (s *Server) handleComic(w http.ResponseWriter, r *http.Request) {
	b, ok := s.bookFromPath(w, r)
	if !ok {
		return
	}
	c, ok := s.openComicOrFail(w, b)
	if !ok {
		return
	}
	defer c.Close()
	writeJSON(w, http.StatusOK, comicAnswer{Pages: len(c.pages), Manga: c.rtl, Format: ebookExt(b)})
}

// handleComicPage serves one page (n counts from 1).
func (s *Server) handleComicPage(w http.ResponseWriter, r *http.Request) {
	b, ok := s.bookFromPath(w, r)
	if !ok {
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		writeError(w, http.StatusBadRequest, "That isn't a page number.")
		return
	}
	c, ok := s.openComicOrFail(w, b)
	if !ok {
		return
	}
	defer c.Close()
	if n > len(c.pages) {
		writeError(w, http.StatusNotFound, "This comic has no such page.")
		return
	}
	page, closePage, ct, err := openComicPage(c, n-1)
	if err != nil {
		slog.Info("comics: page not readable", "book", b.ID, "page", n, "err", err)
		writeError(w, http.StatusUnprocessableEntity, "This page can't be shown.")
		return
	}
	defer closePage()
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Cache-Control", "private, max-age=86400")
	// Streamed rather than read into memory first: a page can be tens of
	// megabytes, and the reader asks for a few at a time.
	if _, err := io.Copy(w, page); err != nil {
		slog.Debug("comics: page cut short", "book", b.ID, "page", n, "err", err)
	}
}

func (s *Server) openComicOrFail(w http.ResponseWriter, b books.Book) (*comicBook, bool) {
	if !hasEbook(b) || !books.ComicFormat(ebookExt(b)) {
		writeError(w, http.StatusNotFound, "This book is not a comic.")
		return nil, false
	}
	c, err := s.openComic(b)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "Couldn't find the file. It may have been moved or deleted.")
			return nil, false
		}
		writeError(w, http.StatusUnprocessableEntity, "This comic couldn't be opened: "+problemTextOf(err))
		return nil, false
	}
	return c, true
}

// problemTextOf is an error's text without the paths around it.
func problemTextOf(err error) string {
	text := err.Error()
	if i := strings.LastIndex(text, ": "); i >= 0 && strings.Contains(text[:i], string(filepath.Separator)) {
		text = text[i+2:]
	}
	return strings.TrimSpace(text)
}

// handleBookFileCover serves the cover of a book taken from its own file (the
// first page of a comic).
func (s *Server) handleBookFileCover(w http.ResponseWriter, r *http.Request) {
	b, ok := s.bookFromPath(w, r)
	if !ok {
		return
	}
	data, err := s.ebookCover(r.Context(), b, 400, 600)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}
