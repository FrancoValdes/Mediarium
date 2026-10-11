package api_test

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rdborg/mediarium/internal/blocklist"
	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/problems"
	"github.com/rdborg/mediarium/internal/queue"
)

// statNNTP is a news server that serves articles by message-id and answers
// STAT, for the check made before a download.
type statNNTP struct {
	port   int
	noStat bool
	bodies map[string][]byte
	stats  atomic.Int32
	bodyN  atomic.Int32
}

func newStatNNTP(t *testing.T, bodies map[string][]byte) *statNNTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &statNNTP{bodies: bodies}
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	s.port, _ = strconv.Atoi(p)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *statNNTP) serve(conn net.Conn) {
	defer conn.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	rw.WriteString("200 ready\r\n")
	rw.Flush()
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "AUTHINFO USER"):
			rw.WriteString("381 password required\r\n")
		case strings.HasPrefix(up, "AUTHINFO PASS"):
			rw.WriteString("281 ok\r\n")
		case strings.HasPrefix(up, "GROUP"):
			rw.WriteString("211 0 0 0 group\r\n")
		case strings.HasPrefix(up, "STAT"):
			s.stats.Add(1)
			id := strings.Trim(strings.TrimSpace(line[4:]), "<>")
			_, ok := s.bodies[id]
			switch {
			case s.noStat:
				rw.WriteString("500 unrecognized\r\n")
			case ok:
				fmt.Fprintf(rw, "223 0 <%s>\r\n", id)
			default:
				rw.WriteString("430 no such article\r\n")
			}
		case strings.HasPrefix(up, "BODY"):
			s.bodyN.Add(1)
			id := strings.Trim(strings.TrimSpace(line[4:]), "<>")
			body, ok := s.bodies[id]
			if !ok {
				rw.WriteString("430 no such article\r\n")
				break
			}
			rw.WriteString("222 body follows\r\n")
			for _, l := range strings.Split(string(body), "\n") {
				l = strings.TrimRight(l, "\r")
				if strings.HasPrefix(l, ".") {
					rw.WriteString(".")
				}
				rw.WriteString(l)
				rw.WriteString("\r\n")
			}
			rw.WriteString(".\r\n")
		case up == "QUIT":
			rw.WriteString("205 bye\r\n")
			rw.Flush()
			return
		default:
			rw.WriteString("500 unrecognized\r\n")
		}
		rw.Flush()
	}
}

// newPrecheckEnv is newChecksEnv with a news server that answers STAT.
func newPrecheckEnv(t *testing.T) (*checksEnv, *statNNTP) {
	t.Helper()
	server, base, client := newControlServer(t, false)
	env := &checksEnv{server: server, base: base, client: client, nzbs: map[string]string{}, bodies: map[string][]byte{}}
	nntp := newStatNNTP(t, env.bodies)
	postJSON[map[string]any](t, client, base+"/api/usenet-servers", map[string]any{
		"name": "Fixture Usenet", "host": "127.0.0.1", "port": nntp.port, "useSsl": false, "connections": 2,
	}, http.StatusCreated)
	env.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.mu.Lock()
		nzb, ok := env.nzbs[r.URL.Path]
		env.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-nzb")
		fmt.Fprint(w, nzb)
	}))
	t.Cleanup(env.srv.Close)
	m, err := server.MovieRepo.Add(library.Movie{TMDBID: 603, Title: "The Fixture Movie", Year: 1999, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	env.movie = m
	return env, nntp
}

func (e *checksEnv) movieEventHas(t *testing.T, contains string) bool {
	t.Helper()
	events, err := e.server.QueueRepo.MovieEvents(e.movie.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if strings.Contains(ev.Message, contains) {
			return true
		}
	}
	return false
}

func TestPrecheckIsOffByDefaultAndSwitchable(t *testing.T) {
	env, _ := newPrecheckEnv(t)
	if got := getJSON[map[string]any](t, env.client, env.base+"/api/settings"); got["usenetPrecheck"] != false {
		t.Fatalf("usenetPrecheck = %v, want false by default", got["usenetPrecheck"])
	}
	env.put(t, map[string]any{"usenetPrecheck": true})
	if got := getJSON[map[string]any](t, env.client, env.base+"/api/settings"); got["usenetPrecheck"] != true {
		t.Fatalf("usenetPrecheck = %v after switching it on", got["usenetPrecheck"])
	}
	env.put(t, map[string]any{"usenetPrecheck": false})
	if got := getJSON[map[string]any](t, env.client, env.base+"/api/settings"); got["usenetPrecheck"] != false {
		t.Fatalf("usenetPrecheck = %v after switching it off", got["usenetPrecheck"])
	}
}

func TestPrecheckFailsAReleaseThatIsNotOnTheServers(t *testing.T) {
	env, nntp := newPrecheckEnv(t)
	env.put(t, map[string]any{"usenetPrecheck": true})
	url := env.release("gone", "gone", []fixtureFile{movieFile, {"extra.mkv", []byte("more")}})
	for id := range env.bodies { // the posts were removed from the servers
		delete(env.bodies, id)
	}
	const title = "The.Fixture.Movie.1999.1080p.WEB-DL.x264-GONE"
	it := env.finish(t, env.grab(t, title, url, 1000))

	if it.Status != queue.StatusFailed {
		t.Fatalf("status = %s, want failed", it.Status)
	}
	if !strings.Contains(it.Error, "Checked 2 articles; 2 of them are not on your Usenet servers") {
		t.Errorf("the person is told %q", it.Error)
	}
	if n := nntp.bodyN.Load(); n != 0 {
		t.Errorf("%d articles were downloaded; the check must come first", n)
	}
	keys, _ := env.server.Blocklist.Keys()
	if !keys[blocklist.Key(title)] {
		t.Error("the release should be on the blocklist")
	}
	if got := env.movieStatus(t); got != library.StatusMissing {
		t.Errorf("movie is %s, want missing so it is searched for again", got)
	}
	found := false
	for _, c := range env.problemCodes(t) {
		if c == problems.CodeUsenetMissingParts {
			found = true
		}
	}
	if !found {
		t.Errorf("the problem log has %v, want %s", env.problemCodes(t), problems.CodeUsenetMissingParts)
	}
	if !env.movieEventHas(t, "Checked 2 of 2 articles: 2 not found") {
		t.Error("the item's activity should say what the check found")
	}
}

func TestPrecheckLetsACompleteReleaseThrough(t *testing.T) {
	env, nntp := newPrecheckEnv(t)
	env.put(t, map[string]any{"usenetPrecheck": true})
	url := env.release("fine", "fine", []fixtureFile{movieFile})
	it := env.finish(t, env.grab(t, "The.Fixture.Movie.1999.1080p.WEB-DL.x264-FINE", url, 1000))
	if it.Status != queue.StatusCompleted {
		t.Fatalf("status = %s (%s), want completed", it.Status, it.Error)
	}
	if nntp.stats.Load() == 0 {
		t.Error("the articles should have been asked about")
	}
	if !env.movieEventHas(t, "Checked 1 of 1 articles: all found") {
		t.Error("the item's activity should say all articles were found")
	}
}

func TestPrecheckIsSkippedWhenTheServerCannotStat(t *testing.T) {
	env, nntp := newPrecheckEnv(t)
	nntp.noStat = true
	env.put(t, map[string]any{"usenetPrecheck": true})
	url := env.release("nostat", "nostat", []fixtureFile{movieFile})
	it := env.finish(t, env.grab(t, "The.Fixture.Movie.1999.1080p.WEB-DL.x264-NOSTAT", url, 1000))
	if it.Status != queue.StatusCompleted {
		t.Fatalf("status = %s (%s), the download must go on when the check can't run", it.Status, it.Error)
	}
	if !env.movieEventHas(t, "Article check skipped") {
		t.Error("the item's activity should say the check was skipped")
	}
}

func TestNoPrecheckWhenItIsOff(t *testing.T) {
	env, nntp := newPrecheckEnv(t)
	url := env.release("fine", "fine", []fixtureFile{movieFile})
	it := env.finish(t, env.grab(t, "The.Fixture.Movie.1999.1080p.WEB-DL.x264-OFF", url, 1000))
	if it.Status != queue.StatusCompleted {
		t.Fatalf("status = %s (%s)", it.Status, it.Error)
	}
	if n := nntp.stats.Load(); n != 0 {
		t.Errorf("%d STAT commands were sent although the check is off", n)
	}
}
