package animemap

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/store"
)

// fakeServices serves the recorded answers of the three services.
type fakeServices struct {
	xem, lists, anilist *httptest.Server
	down                atomic.Bool
	havemaps, alls      atomic.Int32
	listGets, titleGets atomic.Int32
}

func newFakeServices(t *testing.T) *fakeServices {
	t.Helper()
	f := &fakeServices{}
	f.xem = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		switch r.URL.Path {
		case "/map/havemap":
			f.havemaps.Add(1)
			w.Write(fixture(t, "xem_havemap.json"))
		case "/map/all":
			f.alls.Add(1)
			file := map[string]string{
				"267440": "xem_all_267440_attack_on_titan.json",
				"74796":  "xem_all_74796_bleach.json",
			}[r.URL.Query().Get("id")]
			if file == "" {
				w.Write(fixture(t, "xem_all_81797_unknown.json"))
				return
			}
			w.Write(fixture(t, file))
		}
	}))
	f.lists = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		f.listGets.Add(1)
		w.Write(fixture(t, "anime_list_subset.json"))
	}))
	f.anilist = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		f.titleGets.Add(1)
		w.Write(fixture(t, "anilist_titles.json"))
	}))
	t.Cleanup(func() { f.xem.Close(); f.lists.Close(); f.anilist.Close() })
	return f
}

func (f *fakeServices) service(t *testing.T, db *sql.DB) *Service {
	t.Helper()
	s := NewService(db, testUA, t.TempDir())
	s.XEM.SetBase(f.xem.URL)
	s.Lists.SetURL(f.lists.URL)
	s.AniList.SetURL(f.anilist.URL)
	return s
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func addShow(t *testing.T, db *sql.DB, tmdbID int, title string) int64 {
	t.Helper()
	sr, err := library.NewRepo(db).AddSeries(library.Series{TMDBID: tmdbID, Title: title, Monitored: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return sr.ID
}

func TestRefreshSavesTheNumberingAndTheIds(t *testing.T) {
	f := newFakeServices(t)
	db := testDB(t)
	svc := f.service(t, db)
	id := addShow(t, db, 1429, "Attack on Titan")
	show := Show{ID: id, Title: "Attack on Titan", TMDBID: 1429, TVDBID: 267440}
	ctx := context.Background()

	rec, err := svc.Refresh(ctx, show, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rec.XEMState != StateMapped || rec.ListsState != StateFound || rec.Note != "" {
		t.Fatalf("states %q %q, note %q", rec.XEMState, rec.ListsState, rec.Note)
	}
	if rec.AniDB != 9541 || rec.AniList != 16498 || rec.MAL != 16498 {
		t.Fatalf("ids %d %d %d", rec.AniDB, rec.AniList, rec.MAL)
	}
	if len(rec.Numbering.Scene) != 89 || len(rec.Numbering.Entries) != 8 {
		t.Fatalf("%d scene rows, %d entries", len(rec.Numbering.Scene), len(rec.Numbering.Entries))
	}
	hasAlias := false
	for _, a := range rec.Aliases {
		hasAlias = hasAlias || a == "Shingeki no Kyojin"
		if strings.EqualFold(a, "Attack on Titan") {
			t.Errorf("the show's own title is in its aliases")
		}
	}
	if !hasAlias {
		t.Errorf("aliases: %v", rec.Aliases)
	}

	got, ok, err := svc.Repo.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get: %v %v", ok, err)
	}
	if got.AniList != 16498 || len(got.Numbering.Entries) != 8 || len(got.Aliases) != len(rec.Aliases) || got.CheckedAt.IsZero() || got.TitlesAt.IsZero() {
		t.Fatalf("saved record: %+v", got)
	}

	// Asking again within the week reuses the names and the list of mapped
	// shows; only the show's own mapping is read again.
	if _, err := svc.Refresh(ctx, show, Options{}); err != nil {
		t.Fatal(err)
	}
	if f.titleGets.Load() != 1 || f.havemaps.Load() != 1 || f.alls.Load() != 2 {
		t.Fatalf("AniList %d, havemap %d, map/all %d", f.titleGets.Load(), f.havemaps.Load(), f.alls.Load())
	}
	// A person pressing the button reads the names again.
	if _, err := svc.Refresh(ctx, show, Options{Manual: true}); err != nil {
		t.Fatal(err)
	}
	if f.titleGets.Load() != 2 {
		t.Fatalf("AniList %d", f.titleGets.Load())
	}
}

func TestRefreshKeepsWhatItHadWhenTheServicesAreDown(t *testing.T) {
	f := newFakeServices(t)
	db := testDB(t)
	svc := f.service(t, db)
	id := addShow(t, db, 1429, "Attack on Titan")
	show := Show{ID: id, Title: "Attack on Titan", TMDBID: 1429, TVDBID: 267440}
	ctx := context.Background()
	if _, err := svc.Refresh(ctx, show, Options{}); err != nil {
		t.Fatal(err)
	}

	f.down.Store(true)
	// A fresh service has no memory of the lists: it has to ask, and cannot.
	svc2 := f.service(t, db)
	rec, err := svc2.Refresh(ctx, show, Options{Manual: true})
	if err != nil {
		t.Fatalf("being offline is not an error: %v", err)
	}
	if rec.XEMState != StateOffline || rec.ListsState != StateOffline {
		t.Fatalf("states %q %q", rec.XEMState, rec.ListsState)
	}
	if len(rec.Numbering.Scene) != 89 || len(rec.Numbering.Entries) != 8 || rec.AniList != 16498 {
		t.Fatalf("the earlier mapping should be kept: %d rows, %d entries, anilist %d", len(rec.Numbering.Scene), len(rec.Numbering.Entries), rec.AniList)
	}
	if !strings.Contains(rec.Note, "TheXEM couldn't be reached") || !strings.Contains(rec.Note, "kept") {
		t.Fatalf("note: %q", rec.Note)
	}
	if rec.Note == "" || strings.Contains(rec.Note, "http") {
		t.Fatalf("the note should be plain words: %q", rec.Note)
	}
}

func TestRefreshOfAShowNoServiceKnows(t *testing.T) {
	f := newFakeServices(t)
	db := testDB(t)
	svc := f.service(t, db)
	id := addShow(t, db, 4242, "Some Drama")
	ctx := context.Background()

	rec, err := svc.Refresh(ctx, Show{ID: id, Title: "Some Drama", TMDBID: 4242, TVDBID: 424242}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rec.XEMState != StateNone || rec.ListsState != StateNone || !rec.Numbering.Empty() || rec.AniList != 0 || rec.Note != "" {
		t.Fatalf("%+v", rec)
	}
	if f.alls.Load() != 0 {
		t.Fatal("TheXEM is only asked about shows its list names")
	}
	// A show without a TheTVDB id is not asked about at all.
	rec, err = svc.Refresh(ctx, Show{ID: id, Title: "Some Drama", TMDBID: 4242}, Options{})
	if err != nil || rec.XEMState != StateNone {
		t.Fatalf("%+v %v", rec, err)
	}
	// The button asks TheXEM about the show even when its list does not name it.
	rec, err = svc.Refresh(ctx, Show{ID: id, Title: "Some Drama", TMDBID: 4242, TVDBID: 424242}, Options{Manual: true})
	if err != nil || rec.XEMState != StateNone || f.alls.Load() != 1 {
		t.Fatalf("%+v %v, map/all asked %d times", rec, err, f.alls.Load())
	}
}
