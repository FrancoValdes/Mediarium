package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// An audiobook Open Library doesn't carry but Audible does should import
// through the Audnexus fallback, with the narrator and length it reported.
func TestImportAudiobookViaAudnexusFallback(t *testing.T) {
	server, base, client := loginNewServer(t)
	server.TestSetOpenLibrary(newFakeOpenLibrary(t).URL) // answers, but nothing matches our title

	audible := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("title") != "Legion's Fifth Vault" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `{"products":[{"asin":"B0TEST0001"}]}`)
	}))
	defer audible.Close()
	audnexus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/books/B0TEST0001" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"asin":"B0TEST0001","title":"Legion's Fifth Vault","formatType":"unabridged","narrators":[{"name":"Test Narrator"}],"runtimeLengthMin":600,"releaseDate":"2023-05-04T00:00:00.000Z"}`)
	}))
	defer audnexus.Close()
	server.TestSetAudnexus(audible.URL, audnexus.URL)

	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/modules", map[string]any{"audiobooks": true}, http.StatusOK)
	audios := filepath.Join(t.TempDir(), "audio")
	bookDir := filepath.Join(audios, "A. F Kay", "Legion's Fifth Vault")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bookDir, "01 - Legion's Fifth Vault.m4b"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/settings", map[string]any{"audiobooksPath": audios}, http.StatusOK)

	postJSON[map[string]any](t, client, base+"/api/books/import", map[string]any{}, http.StatusAccepted)
	waitBackground(t, server)
	st := getJSON[map[string]any](t, client, base+"/api/books/import")
	if st["phase"] != "done" || st["summary"].(map[string]any)["imported"] != float64(1) {
		t.Fatalf("import: %v", st)
	}

	list := getJSON[[]map[string]any](t, client, base+"/api/books")
	if len(list) != 1 {
		t.Fatalf("books: %v", list)
	}
	b := list[0]
	if b["title"] != "Legion's Fifth Vault" || b["asin"] != "B0TEST0001" || b["narrators"] != "Test Narrator" || b["runtimeMin"] != float64(600) {
		t.Fatalf("imported book: %v", b)
	}
	a := b["audiobook"].(map[string]any)
	if a["status"] != "downloaded" || a["format"] != "m4b" || a["wanted"] != true {
		t.Fatalf("imported audiobook: %v", b)
	}
}

// "Run the unmatched ones again" looks only at the books the last import could
// not find, without reading the folders again: here Audible did not answer the
// first time and does the second.
func TestRetryBookImportLooksAtTheUnmatchedOnes(t *testing.T) {
	server, base, client := loginNewServer(t)
	server.TestSetOpenLibrary(newFakeOpenLibrary(t).URL)

	var up atomic.Bool
	audible := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"products":[{"asin":"B0TEST0002"}]}`)
	}))
	defer audible.Close()
	audnexus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"asin":"B0TEST0002","title":"Uru's Third Temple","formatType":"unabridged","narrators":[{"name":"Test Narrator"}],"runtimeLengthMin":300,"releaseDate":"2022-01-02T00:00:00.000Z"}`)
	}))
	defer audnexus.Close()
	server.TestSetAudnexus(audible.URL, audnexus.URL)

	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/modules", map[string]any{"audiobooks": true}, http.StatusOK)
	audios := filepath.Join(t.TempDir(), "audio")
	bookDir := filepath.Join(audios, "A. F Kay", "Uru's Third Temple")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bookDir, "01.m4b"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/settings", map[string]any{"audiobooksPath": audios}, http.StatusOK)

	postJSON[map[string]any](t, client, base+"/api/books/import/retry", map[string]any{}, http.StatusConflict) // nothing run yet
	postJSON[map[string]any](t, client, base+"/api/books/import", map[string]any{}, http.StatusAccepted)
	waitBackground(t, server)
	st := getJSON[map[string]any](t, client, base+"/api/books/import")
	if st["summary"].(map[string]any)["unmatched"] != float64(1) {
		t.Fatalf("the first run could not find it: %v", st)
	}

	up.Store(true)
	postJSON[map[string]any](t, client, base+"/api/books/import/retry", map[string]any{}, http.StatusAccepted)
	waitBackground(t, server)
	st = getJSON[map[string]any](t, client, base+"/api/books/import")
	sum := st["summary"].(map[string]any)
	if st["phase"] != "done" || sum["imported"] != float64(1) || sum["unmatched"] != nil || st["total"] != float64(1) {
		t.Fatalf("the retry should add it: %v", st)
	}
	postJSON[map[string]any](t, client, base+"/api/books/import/retry", map[string]any{}, http.StatusConflict) // everything was found
}
