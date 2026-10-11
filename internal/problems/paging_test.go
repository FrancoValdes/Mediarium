package problems_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/problems"
)

// seed writes n rows straight into the table, newest first, so a test can have
// thousands without going through Record. Every third row is a warning in the
// usenet area, the rest are errors in downloads. Odd rows are read, even rows
// are unread.
func seed(t *testing.T, db *sql.DB, now time.Time, n int) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		at := now.Add(-time.Duration(i) * time.Minute).UTC().Format("2006-01-02T15:04:05Z")
		level, area, code := "error", "downloads", problems.CodeUnpackFailed
		if i%3 == 0 {
			level, area, code = "warning", "usenet", problems.CodeUsenetTooMany
		}
		if _, err := tx.Exec(`INSERT INTO problems (first_at, last_at, level, area, code, subject, message, detail, title, link, download_id, count, is_read)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', 0, 1, ?)`,
			at, at, level, area, code, fmt.Sprintf("s%d", i), fmt.Sprintf("Message number %d", i), "some detail", fmt.Sprintf("Title %d", i), i%2); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPagingKeepsTotalsRight(t *testing.T) {
	l, c, db := newLogDB(t)
	seed(t, db, c.now(), 250)
	// Rows 0, 3, ..., 249 are warnings: 84. Even rows are unread: 125.

	tests := []struct {
		name      string
		f         problems.Filter
		wantLen   int
		wantTotal int
		wantLimit int
	}{
		{"default page is 30", problems.Filter{}, 30, 250, 30},
		{"a limit below the maximum", problems.Filter{Limit: 10}, 10, 250, 10},
		{"the page is capped at 100", problems.Filter{Limit: 500}, 100, 250, 100},
		{"the last page is short", problems.Filter{Limit: 100, Offset: 200}, 50, 250, 100},
		{"past the end is empty", problems.Filter{Offset: 1000}, 0, 250, 30},
		{"a negative offset starts at the top", problems.Filter{Offset: -5, Limit: 5}, 5, 250, 5},
		{"errors only", problems.Filter{Level: problems.LevelError, Limit: 100}, 100, 250 - 84, 100},
		{"warnings only", problems.Filter{Level: problems.LevelWarning}, 30, 84, 30},
		{"unread only", problems.Filter{UnreadOnly: true, Limit: 100}, 100, 125, 100},
		{"area", problems.Filter{Area: problems.AreaUsenet, Limit: 100}, 84, 84, 100},
		{"text: every word must be in the row (24, 124, 224, 240 to 249)", problems.Filter{Text: "number 24"}, 13, 13, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, total, err := l.List(tt.f)
			if err != nil {
				t.Fatal(err)
			}
			limit, _ := tt.f.Paging()
			if len(got) != tt.wantLen || total != tt.wantTotal || limit != tt.wantLimit {
				t.Errorf("got %d rows, total %d, limit %d; want %d, %d, %d", len(got), total, limit, tt.wantLen, tt.wantTotal, tt.wantLimit)
			}
		})
	}

	t.Run("pages follow each other with no gap and no repeat", func(t *testing.T) {
		seen := map[int64]bool{}
		var last time.Time
		for off := 0; off < 250; off += 30 {
			page, _, err := l.List(problems.Filter{Offset: off})
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range page {
				if seen[e.ID] {
					t.Fatalf("row %d came twice", e.ID)
				}
				seen[e.ID] = true
				if !last.IsZero() && e.LastAt.After(last) {
					t.Fatalf("not newest first at row %d", e.ID)
				}
				last = e.LastAt
			}
		}
		if len(seen) != 250 {
			t.Errorf("saw %d rows, want 250", len(seen))
		}
	})

	t.Run("the cards count everything, not the page", func(t *testing.T) {
		counts, err := l.Counts(c.now())
		if err != nil {
			t.Fatal(err)
		}
		if counts.Unread != 125 || counts.ErrorsWeek+counts.WarningsWeek != 250 {
			t.Errorf("counts = %+v", counts)
		}
	})

	t.Run("All ignores the paging and applies the filter", func(t *testing.T) {
		warnings, err := l.All(problems.Filter{Level: problems.LevelWarning, Limit: 5, Offset: 3})
		if err != nil || len(warnings) != 84 {
			t.Fatalf("got %d rows, err %v; want 84", len(warnings), err)
		}
		everything, err := l.All(problems.Filter{})
		if err != nil || len(everything) != 250 {
			t.Fatalf("got %d rows, err %v; want 250", len(everything), err)
		}
	})
}

func TestMarkAllReadFollowsTheFilter(t *testing.T) {
	l, c, db := newLogDB(t)
	seed(t, db, c.now(), 90) // 30 warnings and 60 errors; the even rows (45) are unread

	// Warnings are rows 0, 3, 6, ...; the even ones among them are 0, 6, 12, ...: 15.
	n, err := l.MarkAllRead(problems.Filter{Level: problems.LevelWarning, Limit: 5, Offset: 20})
	if err != nil {
		t.Fatal(err)
	}
	if n != 15 {
		t.Errorf("marked %d, want 15 (all the warnings, not the page)", n)
	}
	counts, _ := l.Counts(c.now())
	if counts.Unread != 30 {
		t.Errorf("unread = %d, want 30", counts.Unread)
	}
	n, err = l.MarkAllRead(problems.Filter{})
	if err != nil || n != 30 {
		t.Errorf("marked %d (%v), want the remaining 30", n, err)
	}
}

// A few thousand rows, more than the log keeps, must not slow a page down.
func TestPagesStayFastWithThousandsOfRows(t *testing.T) {
	l, c, db := newLogDB(t)
	seed(t, db, c.now(), 3000)
	start := time.Now()
	for i := 0; i < 20; i++ {
		if _, _, err := l.List(problems.Filter{Offset: 30 * i}); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Counts(c.now()); err != nil {
			t.Fatal(err)
		}
	}
	d := time.Since(start)
	t.Logf("20 pages with their counts over 3000 rows: %v (%v each)", d, d/20)
	if d > 5*time.Second {
		t.Errorf("20 pages with their counts took %v", d)
	}
	start = time.Now()
	all, err := l.All(problems.Filter{})
	if err != nil || len(all) != 3000 {
		t.Fatalf("All: %d rows, %v", len(all), err)
	}
	t.Logf("the whole log (3000 rows) for the text file: %v", time.Since(start))
	start = time.Now()
	text := problems.Text(all, c.now())
	t.Logf("the text file: %d bytes in %v", len(text), time.Since(start))
}
