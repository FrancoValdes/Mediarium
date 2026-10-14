package queue_test

import (
	"testing"

	"github.com/rdborg/mediarium/internal/queue"
)

// % and _ in an Activity search are plain characters, not wildcards.
func TestActivitySearchTreatsWildcardsAsText(t *testing.T) {
	repo := queue.NewRepo(openDB(t))
	for _, msg := range []string{"Grabbed Film 100% done", "Grabbed Film 1000 done", "Imported my_show", "Imported myXshow", `Moved C:\films`} {
		if err := repo.LogActivity(0, "added", msg); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		query string
		want  int
	}{
		{"100%", 1},
		{"%", 1},
		{"my_show", 1},
		{"_", 1},
		{`C:\films`, 1},
		{"film", 3},
		{"", 5},
	}
	for _, tt := range tests {
		got, err := repo.ListActivityFor(50, tt.query)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tt.want {
			t.Errorf("search %q found %d lines, want %d", tt.query, len(got), tt.want)
		}
	}
}
