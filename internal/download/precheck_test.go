package download

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"context"
)

// statServer is a small NNTP server that answers STAT, for the pre-download
// check. has says which message-ids exist.
type statServer struct {
	addr string
	port int

	has    map[string]bool
	noStat bool          // answers "500 command not recognized" to STAT
	delay  time.Duration // wait before answering STAT
	reject int           // when set, STAT is answered with this code

	conns    atomic.Int32
	open     atomic.Int32
	maxOpen  atomic.Int32
	stats    atomic.Int32
	groupCmd atomic.Int32

	mu   sync.Mutex
	seen map[string]int
}

func newStatServer(t *testing.T, has map[string]bool) *statServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	s := &statServer{addr: "127.0.0.1", port: port, has: has, seen: map[string]int{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.conns.Add(1)
			n := s.open.Add(1)
			for {
				m := s.maxOpen.Load()
				if n <= m || s.maxOpen.CompareAndSwap(m, n) {
					break
				}
			}
			go s.handle(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *statServer) handle(c net.Conn) {
	defer func() { c.Close(); s.open.Add(-1) }()
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	w.WriteString("200 ready\r\n")
	w.Flush()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "AUTHINFO USER"):
			w.WriteString("381 password\r\n")
		case strings.HasPrefix(up, "AUTHINFO PASS"):
			w.WriteString("281 ok\r\n")
		case strings.HasPrefix(up, "GROUP"):
			s.groupCmd.Add(1)
			w.WriteString("211 0 0 0 g\r\n")
		case strings.HasPrefix(up, "STAT"):
			s.stats.Add(1)
			id := strings.Trim(strings.TrimSpace(line[4:]), "<>")
			s.mu.Lock()
			s.seen[id]++
			s.mu.Unlock()
			if s.delay > 0 {
				time.Sleep(s.delay)
			}
			switch {
			case s.noStat:
				w.WriteString("500 command not recognized\r\n")
			case s.reject != 0:
				fmt.Fprintf(w, "%d refused\r\n", s.reject)
			case s.has[id]:
				fmt.Fprintf(w, "223 0 <%s>\r\n", id)
			default:
				w.WriteString("430 no such article\r\n")
			}
		case up == "QUIT":
			w.WriteString("205 bye\r\n")
			w.Flush()
			return
		default:
			w.WriteString("500 command not recognized\r\n")
		}
		w.Flush()
	}
}

func (s *statServer) asked(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[id]
}

func (s *statServer) cfg(conns int) ClientConfig {
	return ClientConfig{Host: s.addr, Port: s.port, Connections: conns}
}

// precheckNZB is files data files of segs articles each (1000 bytes declared per
// article), plus, when par2Bytes > 0, one PAR2 file of one article of that size.
func precheckNZB(files, segs int, par2 int64) (*NZB, map[string]bool) {
	ids := map[string]bool{}
	nzb := &NZB{}
	for f := 0; f < files; f++ {
		nf := NZBFile{Subject: fmt.Sprintf(`"part%d.rar" yEnc`, f), Groups: []string{"alt.binaries.test"}}
		for s := 1; s <= segs; s++ {
			id := fmt.Sprintf("f%d-s%d@example", f, s)
			ids[id] = true
			nf.Segments = append(nf.Segments, NZBSegment{Number: s, Bytes: 1000, MessageID: id})
		}
		nzb.Files = append(nzb.Files, nf)
	}
	if par2 > 0 {
		ids["par2@example"] = true
		nzb.Files = append(nzb.Files, NZBFile{
			Subject:  `"set.vol0+1.par2" yEnc`,
			Groups:   []string{"alt.binaries.test"},
			Segments: []NZBSegment{{Number: 1, Bytes: par2, MessageID: "par2@example"}},
		})
	}
	return nzb, ids
}

func copyIDs(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestPrecheckAllArticlesFound(t *testing.T) {
	nzb, ids := precheckNZB(5, 100, 50_000)
	srv := newStatServer(t, ids)
	res := Precheck(context.Background(), []ClientConfig{srv.cfg(10)}, nzb, PrecheckOptions{})
	if res.Skipped {
		t.Fatalf("skipped: %s", res.SkipReason)
	}
	// 501 articles: 3% is 16, and the first and last of the five large files
	// (and of the PAR2 file, one article) are among them.
	if res.Total != 501 || res.Checked != 16 || res.Missing != 0 || res.Hopeless() || res.Err() != nil {
		t.Fatalf("unexpected result %+v", res)
	}
	if got := res.Summary(); got != "Checked 16 of 501 articles: all found" {
		t.Fatalf("summary %q", got)
	}
	if int(srv.stats.Load()) != res.Checked {
		t.Fatalf("server saw %d STAT commands, want %d", srv.stats.Load(), res.Checked)
	}
	for f := 0; f < 5; f++ {
		for _, s := range []int{1, 100} {
			if srv.asked(fmt.Sprintf("f%d-s%d@example", f, s)) != 1 {
				t.Errorf("the first and last article of file %d should be asked about (%d)", f, s)
			}
		}
	}
	if srv.maxOpen.Load() > 2 {
		t.Fatalf("%d connections open at once, want at most 2", srv.maxOpen.Load())
	}
}

func TestPrecheckSomeMissingButRepairable(t *testing.T) {
	nzb, ids := precheckNZB(5, 100, 100_000) // PAR2 can rebuild 100 articles' worth
	// Every 100th article gone: about 1% of 500 articles = about 5000 bytes.
	for f := 0; f < 5; f++ {
		delete(ids, fmt.Sprintf("f%d-s1@example", f)) // the first article of each file
	}
	srv := newStatServer(t, ids)
	res := Precheck(context.Background(), []ClientConfig{srv.cfg(2)}, nzb, PrecheckOptions{})
	if res.Skipped || res.Missing < 5 {
		t.Fatalf("expected the missing first articles to be seen: %+v", res)
	}
	if res.Hopeless() || res.Err() != nil {
		t.Fatalf("the PAR2 file can repair this, but got %+v / %s", res, res.Summary())
	}
	if !strings.Contains(res.Summary(), "the PAR2 files can repair that") {
		t.Fatalf("summary %q", res.Summary())
	}
}

func TestPrecheckTooManyMissingFailsFast(t *testing.T) {
	nzb, ids := precheckNZB(4, 100, 2_000) // PAR2 could rebuild 2 articles' worth
	for f := 0; f < 4; f++ {
		for s := 1; s <= 100; s += 2 {
			delete(ids, fmt.Sprintf("f%d-s%d@example", f, s)) // half of every file
		}
	}
	srv := newStatServer(t, ids)
	res := Precheck(context.Background(), []ClientConfig{srv.cfg(2)}, nzb, PrecheckOptions{})
	if !res.Hopeless() {
		t.Fatalf("expected a hopeless release: %+v", res)
	}
	err := res.Err()
	var re *ReleaseError
	if !errors.As(err, &re) {
		t.Fatalf("want a ReleaseError, got %v", err)
	}
	want := fmt.Sprintf("Checked %d articles; %d of them are not on your Usenet servers", res.Checked, res.Missing)
	if err.Error() != want {
		t.Fatalf("message %q, want %q", err.Error(), want)
	}
	if !strings.Contains(res.Summary(), "more than the PAR2 files can repair") {
		t.Fatalf("summary %q", res.Summary())
	}
}

func TestPrecheckMissingWithoutPar2IsHopeless(t *testing.T) {
	nzb, ids := precheckNZB(2, 50, 0)
	delete(ids, "f0-s1@example")
	srv := newStatServer(t, ids)
	res := Precheck(context.Background(), []ClientConfig{srv.cfg(1)}, nzb, PrecheckOptions{})
	if !res.Hopeless() || !strings.Contains(res.Summary(), "no PAR2 files") {
		t.Fatalf("one missing article and no PAR2 files should be hopeless: %+v %s", res, res.Summary())
	}
}

func TestPrecheckServerWithoutStatIsSkipped(t *testing.T) {
	nzb, ids := precheckNZB(3, 60, 10_000)
	srv := newStatServer(t, ids)
	srv.noStat = true
	res := Precheck(context.Background(), []ClientConfig{srv.cfg(2)}, nzb, PrecheckOptions{})
	if !res.Skipped || !strings.Contains(res.SkipReason, "does not support checking articles") {
		t.Fatalf("expected a skip, got %+v", res)
	}
	if res.Hopeless() || res.Err() != nil {
		t.Fatal("a skipped check must never fail the download")
	}
	if !strings.HasPrefix(res.Summary(), "Article check skipped: ") {
		t.Fatalf("summary %q", res.Summary())
	}
}

func TestPrecheckUnexpectedAnswerIsSkipped(t *testing.T) {
	nzb, ids := precheckNZB(3, 60, 10_000)
	srv := newStatServer(t, ids)
	srv.reject = 480
	res := Precheck(context.Background(), []ClientConfig{srv.cfg(2)}, nzb, PrecheckOptions{})
	if !res.Skipped || !strings.Contains(res.SkipReason, "login") {
		t.Fatalf("expected a skip about the login, got %+v", res)
	}
}

func TestPrecheckSlowServerTimesOut(t *testing.T) {
	nzb, ids := precheckNZB(3, 60, 10_000)
	srv := newStatServer(t, ids)
	srv.delay = 3 * time.Second
	start := time.Now()
	res := Precheck(context.Background(), []ClientConfig{srv.cfg(2)}, nzb, PrecheckOptions{Timeout: 400 * time.Millisecond})
	if !res.Skipped || !strings.Contains(res.SkipReason, "took longer than") {
		t.Fatalf("expected a timeout skip, got %+v", res)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the check took %v, it should stop at its timeout", time.Since(start))
	}
}

func TestPrecheckBackupServerHasWhatPrimaryLacks(t *testing.T) {
	nzb, ids := precheckNZB(4, 100, 0)
	primaryIDs := copyIDs(ids)
	var gone []string
	for f := 0; f < 4; f++ {
		id := fmt.Sprintf("f%d-s1@example", f)
		delete(primaryIDs, id)
		gone = append(gone, id)
	}
	primary := newStatServer(t, primaryIDs)
	backup := newStatServer(t, ids)
	res := Precheck(context.Background(), []ClientConfig{primary.cfg(2), backup.cfg(2)}, nzb, PrecheckOptions{})
	if res.Skipped || res.Missing != 0 || res.Hopeless() {
		t.Fatalf("the backup has everything, got %+v", res)
	}
	// The backup is only asked about what the primary lacked.
	if int(backup.stats.Load()) != len(gone) {
		t.Fatalf("backup was asked %d times, want %d", backup.stats.Load(), len(gone))
	}
	for _, id := range gone {
		if backup.asked(id) != 1 {
			t.Errorf("backup was not asked about %s", id)
		}
	}
	if int(primary.stats.Load()) != res.Checked {
		t.Fatalf("primary asked %d times, want %d", primary.stats.Load(), res.Checked)
	}
}

func TestPrecheckBackupNotContactedWhenPrimaryHasAll(t *testing.T) {
	nzb, ids := precheckNZB(3, 100, 0)
	primary := newStatServer(t, ids)
	backup := newStatServer(t, ids)
	res := Precheck(context.Background(), []ClientConfig{primary.cfg(2), backup.cfg(2)}, nzb, PrecheckOptions{})
	if res.Skipped || res.Missing != 0 {
		t.Fatalf("unexpected %+v", res)
	}
	if backup.conns.Load() != 0 {
		t.Fatalf("the backup got %d connections although the primary had everything", backup.conns.Load())
	}
}

func TestPrecheckDeadServer(t *testing.T) {
	nzb, ids := precheckNZB(3, 100, 0)
	dead := ClientConfig{Host: "127.0.0.1", Port: closedPort(t), Connections: 2}

	// The backup has everything: nothing is missing, whatever the dead one says.
	backup := newStatServer(t, ids)
	if res := Precheck(context.Background(), []ClientConfig{dead, backup.cfg(2)}, nzb, PrecheckOptions{}); res.Skipped || res.Missing != 0 {
		t.Fatalf("a dead primary with a complete backup: %+v", res)
	}

	// The backup lacks an article: the dead server might have it, so no verdict.
	partial := copyIDs(ids)
	delete(partial, "f0-s1@example")
	backup2 := newStatServer(t, partial)
	res := Precheck(context.Background(), []ClientConfig{dead, backup2.cfg(2)}, nzb, PrecheckOptions{})
	if !res.Skipped || res.Hopeless() {
		t.Fatalf("a server that can't be asked must make the check skip: %+v", res)
	}
}

func TestPrecheckConnectionsNeverExceedTheLogin(t *testing.T) {
	nzb, ids := precheckNZB(3, 100, 0)
	srv := newStatServer(t, ids)
	res := Precheck(context.Background(), []ClientConfig{srv.cfg(1)}, nzb, PrecheckOptions{})
	if res.Skipped || res.Missing != 0 {
		t.Fatalf("unexpected %+v", res)
	}
	if srv.conns.Load() != 1 {
		t.Fatalf("a login with one connection got %d", srv.conns.Load())
	}
}

func TestPrecheckNothingToAskIsSkipped(t *testing.T) {
	nzb, _ := precheckNZB(1, 10, 0)
	if res := Precheck(context.Background(), nil, nzb, PrecheckOptions{}); !res.Skipped {
		t.Fatal("no server: skip")
	}
	if res := Precheck(context.Background(), []ClientConfig{{Host: "x", Port: 1}}, &NZB{}, PrecheckOptions{}); !res.Skipped {
		t.Fatal("no articles: skip")
	}
}

func TestPrecheckStopsWhenCancelled(t *testing.T) {
	nzb, ids := precheckNZB(3, 60, 0)
	srv := newStatServer(t, ids)
	srv.delay = 2 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	res := Precheck(ctx, []ClientConfig{srv.cfg(2)}, nzb, PrecheckOptions{})
	if !res.Skipped || time.Since(start) > time.Second {
		t.Fatalf("a cancelled check should stop at once: %+v after %v", res, time.Since(start))
	}
}

func TestPickSample(t *testing.T) {
	tests := []struct {
		name         string
		files, segs  int
		par2         int64
		opt          PrecheckOptions
		want         int
		firstAndLast bool
	}{
		{"small nzb is checked completely", 1, 6, 0, PrecheckOptions{}, 6, false},
		{"minimum of ten", 1, 200, 0, PrecheckOptions{}, 10, true},
		{"three percent", 5, 200, 0, PrecheckOptions{}, 30, true},
		{"never more than a hundred", 20, 500, 0, PrecheckOptions{}, 100, false},
		{"custom fraction", 4, 250, 0, PrecheckOptions{Fraction: 0.05}, 50, true},
		{"many large files: more must-haves than room", 100, 50, 0, PrecheckOptions{}, 100, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nzb, _ := precheckNZB(tt.files, tt.segs, tt.par2)
			got := pickSample(nzb, tt.opt.withDefaults())
			if len(got) != tt.want {
				t.Fatalf("sample of %d, want %d", len(got), tt.want)
			}
			seen := map[[2]int]bool{}
			for i, r := range got {
				if seen[[2]int{r.file, r.seg}] {
					t.Fatalf("%v chosen twice", r)
				}
				seen[[2]int{r.file, r.seg}] = true
				if i > 0 && (got[i-1].file > r.file || (got[i-1].file == r.file && got[i-1].seg >= r.seg)) {
					t.Fatalf("sample not in NZB order at %d", i)
				}
			}
			if tt.firstAndLast {
				for f := range nzb.Files {
					if !seen[[2]int{f, 0}] || !seen[[2]int{f, len(nzb.Files[f].Segments) - 1}] {
						t.Fatalf("file %d: first and last article must be in the sample", f)
					}
				}
			}
			// Spread: in a sample without must-haves, every tenth of the NZB has some.
			if tt.files == 1 && tt.segs == 200 {
				var idx []int
				for _, r := range got {
					idx = append(idx, r.seg)
				}
				sort.Ints(idx)
				if idx[len(idx)/2] < 60 || idx[len(idx)/2] > 140 {
					t.Fatalf("sample not spread over the file: %v", idx)
				}
			}
		})
	}
}

// The first and last articles of a file are always asked about, so they must not
// make the estimate for the whole release worse than it is.
func TestEstimateTreatsEndsOfFilesAsThemselves(t *testing.T) {
	nzb, _ := precheckNZB(2, 100, 0) // 200 articles of 1000 bytes
	sample := []segRef{
		{file: 0, seg: 0, forced: true}, {file: 0, seg: 99, forced: true},
		{file: 1, seg: 0, forced: true}, {file: 1, seg: 99, forced: true},
		{file: 0, seg: 30}, {file: 0, seg: 60}, {file: 1, seg: 30}, {file: 1, seg: 60},
	}
	tests := []struct {
		name  string
		found []bool
		want  int64
	}{
		{"all found", []bool{true, true, true, true, true, true, true, true}, 0},
		{"only forced lost", []bool{false, true, false, true, true, true, true, true}, 2000},
		{"one in four of the spread lost", []bool{true, true, true, true, false, true, true, true}, 0 + 196*1000/4},
		{"forced and spread lost", []bool{false, true, true, true, true, false, true, true}, 1000 + 196*1000/4},
	}
	for _, tt := range tests {
		if got := estimateMissingBytes(nzb, sample, tt.found); got != tt.want {
			t.Errorf("%s: estimate %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestStatAnswers(t *testing.T) {
	srv := newStatServer(t, map[string]bool{"here@x": true})
	c, err := DialNNTP(srv.addr, srv.port, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Quit()
	if ok, err := c.Stat("here@x"); !ok || err != nil {
		t.Fatalf("existing article: %v %v", ok, err)
	}
	if ok, err := c.Stat("<gone@x>"); ok || err != nil {
		t.Fatalf("missing article: %v %v", ok, err)
	}
	srv.noStat = true
	var ne *NNTPError
	if _, err := c.Stat("here@x"); !errors.As(err, &ne) || ne.Code != 500 {
		t.Fatalf("unsupported STAT: %v", err)
	}
}
