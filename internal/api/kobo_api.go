package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rdborg/mediarium/internal/auth"
	"github.com/rdborg/mediarium/internal/books"
	"github.com/rdborg/mediarium/internal/ereader"
	"github.com/rdborg/mediarium/internal/kobo"
)

// Kobo sync. A Kobo e-reader can be pointed at a different "store" address
// (api_endpoint in its settings file). Pointed at /kobo/<token>, it asks
// Mediarium which ebooks it should have, downloads them as KEPUB, and reports
// where the reader is in each, which Mediarium keeps with the rest of the
// account's reading progress (so Mediarium Books opens at the same place).
// The token in the address is the account's secret: it is found by hash,
// compared in constant time, never logged, and wrong guesses are counted.
//
// Everything the Kobo asks for that Mediarium does not do itself (signing in,
// its store pages, books bought there) is passed on to Kobo's own servers
// (kobo_store.go).

const (
	koboSyncLimit   = 100 // books or reading states in one sync answer
	koboStateMax    = 64 << 10
	koboUnauthorize = `{"error":"unknown token"}`
)

// koboAccess is one signed-in Kobo request.
type koboAccess struct {
	user  *auth.User
	token string // the secret; never logged
	base  string // this server's address with the token: "https://host/kobo/<token>"
}

func (s *Server) koboRoutes(root *http.ServeMux) {
	root.Handle("/kobo/", noStore(http.HandlerFunc(s.handleKobo)))
}

func (s *Server) handleKobo(w http.ResponseWriter, r *http.Request) {
	token, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/kobo/"), "/")
	rest = "/" + rest
	rt := s.ereaderState()
	if s.ereaderRateLimited(w, r) {
		return
	}
	ipKey := s.ereaderLimitKey(r)
	userID, err := s.EReader.UserForKobo(token)
	if err != nil {
		if errors.Is(err, ereader.ErrNotFound) {
			rt.failIP.RecordFailure(ipKey)
			// Neither the token nor the address it came from goes in the log.
			slog.Warn("kobo: request with an unknown token", "ip", s.clientIP(r))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, koboUnauthorize)
			return
		}
		slog.Error("kobo: look up token", "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't check the address.")
		return
	}
	user, err := s.Auth.GetUser(userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "This account can't be found.")
		return
	}
	if ok, why := s.ereaderUserAllowed(user.ID); !ok {
		writeError(w, http.StatusForbidden, why)
		return
	}
	k := koboAccess{user: user, token: token, base: s.publicBase(r) + s.cfg.URLBase + "/kobo/" + token}

	switch {
	case rest == "/v1/initialization" && r.Method == http.MethodGet:
		s.koboInitialization(w, r, k)
	case rest == "/v1/library/sync" && r.Method == http.MethodGet:
		s.koboSync(w, r, k)
	case strings.HasPrefix(rest, "/v1/library/tags"):
		s.koboForward(w, r, rest) // collections are the Kobo's own
	case strings.HasPrefix(rest, "/v1/library/"):
		s.koboLibraryItem(w, r, k, rest)
	case strings.HasPrefix(rest, "/download/"):
		s.koboDownload(w, r, rest)
	case strings.HasSuffix(rest, "/image.jpg"):
		s.koboCover(w, r, rest)
	case (rest == "/v1/auth/device" || rest == "/v1/auth/refresh") && r.Method == http.MethodPost:
		s.koboAuth(w, r, rest)
	default:
		s.koboForward(w, r, rest)
	}
}

// ---- Initialization ----

func (s *Server) koboInitialization(w http.ResponseWriter, r *http.Request, k koboAccess) {
	res := kobo.Resources(k.base, s.koboStoreResources(r))
	w.Header().Set(kobo.APITokenHeader, kobo.APITokenValue)
	writeJSON(w, http.StatusOK, map[string]any{"Resources": res})
}

// koboAuth passes the sign-in calls on to the store (so the Kobo keeps real
// tokens); when the store can't be reached the Kobo gets tokens that work
// here.
func (s *Server) koboAuth(w http.ResponseWriter, r *http.Request, path string) {
	body := readKoboBody(r)
	resp, err := s.koboStoreCall(r.Context(), r, path, body, 25*time.Second)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode < 500 {
			for k, vs := range resp.Header {
				if koboSkipHeaders[http.CanonicalHeaderKey(k)] {
					continue
				}
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
			return
		}
	}
	koboAuthFallback(w, body)
}

// ---- Library items: metadata, reading state, removal ----

func (s *Server) koboLibraryItem(w http.ResponseWriter, r *http.Request, k koboAccess, rest string) {
	tail := strings.TrimPrefix(rest, "/v1/library/")
	idPart, action, _ := strings.Cut(tail, "/")
	ids := strings.Split(idPart, ",")
	first, ours := kobo.ParseBookUUID(ids[0])
	if !ours {
		s.koboForward(w, r, rest) // a book bought in the Kobo store
		return
	}
	switch {
	case action == "metadata" && r.Method == http.MethodGet:
		s.koboMetadata(w, r, k, ids)
	case action == "state" && r.Method == http.MethodGet:
		s.koboGetState(w, k, first)
	case action == "state" && r.Method == http.MethodPut:
		s.koboPutState(w, r, k, first)
	case action == "" && r.Method == http.MethodDelete:
		if err := s.EReader.Archive(k.user.ID, first); err != nil {
			slog.Error("kobo: archive book", "err", err)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeKoboEmpty(w)
	}
}

// koboBookFor loads a book the Kobo may have.
func (s *Server) koboBookFor(id int64) (books.Book, bool) {
	b, err := s.BookRepo.Get(id)
	if err != nil || !hasEbook(b) || !koboKind(ebookExt(b)) {
		return books.Book{}, false
	}
	return b, true
}

func koboAuthors(b books.Book) []string {
	if strings.TrimSpace(b.Author) == "" {
		return nil
	}
	return []string{strings.TrimSpace(b.Author)}
}

// koboBookInfo describes a book to the Kobo. size and modified come from the
// file; they are zero when it can't be read right now.
func (s *Server) koboBookInfo(k koboAccess, b books.Book) kobo.Book {
	info := kobo.Book{
		ID: b.ID, Title: b.Title, Authors: koboAuthors(b), Description: b.Description, Year: b.Year,
		Added: parseAdded(b), SeriesName: b.SeriesName, SeriesPos: b.SeriesPosition,
		DownloadURL: k.base + "/download/" + kobo.BookUUID(b.ID) + "/kepub",
	}
	if st, err := os.Stat(b.EbookPath); err == nil {
		info.Size = st.Size()
		info.Modified = st.ModTime()
	}
	return info
}

func (s *Server) koboMetadata(w http.ResponseWriter, r *http.Request, k koboAccess, ids []string) {
	out := []kobo.Metadata{}
	for _, idText := range ids {
		id, ok := kobo.ParseBookUUID(idText)
		if !ok {
			continue
		}
		if b, ok := s.koboBookFor(id); ok {
			out = append(out, kobo.BookMetadata(s.koboBookInfo(k, b)))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// koboState builds the reading state the Kobo is given for a book.
func koboState(b books.Book, p books.Progress, hasP bool, st ereader.State, hasSt bool) kobo.ReadingState {
	created := parseAdded(b)
	modified := created
	if hasP {
		if t, ok := kobo.ParseTime(p.UpdatedAt); ok {
			modified = t
		}
	}
	ts := kobo.FormatTime(modified)
	// The Kobo's own details (exact place, time spent) stay valid only while
	// the progress is the one it reported; if Mediarium Books has moved on,
	// only the percentage and the status are known.
	own := hasP && hasSt && st.DeviceTS != "" && st.DeviceTS == progressSig(p)
	status := kobo.StatusReadyToRead
	switch {
	case hasP && p.Finished:
		status = kobo.StatusFinished
	case hasP && p.Percent > 0:
		status = kobo.StatusReading
	}
	if own && kobo.ValidStatus(st.Status) {
		status = st.Status
	}
	rs := kobo.ReadingState{
		EntitlementID: kobo.BookUUID(b.ID), Created: kobo.FormatTime(created), LastModified: ts, PriorityTimestamp: ts,
		StatusInfo:      kobo.StatusInfo{LastModified: ts, Status: status},
		Statistics:      kobo.Statistics{LastModified: ts},
		CurrentBookmark: kobo.Bookmark{LastModified: ts},
	}
	if hasSt {
		rs.StatusInfo.TimesStartedReading = st.TimesStarted
		if st.LastStarted != "" {
			rs.StatusInfo.LastTimeStartedReading = st.LastStarted
		}
		if own {
			if st.SpentMinutes > 0 {
				v := st.SpentMinutes
				rs.Statistics.SpentReadingMinutes = &v
			}
			if st.RemainingMinutes > 0 {
				v := st.RemainingMinutes
				rs.Statistics.RemainingTimeMinutes = &v
			}
		}
	}
	if status == kobo.StatusReading && rs.StatusInfo.TimesStartedReading == 0 {
		rs.StatusInfo.TimesStartedReading = 1
	}
	if hasP {
		pct := roundTo(p.Percent, 1)
		rs.CurrentBookmark.ProgressPercent = &pct
		if own {
			if st.ContentPercent != nil {
				v := *st.ContentPercent
				rs.CurrentBookmark.ContentSourceProgressPercent = &v
			}
			if st.LocationValue != "" {
				rs.CurrentBookmark.Location = &kobo.Location{Value: st.LocationValue, Type: st.LocationType, Source: st.LocationSource}
			}
		}
	}
	return rs
}

// progressSig names one version of a reading place. The Kobo is sent a place
// only when it differs from the version it already has; the time alone is not
// enough, since two saves can land in the same second.
func progressSig(p books.Progress) string {
	return fmt.Sprintf("%s|%.1f|%t", p.UpdatedAt, p.Percent, p.Finished)
}

func roundTo(v float64, places int) float64 {
	m := math.Pow(10, float64(places))
	return math.Round(v*m) / m
}

func (s *Server) koboGetState(w http.ResponseWriter, k koboAccess, id int64) {
	b, ok := s.koboBookFor(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown book"})
		return
	}
	p, hasP, err := s.BookRepo.GetProgress(k.user.ID, id, books.Ebook)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't read your place.")
		return
	}
	st, hasSt, err := s.EReader.GetState(k.user.ID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't read your place.")
		return
	}
	writeJSON(w, http.StatusOK, []kobo.ReadingState{koboState(b, p, hasP, st, hasSt)})
}

// koboPutState takes the place the Kobo reports and keeps it with the
// account's other reading progress.
func (s *Server) koboPutState(w http.ResponseWriter, r *http.Request, k koboAccess, id int64) {
	if _, ok := s.koboBookFor(id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown book"})
		return
	}
	var req kobo.StatePut
	r.Body = http.MaxBytesReader(w, r.Body, koboStateMax)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.ReadingStates) == 0 {
		writeError(w, http.StatusBadRequest, "Couldn't read that request.")
		return
	}
	in := req.ReadingStates[0]
	uid := k.user.ID
	p, hasP, err := s.BookRepo.GetProgress(uid, id, books.Ebook)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't save your place.")
		return
	}
	st, _, err := s.EReader.GetState(uid, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't save your place.")
		return
	}
	now := time.Now().UTC()
	st.BookID = id
	st.ModifiedAt = kobo.FormatTime(now)
	newP := books.Progress{BookID: id, Format: books.Ebook, Position: p.Position, Percent: p.Percent, Finished: p.Finished}
	changed := false

	if in.CurrentBookmark != nil {
		bm := in.CurrentBookmark
		if bm.ProgressPercent != nil && !math.IsNaN(*bm.ProgressPercent) {
			pct := math.Max(0, math.Min(100, *bm.ProgressPercent))
			if !hasP || math.Abs(pct-p.Percent) >= 0.05 {
				newP.Percent = pct
				// A place from the Kobo is not an EPUB location Mediarium Books
				// can open; it opens by the percentage instead.
				newP.Position = ""
				changed = true
			}
		}
		if bm.ContentSourceProgressPercent != nil {
			v := *bm.ContentSourceProgressPercent
			st.ContentPercent = &v
		}
		if bm.Location != nil {
			st.LocationValue, st.LocationType, st.LocationSource = clip(bm.Location.Value, 200), clip(bm.Location.Type, 50), clip(bm.Location.Source, 500)
		}
	}
	if in.Statistics != nil {
		if in.Statistics.SpentReadingMinutes != nil {
			st.SpentMinutes = clampInt(*in.Statistics.SpentReadingMinutes)
		}
		if in.Statistics.RemainingTimeMinutes != nil {
			st.RemainingMinutes = clampInt(*in.Statistics.RemainingTimeMinutes)
		}
	}
	if in.StatusInfo != nil && kobo.ValidStatus(in.StatusInfo.Status) {
		status := in.StatusInfo.Status
		if status == kobo.StatusReading && st.Status != kobo.StatusReading {
			st.TimesStarted++
			st.LastStarted = kobo.FormatTime(now)
		}
		st.Status = status
		finished := status == kobo.StatusFinished
		if finished != newP.Finished {
			newP.Finished = finished
			changed = true
		}
		if finished && newP.Percent < 100 {
			newP.Percent = 100 // the place a Kobo reports for a finished book is not a real one
			newP.Position = ""
			changed = true
		}
		if status == kobo.StatusReadyToRead && in.CurrentBookmark == nil && newP.Percent != 0 {
			newP.Percent, newP.Position = 0, ""
			changed = true
		}
	}

	if changed || !hasP && (in.CurrentBookmark != nil || in.StatusInfo != nil) {
		if err := s.BookRepo.SetProgress(uid, newP); err != nil {
			slog.Error("kobo: save progress", "err", err)
			writeError(w, http.StatusInternalServerError, "Couldn't save your place.")
			return
		}
	}
	// Whatever progress is stored now is what the Kobo has: it is not sent back.
	if cur, ok, err := s.BookRepo.GetProgress(uid, id, books.Ebook); err == nil && ok {
		st.DeviceTS = progressSig(cur)
	}
	if err := s.EReader.SaveState(uid, st); err != nil {
		slog.Error("kobo: save state", "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't save your place.")
		return
	}
	writeJSON(w, http.StatusOK, kobo.StateUpdateResult(id, in.CurrentBookmark != nil, in.Statistics != nil, in.StatusInfo != nil, now))
}

func clampInt(f float64) int {
	if math.IsNaN(f) || f < 0 {
		return 0
	}
	if f > 1e7 {
		return 1e7
	}
	return int(f)
}

// ---- Sync ----

func (s *Server) koboSync(w http.ResponseWriter, r *http.Request, k koboAccess) {
	uid := k.user.ID
	header := r.Header.Get(kobo.TokenHeader)
	tok := kobo.ParseSyncToken(header)
	if strings.TrimSpace(header) == "" {
		// A Kobo with no token has nothing yet (new, or reset): send it all.
		if err := s.EReader.ResetKobo(uid); err != nil {
			slog.Error("kobo: start over", "err", err)
		}
	}

	list, err := s.ebookBooks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't read the library.")
		return
	}
	lib, err := s.EReader.Library(uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't read what was sent.")
		return
	}
	states, err := s.EReader.States(uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't read what was sent.")
		return
	}
	prog, err := s.BookRepo.ListProgress(uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't read your places.")
		return
	}
	progress := map[int64]books.Progress{}
	for _, p := range prog {
		if p.Format == books.Ebook {
			progress[p.BookID] = p
		}
	}

	now := time.Now().UTC()
	entries := []any{}
	count := 0
	more := false
	eligible := map[int64]books.Book{}
	var syncable []books.Book
	for _, b := range list {
		if koboKind(ebookExt(b)) {
			eligible[b.ID] = b
			syncable = append(syncable, b)
		}
	}

	// 1. Books the Kobo has that Mediarium no longer has an ebook for.
	var gone []int64
	for id, e := range lib {
		if _, still := eligible[id]; !still && !e.Archived {
			gone = append(gone, id)
		}
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i] < gone[j] })
	for _, id := range gone {
		if count >= koboSyncLimit {
			more = true
			break
		}
		e := lib[id]
		b := kobo.Book{ID: id, Title: e.Title, Added: now, Removed: true}
		entries = append(entries, kobo.ChangedEntitlement(kobo.BookEntitlement(b, now), kobo.BookMetadata(b), nil))
		_ = s.EReader.Forget(uid, id)
		delete(lib, id)
		count++
	}

	// 2. Books the Kobo does not have yet (or has an older file of).
	sent := map[int64]bool{}
	for _, b := range syncable {
		if more {
			break
		}
		e, has := lib[b.ID]
		if has && e.Archived {
			continue
		}
		st, err := os.Stat(b.EbookPath)
		if err != nil {
			continue // the file can't be read right now (a drive that is off); do not remove or send it
		}
		sig := fileSig(st)
		if has && e.FileSig == sig {
			continue
		}
		if count >= koboSyncLimit {
			more = true
			break
		}
		info := s.koboBookInfo(k, b)
		ent, meta := kobo.BookEntitlement(info, now), kobo.BookMetadata(info)
		var rs *kobo.ReadingState
		if p, ok := progress[b.ID]; ok {
			state := states[b.ID]
			v := koboState(b, p, true, state, state.BookID != 0)
			rs = &v
		}
		if has {
			entries = append(entries, kobo.ChangedEntitlement(ent, meta, rs))
		} else {
			entries = append(entries, kobo.NewEntitlement(ent, meta, rs))
		}
		if err := s.EReader.MarkSynced(uid, b.ID, b.Title, sig); err != nil {
			slog.Error("kobo: record sent book", "err", err)
		}
		lib[b.ID] = ereader.LibEntry{BookID: b.ID, Title: b.Title, FileSig: sig}
		sent[b.ID] = true
		if rs != nil {
			_ = s.EReader.SetDeviceTS(uid, b.ID, progressSig(progress[b.ID]))
			delete(progress, b.ID)
		}
		if kindleFormat(ebookExt(b)) {
			s.convertLater(b) // so the download is ready when the Kobo asks
		}
		count++
	}

	// 3. Places read in Mediarium (or changed another way) since the Kobo last heard.
	if !more {
		ids := make([]int64, 0, len(progress))
		for id := range progress {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			e, onKobo := lib[id]
			b, ok := eligible[id]
			if !ok || !onKobo || e.Archived || sent[id] {
				continue
			}
			p := progress[id]
			state, hasSt := states[id]
			if hasSt && state.DeviceTS == progressSig(p) {
				continue // the Kobo already has this
			}
			if count >= koboSyncLimit {
				more = true
				break
			}
			entries = append(entries, kobo.ChangedReadingState(koboState(b, p, true, state, hasSt)))
			_ = s.EReader.SetDeviceTS(uid, id, progressSig(p))
			count++
		}
	}

	// 4. Books bought in the Kobo store, once Mediarium's own are all sent.
	out := http.Header{}
	storeToken := tok.RawStoreToken
	var storeItems []json.RawMessage
	if !more {
		if items, newTok, hdr, ok := s.koboStoreSync(r, tok.RawStoreToken); ok {
			storeItems = items
			storeToken = newTok
			for k, vs := range hdr {
				out[k] = vs
			}
		}
	}

	tok.RawStoreToken = storeToken
	tok.ReadingStateLastModified = now
	tok.BooksLastCreated = now
	tok.BooksLastModified = now
	h := w.Header()
	h.Set(kobo.TokenHeader, tok.Encode())
	for k, vs := range out {
		for _, v := range vs {
			h.Set(k, v)
		}
	}
	if more {
		h.Set(kobo.SyncHeader, "continue")
	}
	all := make([]any, 0, len(entries)+len(storeItems))
	all = append(all, entries...)
	for _, it := range storeItems {
		all = append(all, it)
	}
	writeJSON(w, http.StatusOK, all)
}

// ---- Downloads and covers ----

func (s *Server) koboDownload(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(strings.TrimPrefix(rest, "/download/"), "/")
	if len(parts) != 2 || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	id, ok := kobo.ParseBookUUID(parts[0])
	if !ok {
		// Not a Mediarium book: the store may know it.
		s.koboForward(w, r, rest)
		return
	}
	b, ok := s.koboBookFor(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSuffix(filepath.Base(b.EbookPath), filepath.Ext(b.EbookPath))
	var path string
	var err error
	if strings.EqualFold(parts[1], "kepub") {
		path, err = s.kepubFile(r.Context(), b)
		if err != nil {
			slog.Info("kobo: no KEPUB, sending the EPUB", "book", b.ID, "err", err)
			path, err = s.epubOf(b)
		} else {
			name += ".kepub"
		}
	} else {
		path, err = s.epubOf(b)
	}
	if err != nil {
		slog.Info("kobo: book not available", "book", b.ID, "err", err)
		status := http.StatusUnprocessableEntity
		if errors.Is(err, fs.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, "This book can't be sent to a Kobo.", status)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "Couldn't read the file.", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "Couldn't read the file.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/epub+zip")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".epub"}))
	http.ServeContent(w, r, name+".epub", st.ModTime(), f)
}

func (s *Server) koboCover(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	// {id}/{width}/{height}/{greyscale}/image.jpg, or with {quality} before {greyscale}
	if len(parts) != 5 && len(parts) != 6 {
		s.koboForward(w, r, rest)
		return
	}
	id, ok := kobo.ParseBookUUID(parts[0])
	if !ok {
		// A book from the Kobo store: its cover is on Kobo's server.
		http.Redirect(w, r, "https://cdn.kobo.com/book-images/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1])+"/"+url.PathEscape(parts[2])+"/false/image.jpg", http.StatusTemporaryRedirect)
		return
	}
	b, ok := s.koboBookFor(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	wantW, _ := strconv.Atoi(parts[1])
	wantH, _ := strconv.Atoi(parts[2])
	s.serveEReaderCover(w, r, b, wantW, wantH)
}
