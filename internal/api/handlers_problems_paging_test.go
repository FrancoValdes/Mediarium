package api_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/problems"
)

// recordMany reports n different problems (a different subject each, so none
// folds into another): codes alternate between an error and a warning.
func recordMany(t *testing.T, log *problems.Log, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		code := problems.CodeUnpackFailed
		if i%3 == 0 {
			code = problems.CodeUsenetTooMany
		}
		problems.Record(problems.Problem{Code: code, Subject: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("Title %d", i)})
	}
	log.Flush()
}

func TestProblemLogPagesWithTotals(t *testing.T) {
	server, base, client := loginNewServer(t)
	recordMany(t, server.Problems, 130) // 44 warnings, 86 errors

	first := problemsList(t, client, base, "")
	if n := len(first["items"].([]any)); n != 30 || first["total"].(float64) != 130 || first["limit"].(float64) != 30 || first["offset"].(float64) != 0 {
		t.Fatalf("the default page: %d items, %+v", n, first)
	}
	// Existing keys stay.
	for _, key := range []string{"items", "total", "counts", "areas", "notifyOnProblems", "keepDays"} {
		if _, ok := first[key]; !ok {
			t.Errorf("the response lost %q", key)
		}
	}

	big := problemsList(t, client, base, "?limit=1000&offset=100")
	if n := len(big["items"].([]any)); n != 30 || big["limit"].(float64) != 100 || big["offset"].(float64) != 100 || big["total"].(float64) != 130 {
		t.Fatalf("a limit above the maximum is cut to 100: %d items, %+v", n, big)
	}

	warnings := problemsList(t, client, base, "?level=warning&limit=10&offset=40")
	if n := len(warnings["items"].([]any)); n != 4 || warnings["total"].(float64) != 44 {
		t.Fatalf("the filter applies before the paging: %d items, total %v", n, warnings["total"])
	}

	// The cards at the top count everything, whatever the page.
	counts := first["counts"].(map[string]any)
	if counts["errorsToday"].(float64) != 86 || counts["warningsToday"].(float64) != 44 || counts["unread"].(float64) != 130 {
		t.Errorf("the counts must cover the whole log: %+v", counts)
	}

	seen := map[float64]bool{}
	for off := 0; off < 130; off += 30 {
		page := problemsList(t, client, base, fmt.Sprintf("?offset=%d", off))
		for _, raw := range page["items"].([]any) {
			id := raw.(map[string]any)["id"].(float64)
			if seen[id] {
				t.Fatalf("row %v came on two pages", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != 130 {
		t.Errorf("the pages hold %d rows, want 130", len(seen))
	}
}

func TestProblemLogMarkAllReadFollowsTheFilters(t *testing.T) {
	server, base, client := loginNewServer(t)
	recordMany(t, server.Problems, 60) // 20 warnings, 40 errors

	// Marking the warnings leaves the errors unread, however many rows a page holds.
	res := postJSON[map[string]any](t, client, base+"/api/system/problems/read?level=warning", map[string]any{"all": true}, http.StatusOK)
	if res["marked"].(float64) != 20 || res["counts"].(map[string]any)["unread"].(float64) != 40 {
		t.Fatalf("mark the warnings: %+v", res)
	}
	if got := problemsList(t, client, base, "?unread=1&level=warning"); got["total"].(float64) != 0 {
		t.Fatalf("no warnings are unread now: %+v", got)
	}
	if got := problemsList(t, client, base, "?unread=1&level=error"); got["total"].(float64) != 40 {
		t.Fatalf("the errors stay unread: %+v", got)
	}

	// A text search narrows it too.
	res = postJSON[map[string]any](t, client, base+"/api/system/problems/read?q=title+1&level=error", map[string]any{"all": true}, http.StatusOK)
	marked := int(res["marked"].(float64))
	want := problemsList(t, client, base, "?level=error&q=title+1")["total"].(float64)
	if float64(marked) != want || marked == 0 || marked >= 40 {
		t.Fatalf("marked %d, the search matches %v", marked, want)
	}

	// A bad filter is refused; with no filter "all" still means everything.
	postJSON[map[string]any](t, client, base+"/api/system/problems/read?level=loud", map[string]any{"all": true}, http.StatusBadRequest)
	res = postJSON[map[string]any](t, client, base+"/api/system/problems/read", map[string]any{"all": true}, http.StatusOK)
	if res["counts"].(map[string]any)["unread"].(float64) != 0 {
		t.Fatalf("mark everything: %+v", res)
	}
}

func TestProblemLogFileHoldsEveryMatchingRow(t *testing.T) {
	server, base, client := loginNewServer(t)
	recordMany(t, server.Problems, 450) // far more than one page, and more than the old 200 per read

	resp, err := client.Get(base + "/api/system/problems/export")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if !strings.Contains(text, "Problems: 450\n") || strings.Count(text, "\n[") != 450 {
		t.Fatalf("the file should hold all 450 rows, header %q, rows %d", strings.SplitN(text, "\n", 4)[2], strings.Count(text, "\n["))
	}

	resp, err = client.Get(base + "/api/system/problems/export?level=warning")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Problems: 150\n") {
		t.Fatalf("the filter applies to the whole file: %s", strings.SplitN(string(body), "\n", 4)[2])
	}
}
