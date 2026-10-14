package migrate

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// One show SickChill can't answer for is skipped with a reason; the others
// are still imported.
func TestSickChillOneBadShowIsSkipped(t *testing.T) {
	h := newHarness(t)
	sc := newFakeSickChill(t)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Get("cmd") == "show" && q.Get("tvdbid") == "12345" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		resp, err := http.Get(sc.URL + r.URL.RequestURI())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(front.Close)
	src := Sources{SickChill: &Conn{URL: front.URL, APIKey: sickchillKey}}

	pv, err := h.im.Preview(context.Background(), Options{Sources: src})
	if err != nil {
		t.Fatal(err)
	}
	s := pv.SickChill
	if s == nil || !s.OK || s.Summary != (Summary{Total: 3, Add: 1, Skip: 2}) {
		t.Fatalf("sickchill preview: %+v", s)
	}
	old := showByTitle(t, s.Items, "Fixture Old Show")
	if old.Action != ActionSkip || !strings.Contains(old.Reason, "couldn't read it from SickChill") {
		t.Fatalf("the show that failed: %+v", old)
	}
	if strings.Contains(old.Reason, sickchillKey) {
		t.Fatalf("the reason shows the API key: %s", old.Reason)
	}
	if bb := showByTitle(t, s.Items, "Breaking Bad"); bb.Action != ActionAdd {
		t.Fatalf("breaking bad: %+v", bb)
	}
}
