package kobo

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestBookUUIDRoundTrip(t *testing.T) {
	for _, id := range []int64{1, 7, 4096, 123456789} {
		u := BookUUID(id)
		if len(u) != 36 {
			t.Fatalf("%d: %q is not a UUID", id, u)
		}
		got, ok := ParseBookUUID(u)
		if !ok || got != id {
			t.Errorf("%d -> %q -> %d, %v", id, u, got, ok)
		}
		if got, ok := ParseBookUUID(strings.ToUpper(u)); !ok || got != id {
			t.Errorf("upper case %q not read", u)
		}
	}
	// Ids the Kobo store made, and broken ones, are not ours.
	for _, bad := range []string{"", "not-a-uuid", "0e2f5a54-7f6d-4cf6-b4d6-0c0f1a2b3c4d", BookUUID(1) + "0", BookUUID(0), "6d656469-6172-4975-8000-zzzzzzzzzzzz"} {
		if _, ok := ParseBookUUID(bad); ok {
			t.Errorf("%q should not be one of ours", bad)
		}
	}
}

func TestSyncTokenRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 30, 15, 0, time.UTC)
	in := SyncToken{RawStoreToken: "store.token", ReadingStateLastModified: at, BooksLastCreated: at}
	out := ParseSyncToken(in.Encode())
	if out.RawStoreToken != "store.token" || !out.ReadingStateLastModified.Equal(at) || !out.BooksLastCreated.Equal(at) {
		t.Fatalf("round trip: %+v", out)
	}
	if !out.ArchiveLastModified.IsZero() || !out.TagsLastModified.IsZero() {
		t.Errorf("unset times should stay unset: %+v", out)
	}
	// The wire form is Calibre-Web's: base64 of {"version", "data": {...}}.
	raw, err := base64.StdEncoding.DecodeString(in.Encode())
	if err != nil {
		t.Fatal(err)
	}
	var w map[string]any
	if err := json.Unmarshal(raw, &w); err != nil || w["version"] != "1-1-0" {
		t.Fatalf("wire form: %s", raw)
	}
	data := w["data"].(map[string]any)
	for _, k := range []string{"raw_kobo_store_token", "books_last_modified", "books_last_created", "archive_last_modified", "reading_state_last_modified", "tags_last_modified"} {
		if _, ok := data[k]; !ok {
			t.Errorf("token has no %s", k)
		}
	}
}

func TestParseSyncTokenForeignAndBroken(t *testing.T) {
	tests := []struct {
		name, in, wantStore string
	}{
		{"empty", "", ""},
		{"spaces", "   ", ""},
		{"store token with a dot", "abc.def", "abc.def"},
		{"not base64", "%%%", ""},
		{"base64 of not JSON", base64.StdEncoding.EncodeToString([]byte("hello")), ""},
		{"too old a version", base64.StdEncoding.EncodeToString([]byte(`{"version":"0-9-0","data":{"raw_kobo_store_token":"x"}}`)), ""},
		{"no padding", strings.TrimRight(base64.StdEncoding.EncodeToString([]byte(`{"version":"1-1-0","data":{"raw_kobo_store_token":"keep"}}`)), "="), "keep"},
	}
	for _, tt := range tests {
		got := ParseSyncToken(tt.in)
		if got.RawStoreToken != tt.wantStore {
			t.Errorf("%s: store token %q, want %q", tt.name, got.RawStoreToken, tt.wantStore)
		}
		if !got.ReadingStateLastModified.IsZero() {
			t.Errorf("%s: a time appeared from nowhere", tt.name)
		}
	}
}

func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run with -update to make it)", path, err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != string(got) {
		t.Fatalf("%s differs from the golden file:\n%s", name, got)
	}
}

func sampleBook() Book {
	return Book{
		ID: 7, Title: "The Hobbit", Authors: []string{"J.R.R. Tolkien"}, Description: "A hobbit goes out.", Year: 1937,
		Added: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), Modified: time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC),
		SeriesName: "Middle-earth", SeriesPos: "1",
		DownloadURL: "https://books.example/kobo/TOKEN/download/" + BookUUID(7) + "/kepub", Size: 123456,
	}
}

func TestSyncEntryShapes(t *testing.T) {
	b := sampleBook()
	now := time.Date(2026, 10, 9, 12, 30, 0, 0, time.UTC)
	ts := "2026-10-09T12:00:00Z"
	pct, content := 41.5, 12.0
	mins := 25
	rs := ReadingState{
		EntitlementID: BookUUID(7), Created: "2026-10-01T08:00:00Z", LastModified: ts, PriorityTimestamp: ts,
		StatusInfo:      StatusInfo{LastModified: ts, Status: StatusReading, TimesStartedReading: 1, LastTimeStartedReading: ts},
		Statistics:      Statistics{LastModified: ts, SpentReadingMinutes: &mins},
		CurrentBookmark: Bookmark{LastModified: ts, ProgressPercent: &pct, ContentSourceProgressPercent: &content, Location: &Location{Value: "kobo.12.1", Type: "KoboSpan", Source: "OEBPS/ch3.xhtml"}},
	}
	golden(t, "sync_new.json", []any{
		NewEntitlement(BookEntitlement(b, now), BookMetadata(b), &rs),
		ChangedReadingState(rs),
	})
	gone := Book{ID: 9, Title: "Gone", Added: b.Added, Removed: true}
	golden(t, "sync_removed.json", []any{ChangedEntitlement(BookEntitlement(gone, now), BookMetadata(gone), nil)})
}

func TestMetadataBasics(t *testing.T) {
	m := BookMetadata(sampleBook())
	if m.EntitlementID != BookUUID(7) || m.Title != "The Hobbit" || m.Language != "en" {
		t.Errorf("metadata: %+v", m)
	}
	if len(m.DownloadUrls) != 1 || m.DownloadUrls[0].Format != "KEPUB" || m.DownloadUrls[0].Size != 123456 {
		t.Errorf("download urls: %+v", m.DownloadUrls)
	}
	if m.Series == nil || m.Series.Number != int64(1) || m.Series.ID != SeriesUUID("middle-earth") {
		t.Errorf("series: %+v", m.Series)
	}
	if m.PublicationDate != "1937-01-01T00:00:00Z" {
		t.Errorf("publication date %q", m.PublicationDate)
	}
	// No download address, no author, no description: still valid.
	empty := BookMetadata(Book{ID: 1, Title: "T", Added: time.Unix(0, 0)})
	data, _ := json.Marshal(empty)
	if !strings.Contains(string(data), `"DownloadUrls":[]`) || !strings.Contains(string(data), `"Contributors":null`) || !strings.Contains(string(data), `"Description":null`) {
		t.Errorf("empty metadata: %s", data)
	}
	// A series position that isn't a number falls back to 1, as Calibre-Web does.
	s := BookMetadata(Book{ID: 2, Title: "T", SeriesName: "S", SeriesPos: "one", Added: time.Unix(0, 0)})
	if s.Series.NumberFloat != 1 {
		t.Errorf("series fallback: %+v", s.Series)
	}
}

func TestStateUpdateResult(t *testing.T) {
	res := StateUpdateResult(7, true, false, true, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	data, _ := json.Marshal(res)
	var back struct {
		RequestResult string
		UpdateResults []map[string]any
	}
	if err := json.Unmarshal(data, &back); err != nil || back.RequestResult != "Success" || len(back.UpdateResults) != 1 {
		t.Fatalf("result: %s", data)
	}
	u := back.UpdateResults[0]
	if u["EntitlementId"] != BookUUID(7) || u["CurrentBookmarkResult"] == nil || u["StatusInfoResult"] == nil || u["StatisticsResult"] != nil {
		t.Errorf("update result: %v", u)
	}
}

func TestResourcesPointAtThisServer(t *testing.T) {
	base := "https://books.example/kobo/TOKEN/"
	res := Resources(base, nil)
	if res["library_sync"] != "https://books.example/kobo/TOKEN/v1/library/sync" {
		t.Errorf("library_sync: %v", res["library_sync"])
	}
	if got := res["image_url_template"]; got != "https://books.example/kobo/TOKEN/{ImageId}/{Width}/{Height}/false/image.jpg" {
		t.Errorf("image_url_template: %v", got)
	}
	if got := res["image_url_quality_template"]; got != "https://books.example/kobo/TOKEN/{ImageId}/{Width}/{Height}/{Quality}/{IsGreyscale}/image.jpg" {
		t.Errorf("image_url_quality_template: %v", got)
	}
	// Store pages and the sign-in server stay Kobo's own.
	if res["oauth_host"] != "https://oauth.kobo.com" || res["store_host"] != "www.kobo.com" {
		t.Errorf("store resources were changed: %v %v", res["oauth_host"], res["store_host"])
	}
	// The store's own list wins, with only our addresses put over it.
	store := map[string]any{"oauth_host": "https://oauth.kobo.example", "library_sync": "https://storeapi.kobo.com/v1/library/sync"}
	res = Resources(base, store)
	if res["oauth_host"] != "https://oauth.kobo.example" || !strings.HasPrefix(res["library_sync"].(string), base[:len(base)-1]) {
		t.Errorf("store list: %v", res)
	}
	// Changing the answer must not change the built-in list.
	if NativeResources()["library_sync"] != "https://storeapi.kobo.com/v1/library/sync" {
		t.Error("the built-in list was changed")
	}
}
