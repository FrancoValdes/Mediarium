package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// familyMusic is a server with an administrator (admin) and a member (member),
// the music module on, and one album of three tracks on disk.
type familyMusic struct {
	*musicEnv
	admin, member *http.Client
	albumID       int64
	trackIDs      []int64
}

func newFamilyMusic(t *testing.T) *familyMusic {
	t.Helper()
	server, base, admin, member, _ := familyServer(t)
	mb, covers := newFakeMusicBrainzHits(t)
	server.TestSetMusicBrainz(mb.URL)
	root := filepath.Join(t.TempDir(), "music")
	postJSONMethod[map[string]any](t, admin, http.MethodPut, base+"/api/settings", map[string]any{"musicEnabled": true, "musicPath": root}, http.StatusOK)
	e := &musicEnv{server: server, base: base, client: admin, musicRoot: root, covers: covers}
	albumID, _, _ := albumOnDisk(t, e)
	tracks, err := server.MusicRepo.ListTracks(albumID)
	if err != nil || len(tracks) != 3 {
		t.Fatalf("tracks: %v %v", tracks, err)
	}
	f := &familyMusic{musicEnv: e, admin: admin, member: member, albumID: albumID}
	for _, tr := range tracks {
		f.trackIDs = append(f.trackIDs, tr.ID)
	}
	return f
}

func (f *familyMusic) url(format string, args ...any) string {
	return f.base + fmt.Sprintf(format, args...)
}

func titlesOf(list map[string]any) []string {
	var out []string
	for _, x := range list["tracks"].([]any) {
		out = append(out, x.(map[string]any)["title"].(string))
	}
	return out
}

func TestPlaylistsArePrivateUntilSharedAndOnlyTheOwnerChangesThem(t *testing.T) {
	f := newFamilyMusic(t)
	created := postJSON[map[string]any](t, f.admin, f.url("/api/music/playlists"), map[string]any{"name": "  Road trip ", "albumId": f.albumID}, http.StatusCreated)
	id := int64(created["id"].(float64))
	if created["name"] != "Road trip" || created["shared"] != false || created["mine"] != true || created["trackCount"] != float64(3) {
		t.Fatalf("created: %+v", created)
	}
	if got := titlesOf(created); strings.Join(got, ",") != "Opening,Middle Song,Closing Time" {
		t.Fatalf("an album is added in track order: %v", got)
	}
	one := func(c *http.Client, method, path string, body any, want int) map[string]any {
		return postJSONMethod[map[string]any](t, c, method, f.url(path, id), body, want)
	}

	// Private: the member neither lists, opens nor changes it.
	if l := getJSON[[]map[string]any](t, f.member, f.url("/api/music/playlists")); len(l) != 0 {
		t.Fatalf("member sees %v", l)
	}
	one(f.member, http.MethodGet, "/api/music/playlists/%d", nil, http.StatusNotFound)
	one(f.member, http.MethodPut, "/api/music/playlists/%d", map[string]any{"name": "Mine now"}, http.StatusNotFound)
	one(f.member, http.MethodPost, "/api/music/playlists/%d/tracks", map[string]any{"trackIds": f.trackIDs[:1]}, http.StatusNotFound)
	one(f.member, http.MethodDelete, "/api/music/playlists/%d", nil, http.StatusNotFound)

	// Shared with everyone: readable, still not changeable.
	shared := one(f.admin, http.MethodPut, "/api/music/playlists/%d", map[string]any{"shared": true}, http.StatusOK)
	if shared["shared"] != true {
		t.Fatalf("shared: %+v", shared)
	}
	l := getJSON[[]map[string]any](t, f.member, f.url("/api/music/playlists"))
	if len(l) != 1 || l[0]["mine"] != false || l[0]["shared"] != true || l[0]["owner"] == "" {
		t.Fatalf("member's list: %+v", l)
	}
	if got := one(f.member, http.MethodGet, "/api/music/playlists/%d", nil, http.StatusOK); got["mine"] != false || len(got["tracks"].([]any)) != 3 {
		t.Fatalf("member opens a shared list: %+v", got)
	}
	one(f.member, http.MethodPut, "/api/music/playlists/%d", map[string]any{"name": "Mine now"}, http.StatusNotFound)
	one(f.member, http.MethodPut, "/api/music/playlists/%d/order", map[string]any{"entryIds": []int{1, 2, 3}}, http.StatusNotFound)
	one(f.member, http.MethodDelete, "/api/music/playlists/%d", nil, http.StatusNotFound)

	// The member's own list is not visible to the administrator.
	postJSON[map[string]any](t, f.member, f.url("/api/music/playlists"), map[string]any{"name": "Sam's secret", "trackIds": f.trackIDs[:2]}, http.StatusCreated)
	if l := getJSON[[]map[string]any](t, f.admin, f.url("/api/music/playlists")); len(l) != 1 {
		t.Fatalf("the administrator must not see a private list of someone else: %+v", l)
	}

	// Switching sharing off hides it again; deleting removes it.
	one(f.admin, http.MethodPut, "/api/music/playlists/%d", map[string]any{"shared": false}, http.StatusOK)
	if l := getJSON[[]map[string]any](t, f.member, f.url("/api/music/playlists")); len(l) != 1 || l[0]["name"] != "Sam's secret" {
		t.Fatalf("after unsharing: %+v", l)
	}
	req, _ := http.NewRequest(http.MethodDelete, f.url("/api/music/playlists/%d", id), nil)
	if resp, err := f.admin.Do(req); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %v %v", resp, err)
	}
	one(f.admin, http.MethodGet, "/api/music/playlists/%d", nil, http.StatusNotFound)
}

func TestPlaylistAddRemoveReorderAndValidation(t *testing.T) {
	f := newFamilyMusic(t)
	for _, tc := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"no name", map[string]any{"name": "  "}, http.StatusBadRequest},
		{"unknown album", map[string]any{"name": "x", "albumId": 99999}, http.StatusNotFound},
		{"unknown artist", map[string]any{"name": "x", "artistId": 99999}, http.StatusNotFound},
	} {
		got := postJSON[map[string]any](t, f.admin, f.url("/api/music/playlists"), tc.body, tc.want)
		if got["error"] == nil || got["error"] == "" {
			t.Errorf("%s: no message in %+v", tc.name, got)
		}
	}
	// An album with nothing downloaded has nothing to add.
	if err := f.server.MusicRepo.SetTrackFile(f.trackIDs[0], ""); err != nil {
		t.Fatal(err)
	}
	if err := f.server.MusicRepo.SetTrackFile(f.trackIDs[1], ""); err != nil {
		t.Fatal(err)
	}
	if err := f.server.MusicRepo.SetTrackFile(f.trackIDs[2], ""); err != nil {
		t.Fatal(err)
	}
	postJSON[map[string]any](t, f.admin, f.url("/api/music/playlists"), map[string]any{"name": "x", "albumId": f.albumID}, http.StatusBadRequest)
	if l := getJSON[[]map[string]any](t, f.admin, f.url("/api/music/playlists")); len(l) != 0 {
		t.Fatalf("a refused create must not leave a list behind: %+v", l)
	}

	p := postJSON[map[string]any](t, f.admin, f.url("/api/music/playlists"), map[string]any{"name": "Mix", "trackIds": []int64{f.trackIDs[2], f.trackIDs[0]}}, http.StatusCreated)
	id := int64(p["id"].(float64))
	added := postJSON[map[string]any](t, f.admin, f.url("/api/music/playlists/%d/tracks", id), map[string]any{"trackIds": []int64{f.trackIDs[1]}}, http.StatusOK)
	list := added["playlist"].(map[string]any)
	if added["added"] != float64(1) || strings.Join(titlesOf(list), ",") != "Closing Time,Opening,Middle Song" {
		t.Fatalf("add: %+v", added)
	}
	postJSON[map[string]any](t, f.admin, f.url("/api/music/playlists/%d/tracks", id), map[string]any{}, http.StatusBadRequest)

	var entries []int64
	for _, x := range list["tracks"].([]any) {
		entries = append(entries, int64(x.(map[string]any)["entryId"].(float64)))
	}
	reordered := postJSONMethod[map[string]any](t, f.admin, http.MethodPut, f.url("/api/music/playlists/%d/order", id), map[string]any{"entryIds": []int64{entries[2], entries[0], entries[1]}}, http.StatusOK)
	if strings.Join(titlesOf(reordered), ",") != "Middle Song,Closing Time,Opening" {
		t.Fatalf("reorder: %v", titlesOf(reordered))
	}
	postJSONMethod[map[string]any](t, f.admin, http.MethodPut, f.url("/api/music/playlists/%d/order", id), map[string]any{"entryIds": []int64{entries[0]}}, http.StatusBadRequest)

	req, _ := http.NewRequest(http.MethodDelete, f.url("/api/music/playlists/%d/tracks/%d", id, entries[0]), nil)
	resp, err := f.admin.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("remove entry: %v %v", resp, err)
	}
	var after map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&after)
	resp.Body.Close()
	if strings.Join(titlesOf(after), ",") != "Middle Song,Opening" {
		t.Fatalf("after remove: %v", titlesOf(after))
	}
	// A track that is not downloaded is kept on the list and flagged.
	if after["tracks"].([]any)[0].(map[string]any)["hasFile"] != false {
		t.Fatalf("a track without a file must say so: %+v", after["tracks"])
	}
}

func TestArtistTopTracksUseListenBrainzPopularityThenTheLibraryOrder(t *testing.T) {
	f := newFamilyMusic(t)
	var asked atomic.Int32
	lb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/1/popularity/top-recordings-for-artist/11111111-1111-4111-8111-111111111111") {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`[{"recording_name":"Closing Time","total_listen_count":90},{"recording_name":"Not In My Library","total_listen_count":80},{"recording_name":"opening","total_listen_count":10}]`))
	}))
	t.Cleanup(lb.Close)
	f.server.TestSetListenBrainz(lb.URL)
	artists := getJSON[[]map[string]any](t, f.admin, f.url("/api/music/artists"))
	artistID := int64(artists[0]["id"].(float64))

	p := postJSON[map[string]any](t, f.admin, f.url("/api/music/playlists"), map[string]any{"name": "Best of", "artistId": artistID}, http.StatusCreated)
	if got := strings.Join(titlesOf(p), ","); got != "Closing Time,Opening" {
		t.Fatalf("most listened first, only what is in the library: %v", got)
	}

	// ListenBrainz down: the artist's tracks in library order instead.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(down.Close)
	f.server.TestSetListenBrainz(down.URL)
	p = postJSON[map[string]any](t, f.admin, f.url("/api/music/playlists"), map[string]any{"name": "Fallback", "artistId": artistID}, http.StatusCreated)
	if got := strings.Join(titlesOf(p), ","); got != "Opening,Middle Song,Closing Time" {
		t.Fatalf("fallback: %v", got)
	}
}

func TestPlayerRoutesAnswer404WhileTheModuleIsOff(t *testing.T) {
	_, base, client := loginNewServer(t)
	for _, path := range []string{"/api/music/playlists", "/api/music/playlists/1", "/api/music/tracks/1/lyrics", "/api/music/tracks/1/gain", "/api/music/listenbrainz", "/api/music/player-settings"} {
		if status, _ := doStatus(t, client, http.MethodGet, base+path); status != http.StatusNotFound {
			t.Errorf("GET %s with music off: %d", path, status)
		}
	}
}

// ---- lyrics and ReplayGain ----

func TestLyricsFromASidecarTheTagsAndLRCLIB(t *testing.T) {
	f := newFamilyMusic(t)
	var hits atomic.Int32
	var lastQuery atomic.Value
	lr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		lastQuery.Store(r.URL.RawQuery)
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "Mediarium/") {
			t.Errorf("LRCLIB request without the identifying User-Agent: %q", ua)
		}
		if r.URL.Path == "/api/get" && r.URL.Query().Get("track_name") == "Middle Song" {
			w.Write([]byte(`{"duration":200,"instrumental":false,"plainLyrics":"online words","syncedLyrics":"[00:01.00]online\n[00:02.00]words"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":404,"name":"TrackNotFound","message":"Failed to find specified track"}`))
	}))
	t.Cleanup(lr.Close)
	f.server.TestSetLRCLIB(lr.URL)
	lyrics := func(i int) map[string]any {
		return getJSON[map[string]any](t, f.member, f.url("/api/music/tracks/%d/lyrics", f.trackIDs[i]))
	}

	// 1. A .lrc beside the file wins, and nothing is asked online.
	tracks, _ := f.server.MusicRepo.ListTracks(f.albumID)
	sidecar := strings.TrimSuffix(tracks[0].FilePath, ".flac") + ".lrc"
	if err := os.WriteFile(sidecar, []byte("[00:01.00]from the sidecar\n[00:03.00]second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := lyrics(0)
	if got["source"] != "sidecar" || !strings.Contains(got["synced"].(string), "from the sidecar") || hits.Load() != 0 {
		t.Fatalf("sidecar: %+v hits %d", got, hits.Load())
	}

	// 2. Online: asked once with track, artist, album and length; then from the database.
	got = lyrics(1)
	if got["source"] != "lrclib" || !strings.Contains(got["synced"].(string), "online") || got["plain"] != "online words" || got["online"] != true {
		t.Fatalf("online: %+v", got)
	}
	q := lastQuery.Load().(string)
	for _, want := range []string{"track_name=Middle+Song", "artist_name=Fixture+Band", "album_name=First+Album"} {
		if !strings.Contains(q, want) {
			t.Errorf("LRCLIB query %q lacks %s", q, want)
		}
	}
	first := hits.Load()
	lyrics(1)
	if hits.Load() != first {
		t.Fatalf("a second look-up asked LRCLIB again (%d -> %d)", first, hits.Load())
	}

	// 3. Nothing found is remembered too, and answers an empty result.
	got = lyrics(2)
	if got["source"] != "" || got["synced"] != "" || got["plain"] != "" {
		t.Fatalf("not found: %+v", got)
	}
	asked := hits.Load()
	lyrics(2)
	if hits.Load() != asked {
		t.Fatal("a miss must be remembered")
	}

	// 4. Plain words in the tags are kept, and synced ones found online replace them.
	plainFile := flacFile(44100, 16, 200, "TITLE=x", "UNSYNCEDLYRICS=words from the file")
	if err := os.WriteFile(tracks[2].FilePath, plainFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lyrics(2); got["plain"] != "words from the file" || got["source"] != "file" {
		t.Fatalf("embedded plain: %+v", got)
	}
	if err := os.WriteFile(tracks[1].FilePath, plainFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lyrics(1); got["plain"] != "words from the file" || !strings.Contains(got["synced"].(string), "online") || got["source"] != "lrclib" {
		t.Fatalf("file words plus online times: %+v", got)
	}

	// 5. The switch: with the online look-up off nothing is asked, and only an administrator can change it.
	postJSONMethod[map[string]any](t, f.member, http.MethodPut, f.url("/api/music/player-settings"), map[string]any{"lyricsOnline": false}, http.StatusForbidden)
	postJSONMethod[map[string]any](t, f.admin, http.MethodPut, f.url("/api/music/player-settings"), map[string]any{"lyricsOnline": false}, http.StatusOK)
	if s := getJSON[map[string]any](t, f.member, f.url("/api/music/player-settings")); s["lyricsOnline"] != false {
		t.Fatalf("settings: %+v", s)
	}
	before := hits.Load()
	if err := os.WriteFile(tracks[1].FilePath, flacFile(44100, 16, 200, "TITLE=x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lyrics(1); got["synced"] != "" || got["online"] != false || hits.Load() != before {
		t.Fatalf("online off: %+v hits %d->%d", got, before, hits.Load())
	}
}

func TestLyricsCanBeTriedAgainAfterLRCLIBFailed(t *testing.T) {
	f := newFamilyMusic(t)
	var fail atomic.Bool
	fail.Store(true)
	lr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"plainLyrics":"back again","syncedLyrics":null}`))
	}))
	t.Cleanup(lr.Close)
	f.server.TestSetLRCLIB(lr.URL)
	path := f.url("/api/music/tracks/%d/lyrics", f.trackIDs[0])
	if got := getJSON[map[string]any](t, f.member, path); got["plain"] != "" {
		t.Fatalf("while down: %+v", got)
	}
	fail.Store(false)
	if got := getJSON[map[string]any](t, f.member, path); got["plain"] != "back again" {
		t.Fatalf("an outage must not be remembered as 'no lyrics': %+v", got)
	}
}

func TestTrackGainReadsReplayGainTags(t *testing.T) {
	f := newFamilyMusic(t)
	tracks, _ := f.server.MusicRepo.ListTracks(f.albumID)
	data := flacFile(44100, 16, 200, "TITLE=x", "REPLAYGAIN_TRACK_GAIN=-7.5 dB", "REPLAYGAIN_TRACK_PEAK=0.95", "REPLAYGAIN_ALBUM_GAIN=-6.1 dB", "REPLAYGAIN_ALBUM_PEAK=1.0")
	if err := os.WriteFile(tracks[0].FilePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got := getJSON[map[string]any](t, f.member, f.url("/api/music/tracks/%d/gain", f.trackIDs[0]))
	if got["trackGain"] != -7.5 || got["trackPeak"] != 0.95 || got["albumGain"] != -6.1 || got["albumPeak"] != 1.0 {
		t.Fatalf("gain: %+v", got)
	}
	none := getJSON[map[string]any](t, f.member, f.url("/api/music/tracks/%d/gain", f.trackIDs[1]))
	if v, has := none["trackGain"]; !has || v != nil {
		t.Fatalf("a file without the tags answers null: %+v", none)
	}
	// Not downloaded, unknown and bad ids.
	if err := f.server.MusicRepo.SetTrackFile(f.trackIDs[2], ""); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		fmt.Sprintf("/api/music/tracks/%d/gain", f.trackIDs[2]):   http.StatusNotFound,
		fmt.Sprintf("/api/music/tracks/%d/lyrics", f.trackIDs[2]): http.StatusNotFound,
		"/api/music/tracks/999999/gain":                           http.StatusNotFound,
		"/api/music/tracks/abc/lyrics":                            http.StatusBadRequest,
	} {
		if status, _ := doStatus(t, f.member, http.MethodGet, f.base+path); status != want {
			t.Errorf("GET %s: %d, want %d", path, status, want)
		}
	}
}

// ---- ListenBrainz ----

type fakeLB struct {
	mu       sync.Mutex
	srv      *httptest.Server
	down     bool
	valid    bool
	requests []map[string]any
	tokens   []string
}

func newFakeLB(t *testing.T) *fakeLB {
	t.Helper()
	f := &fakeLB{valid: true}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.down {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/1/validate-token":
			w.Write([]byte(fmt.Sprintf(`{"code":200,"message":"x","valid":%v,"user_name":"ryan_lb"}`, f.valid)))
		case "/1/submit-listens":
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			f.requests = append(f.requests, m)
			f.tokens = append(f.tokens, r.Header.Get("Authorization"))
			w.Write([]byte(`{"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLB) setDown(v bool) { f.mu.Lock(); f.down = v; f.mu.Unlock() }

func (f *fakeLB) types() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		out = append(out, fmt.Sprintf("%v:%d", r["listen_type"], len(r["payload"].([]any))))
	}
	return out
}

func TestScrobblingToListenBrainz(t *testing.T) {
	f := newFamilyMusic(t)
	lb := newFakeLB(t)
	f.server.TestSetScrobbleServer(lb.srv.URL)
	mine := f.url("/api/music/listenbrainz")
	const fakeToken = "11111111-2222-3333-4444-555555555555"

	if st := getJSON[map[string]any](t, f.member, mine); st["connected"] != false {
		t.Fatalf("before connecting: %+v", st)
	}
	// Nothing is sent until an account connects.
	if got := postJSON[map[string]any](t, f.member, mine+"/playing", map[string]any{"trackId": f.trackIDs[0]}, http.StatusOK); got["sent"] != false {
		t.Fatalf("not connected: %+v", got)
	}

	// A token ListenBrainz does not know is refused; an empty one too.
	lb.mu.Lock()
	lb.valid = false
	lb.mu.Unlock()
	postJSONMethod[map[string]any](t, f.member, http.MethodPut, mine, map[string]any{"token": fakeToken}, http.StatusBadRequest)
	postJSONMethod[map[string]any](t, f.member, http.MethodPut, mine, map[string]any{"token": "  "}, http.StatusBadRequest)
	lb.mu.Lock()
	lb.valid = true
	lb.mu.Unlock()
	st := postJSONMethod[map[string]any](t, f.member, http.MethodPut, mine, map[string]any{"token": " " + fakeToken + " "}, http.StatusOK)
	if st["connected"] != true || st["user"] != "ryan_lb" || st["enabled"] != true {
		t.Fatalf("connected: %+v", st)
	}
	raw, _ := json.Marshal(getJSON[map[string]any](t, f.member, mine))
	if strings.Contains(string(raw), fakeToken) || strings.Contains(string(raw), "token") {
		t.Fatalf("the token must never be sent back: %s", raw)
	}
	// The administrator's own connection is separate.
	if st := getJSON[map[string]any](t, f.admin, mine); st["connected"] != false {
		t.Fatalf("one account's token leaked to another: %+v", st)
	}

	// Playing now, then a counted listen.
	if got := postJSON[map[string]any](t, f.member, mine+"/playing", map[string]any{"trackId": f.trackIDs[1]}, http.StatusOK); got["sent"] != true {
		t.Fatalf("playing now: %+v", got)
	}
	now := unixNow()
	if got := postJSON[map[string]any](t, f.member, mine+"/listen", map[string]any{"trackId": f.trackIDs[1], "listenedAt": now - 120}, http.StatusOK); got["sent"] != true || got["pending"] != float64(0) {
		t.Fatalf("listen: %+v", got)
	}
	lb.mu.Lock()
	first := lb.requests[0]["payload"].([]any)[0].(map[string]any)["track_metadata"].(map[string]any)
	second := lb.requests[1]["payload"].([]any)[0].(map[string]any)
	tokens := append([]string(nil), lb.tokens...)
	lb.mu.Unlock()
	if tokens[0] != "Token "+fakeToken || first["track_name"] != "Middle Song" || first["artist_name"] != "Fixture Band" || first["release_name"] != "First Album" {
		t.Fatalf("what was sent: %v %+v", tokens, first)
	}
	if _, has := lb.requests[0]["payload"].([]any)[0].(map[string]any)["listened_at"]; has {
		t.Error("playing_now carried a time")
	}
	if second["listened_at"] != float64(now-120) || lb.requests[1]["listen_type"] != "single" {
		t.Fatalf("listen payload: %+v", lb.requests[1])
	}

	// ListenBrainz down: plays are kept, then sent together, oldest first, when it is back.
	lb.setDown(true)
	for i, id := range []int64{f.trackIDs[2], f.trackIDs[0]} {
		got := postJSON[map[string]any](t, f.member, mine+"/listen", map[string]any{"trackId": id, "listenedAt": now - 90 + int64(i)}, http.StatusOK)
		if got["sent"] != false || got["pending"] != float64(i+1) {
			t.Fatalf("while down: %+v", got)
		}
	}
	if st := getJSON[map[string]any](t, f.member, mine); st["pending"] != float64(2) || st["lastError"] == nil {
		t.Fatalf("status while down: %+v", st)
	}
	lb.setDown(false)
	before := len(lb.types())
	postJSON[map[string]any](t, f.member, mine+"/listen", map[string]any{"trackId": f.trackIDs[1], "listenedAt": now - 30}, http.StatusOK)
	lb.mu.Lock()
	batch := lb.requests[before]["payload"].([]any)
	lb.mu.Unlock()
	if lb.requests[before]["listen_type"] != "import" || len(batch) != 3 || batch[0].(map[string]any)["track_metadata"].(map[string]any)["track_name"] != "Closing Time" ||
		batch[2].(map[string]any)["track_metadata"].(map[string]any)["track_name"] != "Middle Song" {
		t.Fatalf("kept plays: %+v", batch)
	}
	if st := getJSON[map[string]any](t, f.member, mine); st["pending"] != float64(0) || st["submitted"] != float64(4) || st["lastError"] != nil {
		t.Fatalf("after catching up: %+v", st)
	}

	// Bad times and tracks are refused.
	for name, body := range map[string]map[string]any{
		"future":  {"trackId": f.trackIDs[0], "listenedAt": now + 3600},
		"ancient": {"trackId": f.trackIDs[0], "listenedAt": now - 90*86400},
		"no time": {"trackId": f.trackIDs[0]},
		"no id":   {"listenedAt": now - 5},
	} {
		if status, _ := playerPost(t, f.member, mine+"/listen", body); status != http.StatusBadRequest {
			t.Errorf("%s: %d", name, status)
		}
	}
	if status, _ := playerPost(t, f.member, mine+"/listen", map[string]any{"trackId": 424242, "listenedAt": now - 5}); status != http.StatusNotFound {
		t.Errorf("unknown track: %d", status)
	}

	// The switch stops sending; forgetting removes the token.
	postJSONMethod[map[string]any](t, f.member, http.MethodPut, mine, map[string]any{"enabled": false}, http.StatusOK)
	n := len(lb.types())
	if got := postJSON[map[string]any](t, f.member, mine+"/listen", map[string]any{"trackId": f.trackIDs[0], "listenedAt": now - 5}, http.StatusOK); got["sent"] != false || len(lb.types()) != n {
		t.Fatalf("switched off: %+v", got)
	}
	postJSONMethod[map[string]any](t, f.member, http.MethodPut, mine, map[string]any{"enabled": true}, http.StatusOK)
	req, _ := http.NewRequest(http.MethodDelete, mine, nil)
	if resp, err := f.member.Do(req); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("forget: %v %v", resp, err)
	}
	if st := getJSON[map[string]any](t, f.member, mine); st["connected"] != false {
		t.Fatalf("after forgetting: %+v", st)
	}
	postJSONMethod[map[string]any](t, f.member, http.MethodPut, mine, map[string]any{"enabled": true}, http.StatusNotFound)
}

func TestScrobblingStopsWhenTheTokenIsRefused(t *testing.T) {
	f := newFamilyMusic(t)
	lb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/1/validate-token" {
			w.Write([]byte(`{"valid":true,"user_name":"u"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":401,"error":"Invalid authorization token."}`))
	}))
	t.Cleanup(lb.Close)
	f.server.TestSetScrobbleServer(lb.URL)
	mine := f.url("/api/music/listenbrainz")
	postJSONMethod[map[string]any](t, f.member, http.MethodPut, mine, map[string]any{"token": "tok"}, http.StatusOK)
	postJSON[map[string]any](t, f.member, mine+"/playing", map[string]any{"trackId": f.trackIDs[0]}, http.StatusOK)
	st := getJSON[map[string]any](t, f.member, mine)
	if st["enabled"] != false || !strings.Contains(fmt.Sprint(st["lastError"]), "no longer accepts") {
		t.Fatalf("a refused token must switch sending off and say why: %+v", st)
	}
}

func unixNow() int64 { return time.Now().Unix() }

// rawPost posts JSON and returns the status without judging it.
func playerPost(t *testing.T, c *http.Client, url string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := c.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
