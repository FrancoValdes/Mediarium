package metadata_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/rdborg/mediarium/internal/metadata"
)

// Text that is read on screen can come in the language a person picked, while
// the default client keeps asking in English: names for files and searches on
// indexers must never depend on the chosen language.
func TestLocalizedClientAsksForItsLanguage(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Query().Get("language"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := metadata.NewWithBaseURL("key", srv.URL)
	ctx := context.Background()
	if _, err := c.SearchMovies(ctx, "heat"); err != nil {
		t.Fatal(err)
	}
	de := c.Localized("de-DE")
	if _, err := de.SearchMovies(ctx, "heat"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SearchMovies(ctx, "heat"); err != nil {
		t.Fatal(err)
	}
	want := []string{"en-US", "de-DE", "en-US"}
	if len(asked) != len(want) {
		t.Fatalf("asked %v, want %v", asked, want)
	}
	for i := range want {
		if asked[i] != want[i] {
			t.Errorf("request %d asked for %q, want %q", i, asked[i], want[i])
		}
	}

	if c.Localized("") != c || c.Localized("en-US") != c {
		t.Error("the default language should give the client itself")
	}
	if c.Localized("de-DE") != de {
		t.Error("a language should have one view, made once")
	}
	if de.Localized("fr-FR") != c.Localized("fr-FR") {
		t.Error("a view of a view should be a view of the original")
	}
}

func TestLocalizedClientSharesTheKey(t *testing.T) {
	c := metadata.New("")
	de := c.Localized("de-DE")
	if de.HasAPIKey() {
		t.Fatal("no key yet")
	}
	c.SetAPIKey("later")
	if !de.HasAPIKey() {
		t.Fatal("a key saved afterwards should reach the language view too")
	}
	c.SetAPIKey("")
	if de.HasAPIKey() {
		t.Fatal("a removed key should be gone from the language view too")
	}
}
