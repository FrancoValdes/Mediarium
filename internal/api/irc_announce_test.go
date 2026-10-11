package api_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/announce"
	"github.com/rdborg/mediarium/internal/api"
	"github.com/rdborg/mediarium/internal/library"
)

// miniIRC is a very small IRC server: it welcomes a client, lets it join
// channels and passes on what the test says in them, as any nick.
type miniIRC struct {
	ln    net.Listener
	mu    sync.Mutex
	conns []*miniConn
}

type miniConn struct {
	c    net.Conn
	mu   sync.Mutex
	nick string
	in   map[string]bool
}

func newMiniIRC(t *testing.T) *miniIRC {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &miniIRC{ln: ln}
	t.Cleanup(func() {
		ln.Close()
		m.mu.Lock()
		for _, c := range m.conns {
			c.c.Close()
		}
		m.mu.Unlock()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mc := &miniConn{c: c, in: map[string]bool{}}
			m.mu.Lock()
			m.conns = append(m.conns, mc)
			m.mu.Unlock()
			go mc.serve()
		}
	}()
	return m
}

func (m *miniIRC) port() int { return m.ln.Addr().(*net.TCPAddr).Port }

func (c *miniConn) write(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.c.Write([]byte(s + "\r\n"))
}

func (c *miniConn) serve() {
	r := bufio.NewReader(c.c)
	user := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd, rest, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		switch strings.ToUpper(cmd) {
		case "NICK":
			c.mu.Lock()
			c.nick = rest
			c.mu.Unlock()
		case "USER":
			user = true
		case "JOIN":
			ch, _, _ := strings.Cut(rest, " ")
			c.mu.Lock()
			c.in[strings.ToLower(ch)] = true
			c.mu.Unlock()
			c.write(":" + c.nick + "!u@h JOIN " + ch)
			c.write(":irc.test 366 " + c.nick + " " + ch + " :End of /NAMES list.")
		case "PING":
			c.write(":irc.test PONG irc.test :" + strings.TrimPrefix(rest, ":"))
		}
		c.mu.Lock()
		ready := user && c.nick != ""
		c.mu.Unlock()
		if ready && strings.ToUpper(cmd) == "USER" {
			c.write(":irc.test 001 " + c.nick + " :Welcome")
		}
	}
}

// joined reports whether a client is in the channel.
func (m *miniIRC) joined(ch string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		c.mu.Lock()
		in := c.in[strings.ToLower(ch)]
		c.mu.Unlock()
		if in {
			return true
		}
	}
	return false
}

func (m *miniIRC) say(from, ch, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		c.write(fmt.Sprintf(":%s!u@h PRIVMSG %s :%s", from, ch, text))
	}
}

// ircEnv is a signed-in server, a tracker that holds downloads, and a fake IRC network.
type ircEnv struct {
	server  *api.Server
	base    string
	client  *http.Client
	irc     *miniIRC
	tracker *httptest.Server
	release func()

	mu       sync.Mutex
	requests []string // what the tracker was asked for
}

const ircPasskey = "SECRETPASSKEY0123456789"

func newIRCEnv(t *testing.T) *ircEnv {
	t.Helper()
	server, httpSrv, client := newHuntTestServer(t)
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/onboarding/admin", map[string]string{
		"username": "ryan", "password": "correct-horse-battery-staple", "firstName": "Ryan", "lastName": "Tester",
	}, http.StatusCreated)
	e := &ircEnv{server: server, base: httpSrv.URL, client: client, irc: newMiniIRC(t)}
	gate := make(chan struct{})
	var once sync.Once
	e.release = func() { once.Do(func() { close(gate) }) }
	e.tracker = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.requests = append(e.requests, r.URL.RequestURI())
		e.mu.Unlock()
		<-gate
		w.WriteHeader(http.StatusNotFound) // ends the download without blaming the release
	}))
	t.Cleanup(e.tracker.Close)
	t.Cleanup(e.release)
	server.TestAnnounceTweak(func(c *announce.Config) {
		c.AllowPrivate = true
		c.BackoffMin, c.BackoffMax = 20*time.Millisecond, 100*time.Millisecond
	})
	t.Cleanup(server.TestStopAnnounce)
	return e
}

// connect saves a connection to the fake network and starts it.
func (e *ircEnv) connect(t *testing.T, tune func(*announce.Connection)) announce.Connection {
	t.Helper()
	p, _ := announce.PresetByID("one-line-details-link")
	c := announce.Connection{
		Name: "Test tracker", Enabled: true, Preset: p.ID, Server: "127.0.0.1", Port: e.irc.port(), Nick: "mybot", Auth: announce.AuthNone,
		Channels: []announce.Channel{{Name: "#announce"}}, Announcers: []string{"AnnounceBot"}, TrackerURL: e.tracker.URL,
		Template: p.Template, Keys: map[string]string{"passkey": ircPasskey}, MaxPerHour: 5, MaxPerDay: 20,
	}
	if tune != nil {
		tune(&c)
	}
	saved, err := e.server.AnnounceRepo.Create(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.server.TestStartAnnounce(); err != nil {
		t.Fatal(err)
	}
	waitForIRC(t, "joined the announce channel", func() bool { return e.irc.joined("#announce") })
	return saved
}

func waitForIRC(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type ircLogRow struct {
	Release  string `json:"release"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	Item     string `json:"item"`
}

func (e *ircEnv) log(t *testing.T, id int64) []ircLogRow {
	t.Helper()
	return getJSON[[]ircLogRow](t, e.client, fmt.Sprintf("%s/api/irc-announce/%d/log", e.base, id))
}

// logFor waits for an entry about release and returns it.
func (e *ircEnv) logFor(t *testing.T, id int64, release string) ircLogRow {
	t.Helper()
	var row ircLogRow
	waitForIRC(t, "a log entry for "+release, func() bool {
		for _, r := range e.log(t, id) {
			if r.Release == release {
				row = r
				return true
			}
		}
		return false
	})
	return row
}

func movieLine(release string, id int, size string) string {
	return fmt.Sprintf("[Movies/HD] %s - https://tracker.example/details.php?id=%d - %s", release, id, size)
}

func (e *ircEnv) queue(t *testing.T) []map[string]any {
	return getJSON[[]map[string]any](t, e.client, e.base+"/api/queue")
}

const movieRelease = "The.Fixture.Movie.1999.1080p.BluRay.x264-FIXTURE"

func TestIRCAnnounceGrabsAWantedMovieOnce(t *testing.T) {
	e := newIRCEnv(t)
	movie, err := e.server.MovieRepo.Add(library.Movie{TMDBID: 605, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	conn := e.connect(t, nil)

	// A line from someone who is not an announcer is never read.
	e.irc.say("Intruder", "#announce", movieLine(movieRelease, 1, "8.5 GB"))
	// A film that is not in the library is read but not wanted.
	e.irc.say("AnnounceBot", "#announce", movieLine("Some.Other.Film.2020.1080p.BluRay.x264-GRP", 2, "9 GB"))
	row := e.logFor(t, conn.ID, "Some.Other.Film.2020.1080p.BluRay.x264-GRP")
	if row.Decision != announce.DecisionSkipped || !strings.Contains(row.Reason, "No movie in your library") {
		t.Fatalf("not-wanted entry = %+v", row)
	}
	if len(e.queue(t)) != 0 {
		t.Fatal("something was grabbed before a wanted release was announced")
	}

	// The wanted film.
	e.irc.say("AnnounceBot", "#announce", movieLine(movieRelease, 3, "8.5 GB"))
	row = e.logFor(t, conn.ID, movieRelease)
	if row.Decision != announce.DecisionGrabbed || row.Item != "The Fixture Movie (1999)" {
		t.Fatalf("wanted entry = %+v", row)
	}
	q := e.queue(t)
	if len(q) != 1 || q[0]["releaseTitle"] != movieRelease {
		t.Fatalf("queue = %+v", q)
	}

	// The same release again (another tracker, a repeat) is not grabbed twice.
	e.irc.say("AnnounceBot", "#announce", movieLine(movieRelease, 4, "8.5 GB"))
	waitForIRC(t, "the duplicate to be logged", func() bool {
		for _, r := range e.log(t, conn.ID) {
			if r.Decision == announce.DecisionDuplicate {
				return true
			}
		}
		return false
	})
	if len(e.queue(t)) != 1 {
		t.Fatalf("grabbed twice: %+v", e.queue(t))
	}

	// The intruder's line never reached the log.
	for _, r := range e.log(t, conn.ID) {
		if strings.Contains(r.Reason, "Intruder") {
			t.Fatalf("log mentions the intruder: %+v", r)
		}
	}
	if got, _ := e.server.MovieRepo.Get(movie.ID); got.Status == library.StatusMissing {
		t.Fatalf("movie status = %q", got.Status)
	}

	// The tracker was asked for the file with the real passkey, on the tracker's own host.
	waitForIRC(t, "the download request", func() bool { e.mu.Lock(); defer e.mu.Unlock(); return len(e.requests) > 0 })
	e.mu.Lock()
	req := e.requests[0]
	e.mu.Unlock()
	if !strings.HasPrefix(req, "/download.php/3/") || !strings.Contains(req, "torrent_pass="+ircPasskey) {
		t.Fatalf("download request = %q", req)
	}

	// The passkey is nowhere a browser or a log can see it.
	for _, path := range []string{"/api/irc-announce", fmt.Sprintf("/api/irc-announce/%d/log", conn.ID), "/api/queue", "/api/activity"} {
		resp, err := e.client.Get(e.base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), ircPasskey) {
			t.Fatalf("%s contains the passkey", path)
		}
	}
	e.release()
	waitBackground(t, e.server)
}

func TestIRCAnnounceStoresSecretsEncrypted(t *testing.T) {
	e := newIRCEnv(t)
	p, _ := announce.PresetByID("one-line-details-link")
	conn, err := e.server.AnnounceRepo.Create(announce.Connection{
		Name: "Secrets", Server: "irc.example.org", Port: 6697, TLS: true, Nick: "mybot", Auth: announce.AuthNickServ, AuthPass: "nickserv-secret-pw",
		Invite: "TrackerBot announce INVITEKEY99", Channels: []announce.Channel{{Name: "#announce", Key: "chanpw"}}, Announcers: []string{"AnnounceBot"},
		TrackerURL: "https://tracker.example.org", Template: p.Template, Keys: map[string]string{"passkey": ircPasskey}, MaxPerHour: 5, MaxPerDay: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Read the raw rows through the repo's own database handle: no secret in the clear.
	raw := e.server.TestRawIRCRows()
	for _, secret := range []string{ircPasskey, "nickserv-secret-pw", "INVITEKEY99", "chanpw"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("the database holds %q in the clear", secret)
		}
	}
	got, err := e.server.AnnounceRepo.Get(conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Channels[0].Key != "chanpw" || got.AuthPass != "nickserv-secret-pw" || got.Invite != "TrackerBot announce INVITEKEY99" || got.Keys["passkey"] != ircPasskey {
		t.Fatalf("secrets did not round-trip: %+v", got)
	}
}

func TestIRCAnnounceNeverGrabsWhatIsNotWanted(t *testing.T) {
	e := newIRCEnv(t)
	// Not monitored, already at the right quality, below the profile, and a year that does not match.
	unmonitored, _ := e.server.MovieRepo.Add(library.Movie{TMDBID: 1, Title: "Unwatched Film", Year: 2001, Monitored: false})
	_ = unmonitored
	done, _ := e.server.MovieRepo.Add(library.Movie{TMDBID: 2, Title: "Finished Film", Year: 2002, Monitored: true})
	if err := e.server.MovieRepo.SetStatus(done.ID, library.StatusDownloaded, "Bluray-1080p", "/movies/finished.mkv"); err != nil {
		t.Fatal(err)
	}
	_, _ = e.server.MovieRepo.Add(library.Movie{TMDBID: 3, Title: "Dated Film", Year: 2003, Monitored: true})
	_, _ = e.server.MovieRepo.Add(library.Movie{TMDBID: 4, Title: "Future Film", Year: 2090, Monitored: true, ReleaseDate: "2090-01-01"})
	conn := e.connect(t, nil)

	cases := []struct{ release, reason string }{
		{"Unwatched.Film.2001.1080p.BluRay.x264-GRP", "isn't monitored"},
		{"Finished.Film.2002.1080p.BluRay.x264-GRP", "already at the quality"},
		{"Dated.Film.2010.1080p.BluRay.x264-GRP", "No movie in your library"},
		{"Dated.Film.2003.480p.DVDRip.XviD-GRP", "quality profile"},
		{"Future.Film.2090.1080p.BluRay.x264-GRP", "isn't released yet"},
	}
	for i, tc := range cases {
		e.irc.say("AnnounceBot", "#announce", movieLine(tc.release, 10+i, "8 GB"))
	}
	for _, tc := range cases {
		row := e.logFor(t, conn.ID, tc.release)
		if row.Decision != announce.DecisionSkipped || !strings.Contains(row.Reason, tc.reason) {
			t.Errorf("%s: %+v, want skipped containing %q", tc.release, row, tc.reason)
		}
	}
	waitBackground(t, e.server)
	if q := e.queue(t); len(q) != 0 {
		t.Fatalf("grabbed something unwanted: %+v", q)
	}
}

func TestIRCAnnounceGrabsOnlyARealUpgrade(t *testing.T) {
	e := newIRCEnv(t)
	enableUpgrades(t, e.server)
	m, err := e.server.MovieRepo.Add(library.Movie{TMDBID: 605, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.server.MovieRepo.SetStatus(m.ID, library.StatusDownloaded, "WEBDL-1080p", "/movies/fixture.mkv"); err != nil {
		t.Fatal(err)
	}
	conn := e.connect(t, nil)
	same := "The.Fixture.Movie.1999.1080p.WEB-DL.x264-OTHER"
	better := "The.Fixture.Movie.1999.1080p.BluRay.x264-FIXTURE"
	e.irc.say("AnnounceBot", "#announce", movieLine(same, 50, "6 GB"))
	if row := e.logFor(t, conn.ID, same); row.Decision != announce.DecisionSkipped || !strings.Contains(row.Reason, "better version") {
		t.Fatalf("same quality: %+v", row)
	}
	if len(e.queue(t)) != 0 {
		t.Fatalf("grabbed a release that is no better: %+v", e.queue(t))
	}
	e.irc.say("AnnounceBot", "#announce", movieLine(better, 51, "9 GB"))
	if row := e.logFor(t, conn.ID, better); row.Decision != announce.DecisionGrabbed {
		t.Fatalf("upgrade: %+v", row)
	}
	e.release()
	waitBackground(t, e.server)
}

func TestIRCAnnounceFillsMissingEpisodesOnly(t *testing.T) {
	e := newIRCEnv(t)
	series, err := e.server.MovieRepo.AddSeries(library.Series{TMDBID: 1399, Title: "Fixture Show", Year: 2020, Monitored: true}, []library.Episode{
		{Season: 1, Episode: 1, AirDate: "2020-01-01"}, {Season: 1, Episode: 2, AirDate: "2020-01-08"}, {Season: 1, Episode: 3, AirDate: "2020-01-15"},
	})
	if err != nil {
		t.Fatal(err)
	}
	eps, _ := e.server.MovieRepo.ListEpisodes(series.ID)
	for _, ep := range eps {
		if ep.Episode == 1 {
			if err := e.server.MovieRepo.SetEpisodeStatus(ep.ID, library.StatusDownloaded, "Bluray-1080p", "/tv/s01e01.mkv"); err != nil {
				t.Fatal(err)
			}
		}
	}
	conn := e.connect(t, nil)
	line := func(rel string, id int, size string) string {
		return fmt.Sprintf("[TV/HD] %s - https://tracker.example/details.php?id=%d - %s", rel, id, size)
	}
	const have = "Fixture.Show.S01E01.1080p.WEB-DL.x264-GRP"
	const missing = "Fixture.Show.S01E02.1080p.WEB-DL.x264-GRP"
	const pack = "Fixture.Show.S01.1080p.BluRay.x264-GRP"
	const other = "Different.Show.S01E02.1080p.WEB-DL.x264-GRP"
	e.irc.say("AnnounceBot", "#announce", line(have, 20, "1.5 GB"))
	e.irc.say("AnnounceBot", "#announce", line(other, 21, "1.5 GB"))
	e.irc.say("AnnounceBot", "#announce", line(pack, 22, "20 GB")) // a pack would download an episode that is already there
	e.irc.say("AnnounceBot", "#announce", line(missing, 23, "1.5 GB"))

	if row := e.logFor(t, conn.ID, missing); row.Decision != announce.DecisionGrabbed || row.Item != "Fixture Show S01E02" {
		t.Fatalf("missing episode: %+v", row)
	}
	for _, rel := range []string{have, other, pack} {
		if row := e.logFor(t, conn.ID, rel); row.Decision != announce.DecisionSkipped {
			t.Errorf("%s: %+v, want skipped", rel, row)
		}
	}
	q := e.queue(t)
	if len(q) != 1 || q[0]["releaseTitle"] != missing {
		t.Fatalf("queue = %+v", q)
	}
	e.release()
	waitBackground(t, e.server)
}

func TestIRCAnnounceLimitsGrabs(t *testing.T) {
	e := newIRCEnv(t)
	for i, name := range []string{"First Film", "Second Film", "Third Film"} {
		if _, err := e.server.MovieRepo.Add(library.Movie{TMDBID: 100 + i, Title: name, Year: 2010, Monitored: true}); err != nil {
			t.Fatal(err)
		}
	}
	conn := e.connect(t, func(c *announce.Connection) { c.MaxPerHour = 1 })
	first, second := "First.Film.2010.1080p.BluRay.x264-GRP", "Second.Film.2010.1080p.BluRay.x264-GRP"
	e.irc.say("AnnounceBot", "#announce", movieLine(first, 30, "8 GB"))
	if row := e.logFor(t, conn.ID, first); row.Decision != announce.DecisionGrabbed {
		t.Fatalf("first: %+v", row)
	}
	e.irc.say("AnnounceBot", "#announce", movieLine(second, 31, "8 GB"))
	row := e.logFor(t, conn.ID, second)
	if row.Decision != announce.DecisionSkipped || !strings.Contains(row.Reason, "an hour") {
		t.Fatalf("second: %+v", row)
	}
	if len(e.queue(t)) != 1 {
		t.Fatalf("queue = %+v", e.queue(t))
	}
	e.release()
	waitBackground(t, e.server)
}

func TestIRCAnnounceDailyCapIsPerAnnouncer(t *testing.T) {
	e := newIRCEnv(t)
	for i, name := range []string{"Alpha Film", "Beta Film"} {
		if _, err := e.server.MovieRepo.Add(library.Movie{TMDBID: 200 + i, Title: name, Year: 2011, Monitored: true}); err != nil {
			t.Fatal(err)
		}
	}
	conn := e.connect(t, func(c *announce.Connection) {
		c.MaxPerDay, c.MaxPerHour = 1, 10
		c.Announcers = []string{"BotOne", "BotTwo"}
	})
	a, b := "Alpha.Film.2011.1080p.BluRay.x264-GRP", "Beta.Film.2011.1080p.BluRay.x264-GRP"
	e.irc.say("BotOne", "#announce", movieLine(a, 40, "8 GB"))
	if row := e.logFor(t, conn.ID, a); row.Decision != announce.DecisionGrabbed {
		t.Fatalf("%+v", row)
	}
	// BotOne has used its one for the day; BotTwo has not.
	e.irc.say("BotTwo", "#announce", movieLine(b, 41, "8 GB"))
	if row := e.logFor(t, conn.ID, b); row.Decision != announce.DecisionGrabbed {
		t.Fatalf("%+v", row)
	}
	e.release()
	waitBackground(t, e.server)
}

func TestIRCAnnounceTestBoxDoesNotGrab(t *testing.T) {
	e := newIRCEnv(t)
	if _, err := e.server.MovieRepo.Add(library.Movie{TMDBID: 605, Title: "The Fixture Movie", Year: 1999, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	p, _ := announce.PresetByID("one-line-details-link")
	body := func(lines ...string) map[string]any {
		return map[string]any{"template": p.Template, "trackerUrl": "https://tracker.example.org", "lines": lines}
	}
	var res announce.TestResult
	raw := postJSON[json.RawMessage](t, e.client, e.base+"/api/irc-announce/test", body(movieLine(movieRelease+" FREELEECH", 5, "8.5 GB")), http.StatusOK)
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Matched || res.Release != movieRelease || !res.Freeleech || res.TorrentID != "5" || res.Category != "Movies/HD" {
		t.Fatalf("parsed = %+v", res)
	}
	if res.Decision != announce.DecisionWouldGrab || res.Item != "The Fixture Movie (1999)" {
		t.Fatalf("decision = %s %q (%s)", res.Decision, res.Item, res.Reason)
	}
	if !strings.Contains(res.DownloadURL, "<passkey>") || strings.HasPrefix(res.DownloadURL, "https://tracker.example.org/download.php/5/") == false {
		t.Fatalf("download address = %q", res.DownloadURL)
	}

	// A line that doesn't match says so; a film that isn't wanted says why.
	raw = postJSON[json.RawMessage](t, e.client, e.base+"/api/irc-announce/test", body("hello everyone"), http.StatusOK)
	res = announce.TestResult{}
	_ = json.Unmarshal(raw, &res)
	if res.Matched || !strings.Contains(res.Message, "doesn't match") {
		t.Fatalf("res = %+v", res)
	}
	raw = postJSON[json.RawMessage](t, e.client, e.base+"/api/irc-announce/test", body(movieLine("Unknown.Film.2015.1080p.BluRay.x264-GRP", 6, "8 GB")), http.StatusOK)
	res = announce.TestResult{}
	_ = json.Unmarshal(raw, &res)
	if !res.Matched || res.Decision != announce.DecisionSkipped || !strings.Contains(res.Reason, "No movie in your library") {
		t.Fatalf("res = %+v", res)
	}

	// A multi-line announcement is pasted whole.
	mp, _ := announce.PresetByID("multi-line")
	raw = postJSON[json.RawMessage](t, e.client, e.base+"/api/irc-announce/test", map[string]any{"template": mp.Template, "trackerUrl": "https://tracker.example.org", "lines": mp.Examples}, http.StatusOK)
	res = announce.TestResult{}
	_ = json.Unmarshal(raw, &res)
	if !res.Matched || res.TorrentID != "9116" {
		t.Fatalf("multi-line = %+v", res)
	}

	// Nothing was downloaded, and no IRC connection exists.
	if q := e.queue(t); len(q) != 0 {
		t.Fatalf("the test box grabbed something: %+v", q)
	}
	list := getJSON[map[string]any](t, e.client, e.base+"/api/irc-announce")
	if conns := list["connections"].([]any); len(conns) != 0 {
		t.Fatalf("connections = %v", conns)
	}
}

func TestIRCAnnounceIsOffUntilAddedAndSwitchedOn(t *testing.T) {
	e := newIRCEnv(t)
	list := getJSON[map[string]any](t, e.client, e.base+"/api/irc-announce")
	if len(list["connections"].([]any)) != 0 || len(list["presets"].([]any)) < 3 {
		t.Fatalf("fresh list = %v", list)
	}
	// A connection that is saved but off never connects.
	e.connectOff(t)
	time.Sleep(300 * time.Millisecond)
	if e.irc.joined("#announce") {
		t.Fatal("a switched-off connection joined a channel")
	}
}

func (e *ircEnv) connectOff(t *testing.T) {
	t.Helper()
	p, _ := announce.PresetByID("one-line-details-link")
	if _, err := e.server.AnnounceRepo.Create(announce.Connection{
		Name: "Off", Enabled: false, Server: "127.0.0.1", Port: e.irc.port(), Nick: "mybot", Auth: announce.AuthNone,
		Channels: []announce.Channel{{Name: "#announce"}}, Announcers: []string{"AnnounceBot"}, TrackerURL: e.tracker.URL, Template: p.Template,
		Keys: map[string]string{"passkey": "x"}, MaxPerHour: 5, MaxPerDay: 20,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.server.TestStartAnnounce(); err != nil {
		t.Fatal(err)
	}
}

func TestIRCAnnounceSettingsAPI(t *testing.T) {
	e := newIRCEnv(t)
	p, _ := announce.PresetByID("one-line-details-link")
	valid := func() map[string]any {
		return map[string]any{
			"name": "My tracker", "preset": p.ID, "server": "irc.example.org", "nick": "ryan_bot", "auth": "sasl", "authAccount": "ryan", "authPassword": "irc-password-1",
			"invite": "BotName announce INVITEKEY", "channels": []map[string]string{{"name": "#announce"}}, "announcers": []string{"AnnounceBot"},
			"trackerUrl": "https://tracker.example.org/", "template": p.Template, "keys": []map[string]string{{"name": "passkey", "value": "PASSKEY-VALUE-1"}},
		}
	}
	with := func(over map[string]any) map[string]any {
		m := valid()
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	bad := func(over map[string]any, want string) {
		t.Helper()
		status, body := doJSONStatus(t, e.client, http.MethodPost, e.base+"/api/irc-announce", with(over))
		if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["error"]), want) {
			t.Errorf("%v: %d %v, want 400 mentioning %q", over, status, body["error"], want)
		}
	}
	// What is refused, and why.
	bad(map[string]any{"server": "127.0.0.1"}, "on the internet")
	bad(map[string]any{"server": "192.168.1.5"}, "on the internet")
	bad(map[string]any{"server": "localhost"}, "on the internet")
	bad(map[string]any{"server": "ircserver"}, "on the internet")
	bad(map[string]any{"server": "irc://irc.example.org"}, "server name")
	bad(map[string]any{"nick": "bad nick"}, "nick")
	bad(map[string]any{"nick": "x\r\nQUIT"}, "nick")
	bad(map[string]any{"channels": []map[string]string{}}, "channel")
	bad(map[string]any{"channels": []map[string]string{{"name": "#a b"}}}, "channel")
	bad(map[string]any{"announcers": []string{}}, "nick of the bot")
	bad(map[string]any{"announcers": []string{"bad nick"}}, "announcer")
	bad(map[string]any{"authPassword": ""}, "password")
	bad(map[string]any{"invite": "justabot"}, "Invite command")
	bad(map[string]any{"invite": "Bot hi\r\nQUIT"}, "Invite command")
	bad(map[string]any{"trackerUrl": "ftp://tracker.example.org"}, "tracker")
	bad(map[string]any{"keys": []map[string]string{}}, "passkey")
	bad(map[string]any{"template": map[string]any{"patterns": []string{"(?P<releaseName>"}, "downloadUrl": "/x"}}, "Template")
	bad(map[string]any{"template": map[string]any{"patterns": []string{"(?P<other>.+)"}, "downloadUrl": "/x"}}, "release name")
	bad(map[string]any{"template": map[string]any{"patterns": []string{"(?P<releaseName>.+)"}, "downloadUrl": "https://{{ .releaseName }}/x"}}, "written out")
	bad(map[string]any{"maxPerHour": 0}, "limit")
	bad(map[string]any{"port": 70000}, "Port")

	created := postJSON[map[string]any](t, e.client, e.base+"/api/irc-announce", valid(), http.StatusCreated)
	id := int64(created["id"].(float64))
	if created["enabled"] != false || created["hasPassword"] != true || created["hasInvite"] != true || created["tls"] != true || created["port"] != float64(6697) {
		t.Fatalf("created = %v", created)
	}
	raw, _ := json.Marshal(created)
	for _, secret := range []string{"irc-password-1", "INVITEKEY", "PASSKEY-VALUE-1"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("the response contains %q", secret)
		}
	}
	keys := created["keys"].([]any)
	if len(keys) != 1 || keys[0].(map[string]any)["name"] != "passkey" || keys[0].(map[string]any)["hasValue"] != true {
		t.Fatalf("keys = %v", keys)
	}
	if created["trackerUrl"] != "https://tracker.example.org" {
		t.Fatalf("trackerUrl = %v", created["trackerUrl"])
	}

	url := fmt.Sprintf("%s/api/irc-announce/%d", e.base, id)
	// Editing without retyping the secrets keeps them.
	updated := postJSONMethod[map[string]any](t, e.client, http.MethodPut, url, map[string]any{"name": "Renamed", "authPassword": "", "invite": "", "keys": []map[string]string{{"name": "passkey", "value": ""}}}, http.StatusOK)
	if updated["name"] != "Renamed" || updated["hasPassword"] != true || updated["hasInvite"] != true {
		t.Fatalf("updated = %v", updated)
	}
	saved, _ := e.server.AnnounceRepo.Get(id)
	if saved.AuthPass != "irc-password-1" || saved.Invite != "BotName announce INVITEKEY" || saved.Keys["passkey"] != "PASSKEY-VALUE-1" {
		t.Fatalf("secrets lost on a plain edit: %+v", saved)
	}
	// Moving to another server or another tracker address needs the secrets typed again,
	// so a saved secret is never sent somewhere new.
	status, body := doJSONStatus(t, e.client, http.MethodPut, url, map[string]any{"server": "irc.other.example"})
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["error"]), "type the") {
		t.Fatalf("server move without a password: %d %v", status, body)
	}
	status, body = doJSONStatus(t, e.client, http.MethodPut, url, map[string]any{"trackerUrl": "https://evil.example.net"})
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["error"]), "type the") {
		t.Fatalf("tracker move without the keys: %d %v", status, body)
	}
	// ...but works with them.
	postJSONMethod[map[string]any](t, e.client, http.MethodPut, url, map[string]any{
		"server": "irc.other.example", "authPassword": "new-pw", "invite": "BotName announce NEWKEY",
		"trackerUrl": "https://tracker.other.example", "keys": []map[string]string{{"name": "passkey", "value": "NEWPASS"}},
	}, http.StatusOK)

	// The on/off switch.
	on := postJSONMethod[map[string]any](t, e.client, http.MethodPut, url+"/enabled", map[string]any{"enabled": true}, http.StatusOK)
	if on["enabled"] != true {
		t.Fatalf("on = %v", on)
	}
	off := postJSONMethod[map[string]any](t, e.client, http.MethodPut, url+"/enabled", map[string]any{"enabled": false}, http.StatusOK)
	if off["enabled"] != false {
		t.Fatalf("off = %v", off)
	}
	if status, _ := doJSONStatus(t, e.client, http.MethodPut, e.base+"/api/irc-announce/99999/enabled", map[string]any{"enabled": true}); status != http.StatusNotFound {
		t.Fatalf("unknown connection: %d", status)
	}

	// Removing it.
	if got := deleteReq(t, e.client, url); got != http.StatusNoContent {
		t.Fatalf("delete = %d", got)
	}
	if got := deleteReq(t, e.client, url); got != http.StatusNotFound {
		t.Fatalf("second delete = %d", got)
	}
}

func TestIRCAnnounceNeedsAnAdministrator(t *testing.T) {
	server, base, admin, member, _ := familyServer(t)
	_ = server
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/irc-announce"},
		{http.MethodPost, "/api/irc-announce"},
		{http.MethodPost, "/api/irc-announce/test"},
		{http.MethodGet, "/api/irc-announce/1/log"},
		{http.MethodPut, "/api/irc-announce/1/enabled"},
		{http.MethodDelete, "/api/irc-announce/1"},
	} {
		if status, _ := doJSONStatus(t, member, tc.method, base+tc.path, map[string]any{}); status != http.StatusForbidden {
			t.Errorf("member %s %s = %d, want 403", tc.method, tc.path, status)
		}
		if status, _ := doJSONStatus(t, http.DefaultClient, tc.method, base+tc.path, map[string]any{}); status != http.StatusUnauthorized {
			t.Errorf("signed out %s %s = %d, want 401", tc.method, tc.path, status)
		}
	}
	if status, _ := doJSONStatus(t, admin, http.MethodGet, base+"/api/irc-announce", nil); status != http.StatusOK {
		t.Fatalf("admin = %d", status)
	}
}
