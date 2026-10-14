package queue_test

import (
	"testing"

	"github.com/rdborg/mediarium/internal/queue"
)

// A download meant to replace a movie's file remembers which file, so the
// import can still do it after a restart.
func TestItemRemembersTheFileItReplaces(t *testing.T) {
	db := openDB(t)
	if _, err := db.Exec(`INSERT INTO movies (id, tmdb_id, title, year, status) VALUES (1, 101, 'A', 2000, 'downloaded')`); err != nil {
		t.Fatal(err)
	}
	repo := queue.NewRepo(db)
	tests := []struct {
		name     string
		replaces string
	}{
		{"an ordinary download replaces nothing", ""},
		{"a smaller version names the file", "/movies/A (2000)/A (2000) [2160p].mkv"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, err := repo.Enqueue(queue.Item{MovieID: 1, ReleaseTitle: "A.2000.1080p", NZBURL: "http://x/a.nzb", ReplacesFile: tc.replaces})
			if err != nil {
				t.Fatal(err)
			}
			got, err := repo.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if got.ReplacesFile != tc.replaces {
				t.Fatalf("ReplacesFile = %q, want %q", got.ReplacesFile, tc.replaces)
			}
		})
	}
}
