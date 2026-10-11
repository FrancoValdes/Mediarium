package music_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/music"
	"github.com/rdborg/mediarium/internal/store"
)

type playlistEnv struct {
	db     *sql.DB
	repo   *music.Repo
	tracks []music.Track // five tracks of one album, the last without a file
	album  music.Album
	ann    int64
	bob    int64
}

func newPlaylistEnv(t *testing.T) *playlistEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	e := &playlistEnv{db: db, repo: music.NewRepo(db)}
	for _, name := range []string{"ann", "bob"} {
		res, err := db.Exec(`INSERT INTO users (username, password_hash) VALUES (?, 'x')`, name)
		if err != nil {
			t.Fatalf("add user: %v", err)
		}
		id, _ := res.LastInsertId()
		if name == "ann" {
			e.ann = id
		} else {
			e.bob = id
		}
	}
	_, albums, err := e.repo.AddArtist(music.Artist{MBID: "mb-a", Name: "Artist"}, []music.Album{{MBID: "rg-1", Title: "Album One", ReleaseDate: "2001"}})
	if err != nil {
		t.Fatalf("add artist: %v", err)
	}
	e.album = albums[0]
	var tr []music.Track
	for i := 1; i <= 5; i++ {
		tr = append(tr, music.Track{Disc: 1, Position: i, Title: "Song " + string(rune('A'+i-1)), LengthMs: 60000})
	}
	if err := e.repo.ReplaceTracks(e.album.ID, tr); err != nil {
		t.Fatal(err)
	}
	e.tracks, _ = e.repo.ListTracks(e.album.ID)
	for _, tk := range e.tracks[:4] {
		if err := e.repo.SetTrackFile(tk.ID, "/music/x/"+tk.Title+".flac"); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *playlistEnv) ids() []int64 {
	var out []int64
	for _, t := range e.tracks {
		out = append(out, t.ID)
	}
	return out
}

func entryTitles(t *testing.T, r *music.Repo, id int64) []string {
	t.Helper()
	es, err := r.PlaylistEntries(id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Title)
	}
	return out
}

func TestPlaylistCreateRenameAndShare(t *testing.T) {
	e := newPlaylistEnv(t)
	p, err := e.repo.CreatePlaylist(e.ann, "  Road   trip  ", false)
	if err != nil || p.Name != "Road trip" || p.Shared || p.UserID != e.ann {
		t.Fatalf("create: %+v %v", p, err)
	}
	if _, err := e.repo.CreatePlaylist(e.ann, "   ", false); err == nil {
		t.Fatal("an empty name must be refused")
	}
	long := strings.Repeat("é", 300)
	if q, _ := e.repo.CreatePlaylist(e.ann, long, false); len([]rune(q.Name)) != music.MaxPlaylistNameLen {
		t.Fatalf("long name kept %d characters", len([]rune(q.Name)))
	}

	// Private: Bob cannot see, rename, share, fill or delete it.
	if _, err := e.repo.GetPlaylist(p.ID, e.bob); !errors.Is(err, music.ErrPlaylistNotFound) {
		t.Fatalf("bob reads a private list: %v", err)
	}
	name, yes := "Hijacked", true
	for label, err := range map[string]error{
		"rename": e.repo.UpdatePlaylist(p.ID, e.bob, &name, nil),
		"share":  e.repo.UpdatePlaylist(p.ID, e.bob, nil, &yes),
		"delete": e.repo.DeletePlaylist(p.ID, e.bob),
	} {
		if !errors.Is(err, music.ErrPlaylistNotFound) {
			t.Errorf("bob %s: %v", label, err)
		}
	}
	if _, err := e.repo.AddToPlaylist(p.ID, e.bob, e.ids()[:1]); !errors.Is(err, music.ErrPlaylistNotFound) {
		t.Errorf("bob adds: %v", err)
	}

	// Shared: Bob can read it but still not change it.
	if err := e.repo.UpdatePlaylist(p.ID, e.ann, nil, &yes); err != nil {
		t.Fatal(err)
	}
	if got, err := e.repo.GetPlaylist(p.ID, e.bob); err != nil || !got.Shared {
		t.Fatalf("bob reads a shared list: %+v %v", got, err)
	}
	if err := e.repo.UpdatePlaylist(p.ID, e.bob, &name, nil); !errors.Is(err, music.ErrPlaylistNotFound) {
		t.Errorf("bob renames a shared list: %v", err)
	}
	listed, _ := e.repo.ListPlaylists(e.bob)
	if len(listed) != 1 || listed[0].ID != p.ID {
		t.Fatalf("bob's lists: %+v", listed)
	}
	mine, _ := e.repo.ListPlaylists(e.ann)
	if len(mine) != 2 {
		t.Fatalf("ann's lists: %d", len(mine))
	}

	if err := e.repo.DeletePlaylist(p.ID, e.ann); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.GetPlaylist(p.ID, e.ann); !errors.Is(err, music.ErrPlaylistNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestPlaylistAddRemoveReorder(t *testing.T) {
	e := newPlaylistEnv(t)
	p, _ := e.repo.CreatePlaylist(e.ann, "Mix", false)
	ids := e.ids()

	// Order given is the order kept; an unknown id is skipped; a track can be on twice.
	n, err := e.repo.AddToPlaylist(p.ID, e.ann, []int64{ids[2], ids[0], 999999, ids[2]})
	if err != nil || n != 3 {
		t.Fatalf("add: %d %v", n, err)
	}
	if got := entryTitles(t, e.repo, p.ID); !reflect.DeepEqual(got, []string{"Song C", "Song A", "Song C"}) {
		t.Fatalf("titles %v", got)
	}
	if _, err := e.repo.AddToPlaylist(p.ID, e.ann, []int64{ids[1]}); err != nil {
		t.Fatal(err)
	}
	es, _ := e.repo.PlaylistEntries(p.ID)
	if len(es) != 4 || es[3].Title != "Song B" || es[3].Artist != "Artist" || es[3].Album != "Album One" {
		t.Fatalf("entries %+v", es)
	}

	// Reorder: reverse. It must name every entry exactly once.
	rev := []int64{es[3].EntryID, es[2].EntryID, es[1].EntryID, es[0].EntryID}
	if err := e.repo.ReorderPlaylist(p.ID, e.ann, rev); err != nil {
		t.Fatal(err)
	}
	if got := entryTitles(t, e.repo, p.ID); !reflect.DeepEqual(got, []string{"Song B", "Song C", "Song A", "Song C"}) {
		t.Fatalf("after reorder %v", got)
	}
	for name, bad := range map[string][]int64{
		"short":     rev[:3],
		"duplicate": {rev[0], rev[0], rev[1], rev[2]},
		"stranger":  {rev[0], rev[1], rev[2], 424242},
		"empty":     nil,
	} {
		if err := e.repo.ReorderPlaylist(p.ID, e.ann, bad); !errors.Is(err, music.ErrBadOrder) {
			t.Errorf("%s order: %v", name, err)
		}
	}
	if err := e.repo.ReorderPlaylist(p.ID, e.bob, rev); !errors.Is(err, music.ErrPlaylistNotFound) {
		t.Errorf("bob reorders: %v", err)
	}

	// Remove one of the two copies of Song C; the other stays.
	if err := e.repo.RemoveFromPlaylist(p.ID, e.ann, rev[1]); err != nil {
		t.Fatal(err)
	}
	if got := entryTitles(t, e.repo, p.ID); !reflect.DeepEqual(got, []string{"Song B", "Song A", "Song C"}) {
		t.Fatalf("after remove %v", got)
	}
	if err := e.repo.RemoveFromPlaylist(p.ID, e.ann, rev[1]); !errors.Is(err, music.ErrPlaylistNotFound) {
		t.Errorf("removing twice: %v", err)
	}
	if err := e.repo.RemoveFromPlaylist(p.ID, e.bob, rev[0]); !errors.Is(err, music.ErrPlaylistNotFound) {
		t.Errorf("bob removes: %v", err)
	}

	// The summary counts and a cover mosaic.
	got, _ := e.repo.GetPlaylist(p.ID, e.ann)
	if got.TrackCount != 3 || got.DurationMs != 180000 {
		t.Fatalf("summary %+v", got)
	}
	l, _ := e.repo.ListPlaylists(e.ann)
	if len(l) != 1 || !reflect.DeepEqual(l[0].AlbumIDs, []int64{e.album.ID}) {
		t.Fatalf("albums on the list: %+v", l)
	}
}

func TestPlaylistEntriesGoWhenTheTrackOrAccountGoes(t *testing.T) {
	e := newPlaylistEnv(t)
	p, _ := e.repo.CreatePlaylist(e.ann, "Mix", true)
	if _, err := e.repo.AddToPlaylist(p.ID, e.ann, e.ids()[:3]); err != nil {
		t.Fatal(err)
	}
	// An album's tracks are re-saved on every refresh; the ids (and so the list) survive.
	if err := e.repo.ReplaceTracks(e.album.ID, []music.Track{{Disc: 1, Position: 1, Title: "Song A renamed", LengthMs: 1}, {Disc: 1, Position: 2, Title: "Song B", LengthMs: 1}}); err != nil {
		t.Fatal(err)
	}
	// Song C no longer exists on the album, so it leaves the list.
	if got := entryTitles(t, e.repo, p.ID); !reflect.DeepEqual(got, []string{"Song A renamed", "Song B"}) {
		t.Fatalf("after the tracklist changed: %v", got)
	}
	if _, err := e.db.Exec(`DELETE FROM users WHERE id = ?`, e.ann); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = e.db.QueryRow(`SELECT (SELECT COUNT(*) FROM music_playlists) + (SELECT COUNT(*) FROM music_playlist_tracks)`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows left after the account was deleted", n)
	}
}

func TestPlaylistLimit(t *testing.T) {
	e := newPlaylistEnv(t)
	p, _ := e.repo.CreatePlaylist(e.ann, "Big", false)
	ids := make([]int64, music.MaxPlaylistTracks+1)
	for i := range ids {
		ids[i] = e.ids()[0]
	}
	if _, err := e.repo.AddToPlaylist(p.ID, e.ann, ids); !errors.Is(err, music.ErrPlaylistFull) {
		t.Fatalf("too many: %v", err)
	}
	if n, err := e.repo.AddToPlaylist(p.ID, e.ann, ids[:music.MaxPlaylistTracks]); err != nil || n != music.MaxPlaylistTracks {
		t.Fatalf("exactly the limit: %d %v", n, err)
	}
	if _, err := e.repo.AddToPlaylist(p.ID, e.ann, ids[:1]); !errors.Is(err, music.ErrPlaylistFull) {
		t.Fatalf("one more: %v", err)
	}
}

func TestPlayableSelections(t *testing.T) {
	e := newPlaylistEnv(t)
	got, err := e.repo.PlayableTrackIDs(e.album.ID)
	if err != nil || len(got) != 4 || got[0] != e.tracks[0].ID {
		t.Fatalf("album playable: %v %v", got, err)
	}
	artist, _, _ := e.repo.GetArtistByMBID("mb-a")
	tracks, err := e.repo.ArtistPlayableTracks(artist.ID)
	if err != nil || len(tracks) != 4 {
		t.Fatalf("artist playable: %v %v", tracks, err)
	}
}
