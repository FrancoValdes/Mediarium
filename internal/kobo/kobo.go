// Package kobo holds the pieces of the Kobo store protocol that Mediarium
// answers itself so a Kobo e-reader can sync a library from it: the sync
// token, the book and reading-state shapes, and the resource list of the
// first request a Kobo makes.
//
// The shapes follow what Kobo's own store sends and what the open source
// servers that imitate it (Calibre-Web, Komga, kobo-shelf) have found the
// device accepts. Nothing here touches the network or the database.
package kobo

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TokenHeader carries the sync token in both directions.
const TokenHeader = "x-kobo-synctoken"

// Header values the Kobo looks at.
const (
	SyncHeader      = "x-kobo-sync"     // "continue" while there is more to fetch
	APITokenHeader  = "x-kobo-apitoken" // answer to the initialization call
	APITokenValue   = "e30="            // base64 of {}
	syncTokenVer    = "1-1-0"           // the version written
	syncTokenMinVer = "1-0-0"           // the oldest read
	timeLayout      = "2006-01-02T15:04:05Z"
	timeLayoutFrac  = "2006-01-02T15:04:05.0000000Z"
)

// FormatTime writes a time the way the Kobo store does.
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// ParseTime reads a time written by FormatTime, with or without fractions.
func ParseTime(s string) (time.Time, bool) {
	for _, l := range []string{timeLayout, timeLayoutFrac, time.RFC3339Nano, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// SyncToken is what the Kobo hands back on its next sync so the server knows
// where it got to. Its shape is the one Calibre-Web uses, so a Kobo that
// moves between the two servers keeps working.
type SyncToken struct {
	// RawStoreToken is the token of the real Kobo store, kept so the answers
	// of both can be merged (see the store merge in the API).
	RawStoreToken            string
	BooksLastModified        time.Time
	BooksLastCreated         time.Time
	ArchiveLastModified      time.Time
	ReadingStateLastModified time.Time
	TagsLastModified         time.Time
}

type tokenWire struct {
	Version string `json:"version"`
	Data    struct {
		RawStoreToken            string  `json:"raw_kobo_store_token"`
		BooksLastModified        float64 `json:"books_last_modified"`
		BooksLastCreated         float64 `json:"books_last_created"`
		ArchiveLastModified      float64 `json:"archive_last_modified"`
		ReadingStateLastModified float64 `json:"reading_state_last_modified"`
		TagsLastModified         float64 `json:"tags_last_modified"`
	} `json:"data"`
}

func epoch(t time.Time) float64 {
	if t.IsZero() {
		return -62135596800 // datetime.min, as Calibre-Web writes it
	}
	return float64(t.UnixNano()) / 1e9
}

func fromEpoch(f float64) time.Time {
	if f <= 0 { // unset, or datetime.min
		return time.Time{}
	}
	sec := int64(f)
	return time.Unix(sec, int64((f-float64(sec))*1e9)).UTC()
}

// ParseSyncToken reads the header value. An empty, unreadable or too old
// token is a new device: the zero SyncToken. A value with a dot in it is a
// token of the real Kobo store, kept as it is.
func ParseSyncToken(header string) SyncToken {
	header = strings.TrimSpace(header)
	if header == "" {
		return SyncToken{}
	}
	if strings.Contains(header, ".") {
		return SyncToken{RawStoreToken: header}
	}
	raw := header
	if pad := len(raw) % 4; pad != 0 {
		raw += strings.Repeat("=", 4-pad)
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return SyncToken{}
	}
	var w tokenWire
	if err := json.Unmarshal(data, &w); err != nil || w.Version == "" || w.Version < syncTokenMinVer {
		return SyncToken{}
	}
	return SyncToken{
		RawStoreToken:            w.Data.RawStoreToken,
		BooksLastModified:        fromEpoch(w.Data.BooksLastModified),
		BooksLastCreated:         fromEpoch(w.Data.BooksLastCreated),
		ArchiveLastModified:      fromEpoch(w.Data.ArchiveLastModified),
		ReadingStateLastModified: fromEpoch(w.Data.ReadingStateLastModified),
		TagsLastModified:         fromEpoch(w.Data.TagsLastModified),
	}
}

// Encode is the header value to send back.
func (t SyncToken) Encode() string {
	var w tokenWire
	w.Version = syncTokenVer
	w.Data.RawStoreToken = t.RawStoreToken
	w.Data.BooksLastModified = epoch(t.BooksLastModified)
	w.Data.BooksLastCreated = epoch(t.BooksLastCreated)
	w.Data.ArchiveLastModified = epoch(t.ArchiveLastModified)
	w.Data.ReadingStateLastModified = epoch(t.ReadingStateLastModified)
	w.Data.TagsLastModified = epoch(t.TagsLastModified)
	data, _ := json.Marshal(w)
	return base64.StdEncoding.EncodeToString(data)
}

// A book's id on the Kobo is a UUID. Ours carry the book's number in the last
// part behind a fixed start, so an id can be turned back into a book without
// a table, and an id that is not ours (a book bought in the Kobo store) is
// recognised at once and handed to the store.
const uuidPrefix = "6d656469-6172-4975-8000-" // "mediarIu" in hex, then the variant

// BookUUID is the Kobo id of a book.
func BookUUID(bookID int64) string {
	return fmt.Sprintf("%s%012x", uuidPrefix, bookID)
}

// ParseBookUUID returns the book an id belongs to; ok is false for any id
// that Mediarium did not make.
func ParseBookUUID(s string) (int64, bool) {
	s = strings.ToLower(s)
	if !strings.HasPrefix(s, uuidPrefix) || len(s) != len(uuidPrefix)+12 {
		return 0, false
	}
	n, err := strconv.ParseInt(s[len(uuidPrefix):], 16, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// SeriesUUID is a stable id for a series name.
func SeriesUUID(name string) string {
	sum := sha1.Sum([]byte("mediarium-series:" + strings.ToLower(name)))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Book is what the sync needs to describe one book.
type Book struct {
	ID          int64
	Title       string
	Authors     []string
	Description string
	Year        int
	Added       time.Time
	Modified    time.Time
	Language    string // two letters
	SeriesName  string
	SeriesPos   string
	DownloadURL string // where the Kobo fetches the KEPUB from
	Size        int64
	Removed     bool // the Kobo is told to drop it
}

type downloadURL struct {
	Format   string `json:"Format"`
	Size     int64  `json:"Size"`
	URL      string `json:"Url"`
	Platform string `json:"Platform"`
}

type seriesInfo struct {
	Name        string  `json:"Name"`
	Number      any     `json:"Number"`
	NumberFloat float64 `json:"NumberFloat"`
	ID          string  `json:"Id"`
}

type price struct {
	CurrencyCode string `json:"CurrencyCode,omitempty"`
	TotalAmount  int    `json:"TotalAmount"`
}

type publisher struct {
	Imprint string `json:"Imprint"`
	Name    string `json:"Name"`
}

type contributorRole struct {
	Name string `json:"Name"`
}

// Metadata is the BookMetadata object.
type Metadata struct {
	Categories              []string          `json:"Categories"`
	CoverImageID            string            `json:"CoverImageId"`
	CrossRevisionID         string            `json:"CrossRevisionId"`
	CurrentDisplayPrice     price             `json:"CurrentDisplayPrice"`
	CurrentLoveDisplayPrice price             `json:"CurrentLoveDisplayPrice"`
	Description             *string           `json:"Description"`
	DownloadUrls            []downloadURL     `json:"DownloadUrls"`
	EntitlementID           string            `json:"EntitlementId"`
	ExternalIDs             []string          `json:"ExternalIds"`
	Genre                   string            `json:"Genre"`
	IsEligibleForKoboLove   bool              `json:"IsEligibleForKoboLove"`
	IsInternetArchive       bool              `json:"IsInternetArchive"`
	IsPreOrder              bool              `json:"IsPreOrder"`
	IsSocialEnabled         bool              `json:"IsSocialEnabled"`
	Language                string            `json:"Language"`
	PhoneticPronunciations  map[string]string `json:"PhoneticPronunciations"`
	PublicationDate         string            `json:"PublicationDate"`
	Publisher               publisher         `json:"Publisher"`
	RevisionID              string            `json:"RevisionId"`
	Title                   string            `json:"Title"`
	WorkID                  string            `json:"WorkId"`
	ContributorRoles        []contributorRole `json:"ContributorRoles,omitempty"`
	Contributors            []string          `json:"Contributors"`
	Series                  *seriesInfo       `json:"Series,omitempty"`
}

const catAll = "00000000-0000-0000-0000-000000000001"

// BookMetadata describes a book the way the Kobo store does.
func BookMetadata(b Book) Metadata {
	id := BookUUID(b.ID)
	lang := b.Language
	if lang == "" {
		lang = "en"
	}
	pub := b.Added
	if b.Year > 0 {
		pub = time.Date(b.Year, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	m := Metadata{
		Categories:              []string{catAll},
		CoverImageID:            id,
		CrossRevisionID:         id,
		CurrentDisplayPrice:     price{CurrencyCode: "USD", TotalAmount: 0},
		CurrentLoveDisplayPrice: price{TotalAmount: 0},
		DownloadUrls:            []downloadURL{},
		EntitlementID:           id,
		ExternalIDs:             []string{},
		Genre:                   catAll,
		IsSocialEnabled:         true,
		Language:                lang,
		PhoneticPronunciations:  map[string]string{},
		PublicationDate:         FormatTime(pub),
		Publisher:               publisher{Imprint: "", Name: ""},
		RevisionID:              id,
		Title:                   b.Title,
		WorkID:                  id,
	}
	if d := strings.TrimSpace(b.Description); d != "" {
		m.Description = &d
	}
	if b.DownloadURL != "" {
		m.DownloadUrls = append(m.DownloadUrls, downloadURL{Format: "KEPUB", Size: b.Size, URL: b.DownloadURL, Platform: "Generic"})
	}
	if len(b.Authors) > 0 {
		m.Contributors = b.Authors
		for _, a := range b.Authors {
			m.ContributorRoles = append(m.ContributorRoles, contributorRole{Name: a})
		}
	}
	if b.SeriesName != "" {
		f, err := strconv.ParseFloat(strings.TrimSpace(b.SeriesPos), 64)
		if err != nil {
			f = 1
		}
		var num any = f
		if f == float64(int64(f)) {
			num = int64(f)
		}
		m.Series = &seriesInfo{Name: b.SeriesName, Number: num, NumberFloat: f, ID: SeriesUUID(b.SeriesName)}
	}
	return m
}

type activePeriod struct {
	From string `json:"From"`
}

// Entitlement is the BookEntitlement object.
type Entitlement struct {
	Accessibility       string       `json:"Accessibility"`
	ActivePeriod        activePeriod `json:"ActivePeriod"`
	Created             string       `json:"Created"`
	CrossRevisionID     string       `json:"CrossRevisionId"`
	ID                  string       `json:"Id"`
	IsRemoved           bool         `json:"IsRemoved"`
	IsHiddenFromArchive bool         `json:"IsHiddenFromArchive"`
	IsLocked            bool         `json:"IsLocked"`
	LastModified        string       `json:"LastModified"`
	OriginCategory      string       `json:"OriginCategory"`
	RevisionID          string       `json:"RevisionId"`
	Status              string       `json:"Status"`
}

// BookEntitlement says the Kobo owns the book (or, with Removed, that it no
// longer should).
func BookEntitlement(b Book, now time.Time) Entitlement {
	id := BookUUID(b.ID)
	mod := b.Modified
	if mod.IsZero() {
		mod = b.Added
	}
	return Entitlement{
		Accessibility:   "Full",
		ActivePeriod:    activePeriod{From: FormatTime(now)},
		Created:         FormatTime(b.Added),
		CrossRevisionID: id,
		ID:              id,
		IsRemoved:       b.Removed,
		LastModified:    FormatTime(mod),
		OriginCategory:  "Imported",
		RevisionID:      id,
		Status:          "Active",
	}
}

// Location is a place in a book, in the Kobo's own terms.
type Location struct {
	Value  string `json:"Value"`
	Type   string `json:"Type"`
	Source string `json:"Source"`
}

// StatusInfo is whether the book is unread, being read or finished.
type StatusInfo struct {
	LastModified           string `json:"LastModified"`
	Status                 string `json:"Status"`
	TimesStartedReading    int    `json:"TimesStartedReading"`
	LastTimeStartedReading string `json:"LastTimeStartedReading,omitempty"`
}

// Statistics is the time spent and left.
type Statistics struct {
	LastModified         string `json:"LastModified"`
	SpentReadingMinutes  *int   `json:"SpentReadingMinutes,omitempty"`
	RemainingTimeMinutes *int   `json:"RemainingTimeMinutes,omitempty"`
}

// Bookmark is where the reader is.
type Bookmark struct {
	LastModified                 string    `json:"LastModified"`
	ProgressPercent              *float64  `json:"ProgressPercent,omitempty"`
	ContentSourceProgressPercent *float64  `json:"ContentSourceProgressPercent,omitempty"`
	Location                     *Location `json:"Location,omitempty"`
}

// ReadingState is the ReadingState object.
type ReadingState struct {
	EntitlementID     string     `json:"EntitlementId"`
	Created           string     `json:"Created"`
	LastModified      string     `json:"LastModified"`
	PriorityTimestamp string     `json:"PriorityTimestamp"`
	StatusInfo        StatusInfo `json:"StatusInfo"`
	Statistics        Statistics `json:"Statistics"`
	CurrentBookmark   Bookmark   `json:"CurrentBookmark"`
}

// The three states of a book.
const (
	StatusReadyToRead = "ReadyToRead"
	StatusReading     = "Reading"
	StatusFinished    = "Finished"
)

// ValidStatus reports whether s is a state the Kobo uses.
func ValidStatus(s string) bool {
	return s == StatusReadyToRead || s == StatusReading || s == StatusFinished
}

// StatePut is the body of a reading state update.
type StatePut struct {
	ReadingStates []struct {
		CurrentBookmark *struct {
			ProgressPercent              *float64  `json:"ProgressPercent"`
			ContentSourceProgressPercent *float64  `json:"ContentSourceProgressPercent"`
			Location                     *Location `json:"Location"`
		} `json:"CurrentBookmark"`
		Statistics *struct {
			SpentReadingMinutes  *float64 `json:"SpentReadingMinutes"`
			RemainingTimeMinutes *float64 `json:"RemainingTimeMinutes"`
		} `json:"Statistics"`
		StatusInfo *struct {
			Status string `json:"Status"`
		} `json:"StatusInfo"`
	} `json:"ReadingStates"`
}

// Wrapper entries of the sync answer. Each element of the array is an object
// with one key.
type syncEntry map[string]any

// NewEntitlement wraps a book the Kobo does not have yet.
func NewEntitlement(e Entitlement, m Metadata, rs *ReadingState) any {
	return entitlement("NewEntitlement", e, m, rs)
}

// ChangedEntitlement wraps a book the Kobo has, changed or removed.
func ChangedEntitlement(e Entitlement, m Metadata, rs *ReadingState) any {
	return entitlement("ChangedEntitlement", e, m, rs)
}

func entitlement(kind string, e Entitlement, m Metadata, rs *ReadingState) any {
	inner := map[string]any{"BookEntitlement": e, "BookMetadata": m}
	if rs != nil {
		inner["ReadingState"] = rs
	}
	return syncEntry{kind: inner}
}

// ChangedReadingState wraps a reading state that changed since the last sync.
func ChangedReadingState(rs ReadingState) any {
	return syncEntry{"ChangedReadingState": map[string]any{"ReadingState": rs}}
}

// StateUpdateResult is the answer to a reading state update.
func StateUpdateResult(bookID int64, bookmark, stats, status bool, now time.Time) map[string]any {
	res := map[string]any{
		"EntitlementId":     BookUUID(bookID),
		"LastModified":      FormatTime(now),
		"PriorityTimestamp": FormatTime(now),
	}
	ok := map[string]string{"Result": "Success"}
	if bookmark {
		res["CurrentBookmarkResult"] = ok
	}
	if stats {
		res["StatisticsResult"] = ok
	}
	if status {
		res["StatusInfoResult"] = ok
	}
	return map[string]any{"RequestResult": "Success", "UpdateResults": []any{res}}
}
