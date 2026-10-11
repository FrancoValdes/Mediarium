package api_test

import (
	"fmt"
	"testing"

	"github.com/rdborg/mediarium/internal/blocklist"
)

func TestBlocklistListsInPagesWhenAsked(t *testing.T) {
	server, base, client := loginNewServer(t)
	for i := 0; i < 130; i++ {
		if err := server.Blocklist.Add(blocklist.Entry{ReleaseTitle: fmt.Sprintf("Some.Release.%03d", i), Reason: "bad"}); err != nil {
			t.Fatal(err)
		}
	}

	// No query: the plain list of everything, as before.
	if all := getJSON[[]map[string]any](t, client, base+"/api/blocklist"); len(all) != 130 {
		t.Fatalf("the plain list holds %d entries, want 130", len(all))
	}

	page := getJSON[map[string]any](t, client, base+"/api/blocklist?limit=100")
	if n := len(page["items"].([]any)); n != 100 || page["total"].(float64) != 130 || page["limit"].(float64) != 100 || page["offset"].(float64) != 0 {
		t.Fatalf("first page: %d items, %+v", n, page)
	}
	rest := getJSON[map[string]any](t, client, base+"/api/blocklist?limit=100&offset=100")
	if n := len(rest["items"].([]any)); n != 30 || rest["total"].(float64) != 130 {
		t.Fatalf("second page: %d items, %+v", n, rest)
	}
	// A silly limit is cut to the most allowed.
	if big := getJSON[map[string]any](t, client, base+"/api/blocklist?limit=100000"); big["limit"].(float64) != 500 {
		t.Fatalf("limit not capped: %+v", big)
	}
}
