package store_test

import (
	"strings"
	"testing"
)

// The questions asked of the episodes table all the time are answered from an
// index, not by reading every episode.
func TestEpisodeQuestionsUseAnIndex(t *testing.T) {
	db := openTemp(t)
	for _, c := range []struct{ query, index string }{
		{`SELECT COUNT(*) FROM episodes WHERE series_id = 1 AND status = 'downloaded'`, "episodes_state"},
		{`SELECT 1 FROM episodes WHERE series_id = 1 AND season > 0 AND monitored = 1 AND status = 'missing'`, "episodes_state"},
		{`SELECT file_path FROM episodes WHERE status = 'missing' AND air_date <= '2026-10-09'`, "episodes_status_air"},
		{`SELECT id FROM episodes WHERE air_date >= '2026-09-09' AND air_date <= '2027-02-06'`, "episodes_air"},
	} {
		rows, err := db.Query(`EXPLAIN QUERY PLAN ` + c.query)
		if err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		if joined := strings.Join(plan, " | "); !strings.Contains(joined, c.index) {
			t.Errorf("%s\n  plan: %s\n  want it to use %s", c.query, joined, c.index)
		}
	}
}
