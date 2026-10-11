package torrentclient

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/upnp"
)

func TestJunkFile(t *testing.T) {
	const mb = 1 << 20
	tests := []struct {
		name     string
		path     string
		size     int64
		keepText bool
		want     bool
	}{
		{"an nfo", "Movie.2020.1080p/movie.nfo", 2000, false, true},
		{"a text file", "Movie/readme.txt", 500, false, true},
		{"a text file in a book", "Book/chapter.txt", 500, true, false},
		{"an nfo in a book is still junk", "Book/info.nfo", 500, true, true},
		{"a program", "Movie/Get This.EXE", 90_000, false, true},
		{"a web link", "Movie/Visit Us.url", 100, false, true},
		{"the movie", "Movie.2020/Movie.2020.1080p.mkv", 8_000 * mb, false, false},
		{"a sample clip by name", "Movie.2020/movie-sample.mkv", 40 * mb, false, true},
		{"a sample clip called sample", "Movie.2020/Sample.mp4", 40 * mb, false, true},
		{"an episode sample", "Show.S01/Show.S01E01.sample.mkv", 30 * mb, false, true},
		{"a file in a Sample folder", "Movie.2020/Sample/clip.mkv", 20 * mb, false, true},
		{"a picture in a samples folder", "Movie.2020/samples/frame.jpg", 90_000, false, true},
		{"sampler is not sample", "Album/Sampler.mkv", 20 * mb, false, false},
		{"a film that has sample in the title", "The Sample/The Sample 2019.mkv", 2_000 * mb, false, false},
		{"a big file in a Sample folder is the real thing", "Show/Sample/Show.mkv", 2_000 * mb, false, false},
		{"a subtitle", "Movie/Subs/English.srt", 70_000, false, false},
		{"a cover", "Album/cover.jpg", 300_000, false, false},
		{"windows separators", "Movie\\Sample\\a.mkv", 10 * mb, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := JunkFile(tc.path, tc.size, tc.keepText); got != tc.want {
				t.Fatalf("JunkFile(%q, %d) = %v, want %v", tc.path, tc.size, got, tc.want)
			}
		})
	}
}

func TestJunkPathsNeverLeavesNothing(t *testing.T) {
	only := []FileState{{Path: "a/readme.txt", Size: 10}, {Path: "a/x.nfo", Size: 10}}
	if got := JunkPaths(only, false); got != nil {
		t.Fatalf("a torrent of only junk must keep everything, got %v", got)
	}
	mixed := []FileState{{Path: "a/movie.mkv", Size: 1 << 30}, {Path: "a/x.nfo", Size: 10}}
	if got := JunkPaths(mixed, false); len(got) != 1 || got[0] != "a/x.nfo" {
		t.Fatalf("junk = %v", got)
	}
}

func TestBucketHoldsATorrentThatTookTooMuch(t *testing.T) {
	var b bucket
	const limit = 1000 // bytes per second
	step := 250 * time.Millisecond
	var counter int64
	// Takes 4000 bytes in one tick: far over the 250 it may have.
	if b.update(limit, counter, step) {
		t.Fatal("the first look only reads the counter")
	}
	counter += 4000
	if !b.update(limit, counter, step) {
		t.Fatal("a torrent that took 16 times its share must be held")
	}
	// Idle ticks pay the debt back at 250 bytes a tick: 3750 owed.
	held := 0
	for i := 0; i < 40 && b.update(limit, counter, step); i++ {
		held++
	}
	if held < 13 || held > 16 {
		t.Fatalf("held for %d ticks, want about 15", held)
	}
	// Idle for a long time saves up at most half a second.
	for i := 0; i < 100; i++ {
		b.update(limit, counter, step)
	}
	if b.tokens > limit*burstSeconds+1 {
		t.Fatalf("saved up %v bytes, want at most %v", b.tokens, limit*burstSeconds)
	}
	// No limit never holds, whatever was moved.
	counter += 1 << 30
	if b.update(0, counter, step) {
		t.Fatal("no limit must never hold")
	}
}

func TestSequentialPlan(t *testing.T) {
	// Two files: pieces 0-19 and 20-29 (a big movie and a short one).
	spans := []pieceSpan{{0, 20}, {20, 30}}
	complete := make([]bool, 30)
	plan := sequentialPlan(spans, complete, 12)

	for i, want := range map[int]torrent.PiecePriority{
		0: torrent.PiecePriorityNow, 1: torrent.PiecePriorityNow,
		2: torrent.PiecePriorityNext, 5: torrent.PiecePriorityNext,
		6: torrent.PiecePriorityReadahead, 11: torrent.PiecePriorityReadahead,
		19: torrent.PiecePriorityNext, // the end of the file being read
		20: torrent.PiecePriorityHigh, 29: torrent.PiecePriorityHigh,
	} {
		if plan[i] != want {
			t.Errorf("piece %d has priority %v, want %v", i, plan[i], want)
		}
	}
	if _, ok := plan[12]; ok {
		t.Error("a piece beyond the window keeps its file's priority")
	}

	// As pieces arrive the window moves on and finished pieces drop out.
	for i := 0; i < 6; i++ {
		complete[i] = true
	}
	plan = sequentialPlan(spans, complete, 12)
	if _, ok := plan[3]; ok {
		t.Error("a finished piece must not be in the plan")
	}
	if plan[6] != torrent.PiecePriorityNow || plan[7] != torrent.PiecePriorityNow || plan[8] != torrent.PiecePriorityNext {
		t.Errorf("the window should start at the first missing piece: %v %v %v", plan[6], plan[7], plan[8])
	}

	// When the first file is finished the second one is the one being read.
	for i := 0; i < 20; i++ {
		complete[i] = true
	}
	plan = sequentialPlan(spans, complete, 12)
	if plan[20] != torrent.PiecePriorityNow || plan[29] != torrent.PiecePriorityNext {
		t.Errorf("second file: first %v, last %v", plan[20], plan[29])
	}

	// A file that shares a piece with the next one is not asked for twice.
	shared := sequentialPlan([]pieceSpan{{0, 3}, {2, 5}}, make([]bool, 5), 10)
	if len(shared) != 5 {
		t.Errorf("plan has %d pieces for 5 pieces", len(shared))
	}

	// Everything finished: nothing to ask for.
	for i := range complete {
		complete[i] = true
	}
	if plan = sequentialPlan(spans, complete, 12); len(plan) != 0 {
		t.Errorf("a finished torrent has a plan: %v", plan)
	}
}

func TestParseDefaultGateway(t *testing.T) {
	table := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0100A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eth0\t0000A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	ip, err := parseDefaultGateway(table)
	if err != nil || ip.String() != "192.168.0.1" {
		t.Fatalf("gateway = %v, %v", ip, err)
	}
	if _, err := parseDefaultGateway("Iface\tDestination\tGateway\tFlags\n"); err == nil {
		t.Fatal("a table with no default route must be an error")
	}
}

// fakeRouter answers NAT-PMP on a local UDP port, the way RFC 6886 says.
type fakeRouter struct {
	conn *net.UDPConn
	mu   sync.Mutex // guards everything below: the test and the router goroutine both use it
	// mapped is opcode -> external port granted.
	mapped   map[byte]int
	grant    int // external port to hand out (0 = the one asked for)
	refuse   bool
	removals int
}

func (r *fakeRouter) set(grant int, refuse bool) {
	r.mu.Lock()
	r.grant, r.refuse = grant, refuse
	r.mu.Unlock()
}

func (r *fakeRouter) mappedPort(op byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mapped[op]
}

func (r *fakeRouter) mappedCopy() map[byte]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[byte]int, len(r.mapped))
	for k, v := range r.mapped {
		out[k] = v
	}
	return out
}

func (r *fakeRouter) removalCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.removals
}

func newFakeRouter(t *testing.T) *fakeRouter {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRouter{conn: conn, mapped: map[byte]int{}}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 64)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			switch {
			case n == 2 && buf[1] == 0:
				resp := make([]byte, 12)
				resp[1] = 128
				copy(resp[8:], net.IPv4(203, 0, 113, 9).To4())
				conn.WriteToUDP(resp, from)
			case n == 12 && (buf[1] == natpmpTCP || buf[1] == natpmpUDP):
				resp := make([]byte, 16)
				resp[1] = 128 + buf[1]
				r.mu.Lock()
				if r.refuse {
					binary.BigEndian.PutUint16(resp[2:], 2) // not authorized
				}
				lifetime := binary.BigEndian.Uint32(buf[8:])
				if lifetime == 0 {
					r.removals++
				}
				ext := int(binary.BigEndian.Uint16(buf[6:]))
				if r.grant != 0 {
					ext = r.grant
				}
				r.mapped[buf[1]] = ext
				r.mu.Unlock()
				copy(resp[8:], buf[4:6])
				binary.BigEndian.PutUint16(resp[10:], uint16(ext))
				binary.BigEndian.PutUint32(resp[12:], lifetime)
				conn.WriteToUDP(resp, from)
			}
		}
	}()
	return r
}

func (r *fakeRouter) mapper(port int, withUPnP []upnp.Device) *portMapper {
	m := newPortMapper(func() int { return port })
	m.discover = func(time.Duration) []upnp.Device { return withUPnP }
	m.gateway = func() (net.IP, error) { return net.IPv4(127, 0, 0, 1), nil }
	m.gwPort = r.conn.LocalAddr().(*net.UDPAddr).Port
	return m
}

func waitForState(t *testing.T, m *portMapper, want string) PortMapStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := m.Status(); s.State == want {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("port mapping is %+v, wanted state %q", m.Status(), want)
	return PortMapStatus{}
}

func TestPortMappingByNATPMP(t *testing.T) {
	router := newFakeRouter(t)
	router.set(61234, false)
	m := router.mapper(58264, nil)
	if m.Status().State != MapOff {
		t.Fatal("a mapper that was never started is off")
	}
	m.Start()
	got := waitForState(t, m, MapMapped)
	if got.Method != "NAT-PMP" || got.Port != 61234 || got.ExternalIP != "203.0.113.9" {
		t.Fatalf("status = %+v", got)
	}
	if router.mappedPort(natpmpTCP) != 61234 {
		t.Fatalf("the router mapped %v", router.mappedCopy())
	}
	m.Stop()
	if m.Status().State != MapOff {
		t.Fatal("a stopped mapper is off")
	}
	if n := router.removalCount(); n < 2 {
		t.Fatalf("the mapping should be taken off the router when stopped, saw %d removals", n)
	}
}

func TestPortMappingSaysNoneWhenNoRouterAnswers(t *testing.T) {
	router := newFakeRouter(t)
	router.set(0, true)
	m := router.mapper(58264, nil)
	m.Start()
	defer m.Stop()
	got := waitForState(t, m, MapNone)
	if got.Port != 0 || got.Method != "" {
		t.Fatalf("status = %+v", got)
	}

	// A gateway that cannot be found is the same answer.
	m2 := newPortMapper(func() int { return 58264 })
	m2.discover = func(time.Duration) []upnp.Device { return nil }
	m2.gateway = func() (net.IP, error) { return nil, errors.New("no default route") }
	m2.Start()
	defer m2.Stop()
	waitForState(t, m2, MapNone)
}

type fakeUPnP struct {
	added   []upnp.Protocol
	deleted []upnp.Protocol
}

func (f *fakeUPnP) ID() string                            { return "fake-igd" }
func (f *fakeUPnP) GetLocalIPAddress() net.IP             { return net.IPv4(192, 168, 1, 5) }
func (f *fakeUPnP) GetExternalIPAddress() (net.IP, error) { return net.IPv4(198, 51, 100, 7), nil }
func (f *fakeUPnP) AddPortMapping(p upnp.Protocol, internal, external int, _ string, _ time.Duration) (int, error) {
	f.added = append(f.added, p)
	return external, nil
}
func (f *fakeUPnP) DeletePortMapping(p upnp.Protocol, _ int) error {
	f.deleted = append(f.deleted, p)
	return nil
}

func TestPortMappingPrefersUPnP(t *testing.T) {
	router := newFakeRouter(t)
	dev := &fakeUPnP{}
	m := router.mapper(58264, []upnp.Device{dev})
	m.Start()
	got := waitForState(t, m, MapMapped)
	if got.Method != "UPnP" || got.Port != 58264 || got.ExternalIP != "198.51.100.7" {
		t.Fatalf("status = %+v", got)
	}
	if len(router.mappedCopy()) != 0 {
		t.Fatal("NAT-PMP must not be asked when UPnP worked")
	}
	m.Stop()
	if len(dev.added) != 2 || len(dev.deleted) != 2 {
		t.Fatalf("UPnP added %v and deleted %v; both TCP and UDP should be mapped and removed", dev.added, dev.deleted)
	}
}

func TestSwitchingPortMappingOffWhileLookingLeavesItOff(t *testing.T) {
	router := newFakeRouter(t)
	release := make(chan struct{})
	m := router.mapper(58264, nil)
	var searchAgain atomic.Bool
	m.discover = func(time.Duration) []upnp.Device {
		if !searchAgain.Load() {
			<-release // a router search that takes its time
		}
		return nil
	}
	m.Start()
	if m.Status().State != MapWorking {
		t.Fatalf("status = %+v, want working", m.Status())
	}
	m.Stop()
	close(release)
	time.Sleep(300 * time.Millisecond)
	if st := m.Status().State; st != MapOff {
		t.Fatalf("a mapper that was switched off says %q", st)
	}
	if got := router.mappedCopy(); len(got) != 0 {
		t.Fatalf("nothing may be opened on the router after it was switched off: %v", got)
	}
	// It can be switched on again afterwards.
	searchAgain.Store(true)
	m.Start()
	defer m.Stop()
	waitForState(t, m, MapMapped)
}

func TestNATPMPExchangeStopsWhenAskedTo(t *testing.T) {
	// A port nobody answers on.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := natpmpMap(ctx, conn.LocalAddr().(*net.UDPAddr), natpmpTCP, 1, 1, 10); err == nil {
		t.Fatal("no answer must be an error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the exchange ignored the deadline")
	}
}
