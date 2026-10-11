package blocklist_test

import (
	"fmt"
	"testing"

	"github.com/rdborg/mediarium/internal/blocklist"
)

func TestPageGivesSlicesAndTheTotal(t *testing.T) {
	repo, _ := newRepos(t)
	for i := 0; i < 25; i++ {
		if err := repo.Add(blocklist.Entry{ReleaseTitle: fmt.Sprintf("release %02d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		limit, offset int
		wantLen       int
	}{
		{10, 0, 10},
		{10, 20, 5},
		{10, 25, 0},
		{100, 0, 25},
	}
	for _, tt := range tests {
		got, total, err := repo.Page(tt.limit, tt.offset)
		if err != nil || total != 25 || len(got) != tt.wantLen {
			t.Errorf("Page(%d, %d) = %d rows, total %d, err %v; want %d rows, total 25", tt.limit, tt.offset, len(got), total, err, tt.wantLen)
		}
	}
	all, _ := repo.List()
	first, _, _ := repo.Page(10, 0)
	next, _, _ := repo.Page(10, 10)
	for i, e := range append(first, next...) {
		if e.ID != all[i].ID {
			t.Fatalf("page rows are not in List order at %d", i)
		}
	}
}
