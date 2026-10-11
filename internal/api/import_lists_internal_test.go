package api

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/importlists"
	"github.com/rdborg/mediarium/internal/metadata"
	"github.com/rdborg/mediarium/internal/trakt"
)

func TestParseTMDBListNumber(t *testing.T) {
	tests := []struct {
		in   string
		want int
		err  bool
	}{
		{"8504994", 8504994, false},
		{" 555 ", 555, false},
		{"https://www.themoviedb.org/list/8504994", 8504994, false},
		{"themoviedb.org/list/8504994-my-favourites", 8504994, false},
		{"https://www.themoviedb.org/list/12?language=de-DE", 12, false},
		{"https://www.themoviedb.org/de-DE/list/77", 77, false},
		{"https://www.themoviedb.org/movie/603", 0, true}, // a movie, not a list
		{"https://example.com/list/5", 0, true},
		{"0", 0, true},
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, tc := range tests {
		got, err := parseTMDBListNumber(tc.in)
		if got != tc.want || (err != nil) != tc.err {
			t.Errorf("parseTMDBListNumber(%q) = %d, %v; want %d, error %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestListRef(t *testing.T) {
	tests := []struct {
		name string
		it   importlists.Item
		want string
	}{
		{"tmdb id", importlists.Item{Kind: "tv", TMDBID: 5}, "tmdb:tv:5"},
		{"bare tmdb id is a movie", importlists.Item{TMDBID: 603}, "tmdb:movie:603"},
		{"imdb id", importlists.Item{Kind: "movie", IMDBID: "tt1"}, "imdb:tt1:movie"},
		{"title only, spelled differently", importlists.Item{Title: "The Alien!", Year: 1979}, "title:alien|1979|"},
	}
	for _, tc := range tests {
		if got := listRef(tc.it); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	if listRef(importlists.Item{Title: "Alien", Year: 1979}) != listRef(importlists.Item{Title: "alien", Year: 1979}) {
		t.Error("titles that differ only in case are the same entry")
	}
	items := []importlists.Item{{Kind: "movie", TMDBID: 1}, {Kind: "movie", TMDBID: 1, Title: "again"}, {Kind: "tv", TMDBID: 1}}
	if got := dedupeListItems(items); len(got) != 2 {
		t.Errorf("dedupe kept %d, want 2", len(got))
	}
}

func TestListSchedule(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		l    importList
		due  bool
	}{
		{"never read", importList{Enabled: true, IntervalHours: 24}, true},
		{"read an hour ago, daily", importList{Enabled: true, IntervalHours: 24, LastRunAt: now.Add(-time.Hour).Format(time.RFC3339)}, false},
		{"read a day ago, daily", importList{Enabled: true, IntervalHours: 24, LastRunAt: now.Add(-24 * time.Hour).Format(time.RFC3339)}, true},
		{"read two hours ago, hourly", importList{Enabled: true, IntervalHours: 1, LastRunAt: now.Add(-2 * time.Hour).Format(time.RFC3339)}, true},
		{"read six days ago, weekly", importList{Enabled: true, IntervalHours: 168, LastRunAt: now.Add(-6 * 24 * time.Hour).Format(time.RFC3339)}, false},
		{"switched off", importList{Enabled: false, IntervalHours: 1}, false},
		{"unreadable date", importList{Enabled: true, IntervalHours: 24, LastRunAt: "yesterday"}, true},
	}
	for _, tc := range tests {
		if got := listIsDue(tc.l, now); got != tc.due {
			t.Errorf("%s: due = %v, want %v", tc.name, got, tc.due)
		}
	}
}

func TestListRunMessage(t *testing.T) {
	tests := []struct {
		name string
		r    listRunResult
		want []string
		not  []string
	}{
		{"empty list", listRunResult{}, []string{"The list is empty."}, nil},
		{"all new", listRunResult{found: 3, added: 3}, []string{"Found 3 titles.", "Added 3."}, []string{"Skipped"}},
		{"one title", listRunResult{found: 1, added: 1}, []string{"Found 1 title."}, nil},
		{"nothing new", listRunResult{found: 5, earlier: 5}, []string{"Nothing new to add.", "5 handled before"}, nil},
		{"mixed", listRunResult{found: 20, added: 4, inLibrary: 6, excluded: 1, filtered: 3, noMatch: 2, waiting: 4},
			[]string{"Added 4.", "6 already in the library", "1 on your Not interested list", "3 below the minimum rating or year", "2 not found on TMDB", "4 more wait for the next read."}, nil},
		{"failures", listRunResult{found: 2, failed: 2, firstFailure: "TMDB is down"}, []string{"2 couldn't be added and will be tried again. TMDB is down."}, nil},
		{"module off", listRunResult{found: 2, off: 2}, []string{"switched off"}, nil},
	}
	for _, tc := range tests {
		got := tc.r.message()
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q should contain %q", tc.name, got, w)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: %q should not contain %q", tc.name, got, n)
			}
		}
	}
	if (listRunResult{inLibrary: 2, filtered: 1, excluded: 1, noMatch: 1, earlier: 3}).skipped() != 8 {
		t.Error("skipped adds up wrong")
	}
}

func TestListErrorText(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{metadata.ErrNoAPIKey, "Add your TMDB API key"},
		{metadata.ErrInvalidKey, "TMDB rejected"},
		{trakt.ErrNoClientID, "Trakt client ID"},
		{trakt.ErrPrivate, "private"},
		{&trakt.RateLimitError{RetryAfter: 30 * time.Second}, "30 seconds"},
		{&importlists.RateLimitError{Service: "MDBList"}, "MDBList asked to slow down"},
		{importlists.ErrBlocked, "browser"},
		{&metadata.StatusError{Path: "/list/5", Code: 404}, "no public list"},
		{errors.New(`Get "https://x.example/a.csv": dial tcp: lookup x.example: no such host`), "address"},
	}
	for _, tc := range tests {
		got := listErrorText(tc.err)
		if !strings.Contains(got, tc.want) || !strings.HasSuffix(got, ".") {
			t.Errorf("listErrorText(%v) = %q, want it to contain %q and end with a full stop", tc.err, got, tc.want)
		}
	}
}

func TestRequestKeyKeepsFourKApart(t *testing.T) {
	plain := requestKey("movie", `{"tmdbId":603}`)
	four := requestKey("movie", `{"tmdbId":603,"fourK":true}`)
	if plain == "" || four == "" || plain == four {
		t.Fatalf("keys: %q and %q should be set and differ", plain, four)
	}
	if !payloadFourK([]byte(`{"tmdbId":603,"fourK":true}`)) || payloadFourK([]byte(`{"tmdbId":603}`)) || payloadFourK([]byte(`nonsense`)) {
		t.Fatal("payloadFourK is wrong")
	}
}

func TestQuotaWording(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	week := quotaInfo{Limit: 5, Used: 5, Period: "week", ResetsAt: now.Add(50 * time.Hour).Format(time.RFC3339)}
	if got := quotaRefusal(week, now); !strings.Contains(got, "5 requests in the last week") || !strings.Contains(got, "in about 2 days") {
		t.Errorf("week: %q", got)
	}
	day := quotaInfo{Limit: 1, Used: 1, Period: "day", ResetsAt: now.Add(40 * time.Minute).Format(time.RFC3339)}
	if got := quotaRefusal(day, now); !strings.Contains(got, "1 request in the last day") || !strings.Contains(got, "in about an hour") {
		t.Errorf("day: %q", got)
	}
	for d, want := range map[time.Duration]string{3 * time.Hour: "in about 3 hours", 20 * time.Hour: "in about 20 hours", 6 * 24 * time.Hour: "in about 6 days"} {
		if got := inAbout(d); got != want {
			t.Errorf("inAbout(%v) = %q, want %q", d, got, want)
		}
	}
}
