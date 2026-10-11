package torrentclient

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/log"
	"github.com/anacrolix/upnp"
)

// Port mapping asks the home router to forward the engine's listen port to this
// machine, so peers on the internet can connect to us. Routers speak one of two
// protocols: UPnP (an "Internet Gateway Device" found by multicast) or NAT-PMP
// (RFC 6886, a small UDP exchange with the default gateway). UPnP is tried
// first, NAT-PMP when no UPnP router answers.
//
// The engine can do UPnP by itself, but it keeps the result private; doing it
// here lets the Download clients page say whether the port is open.

// Map states reported by PortMapStatus.
const (
	MapOff     = "off"     // port mapping is switched off, or the VPN is on
	MapWorking = "working" // looking for a router
	MapMapped  = "mapped"  // the router opened the port
	MapNone    = "none"    // no router did
)

// PortMapStatus says what port mapping has done.
type PortMapStatus struct {
	State      string `json:"state"`
	Method     string `json:"method,omitempty"` // "UPnP" or "NAT-PMP" when mapped
	Port       int    `json:"port,omitempty"`   // the port open on the router (the outside port)
	ExternalIP string `json:"externalIp,omitempty"`
}

const (
	natpmpPort     = 5351
	natpmpLifetime = 3600 // seconds asked for; renewed at half
	mapRetry       = 30 * time.Minute
	upnpWait       = 2 * time.Second
)

// portMapper keeps the engine's port mapped while it is switched on.
type portMapper struct {
	port func() int // the internal port to map (0 = nothing to map)

	// Replaceable for tests.
	discover func(timeout time.Duration) []upnp.Device
	gateway  func() (net.IP, error)
	gwPort   int

	mu      sync.Mutex
	status  PortMapStatus
	cancel  context.CancelFunc
	stopped chan struct{}
}

func newPortMapper(port func() int) *portMapper {
	return &portMapper{
		port: port,
		discover: func(timeout time.Duration) []upnp.Device {
			return upnp.Discover(0, timeout, log.Default)
		},
		gateway: defaultGateway,
		gwPort:  natpmpPort,
		status:  PortMapStatus{State: MapOff},
	}
}

// Status is what the mapper has done so far.
func (m *portMapper) Status() PortMapStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *portMapper) set(s PortMapStatus) {
	m.mu.Lock()
	m.status = s
	m.mu.Unlock()
}

// Start begins mapping in the background. It does nothing if it is running.
func (m *portMapper) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.stopped = make(chan struct{})
	m.status = PortMapStatus{State: MapWorking}
	go m.run(ctx, m.stopped)
}

// Stop ends mapping and takes the mapping off the router (waiting a few seconds
// for it).
func (m *portMapper) Stop() {
	m.mu.Lock()
	cancel, stopped := m.cancel, m.stopped
	wasMapped := m.status.State == MapMapped
	m.cancel, m.stopped = nil, nil
	m.status = PortMapStatus{State: MapOff}
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if !wasMapped {
		return // nothing on the router to take off
	}
	select {
	case <-stopped:
	case <-time.After(1500 * time.Millisecond):
	}
}

// mapping is what one successful attempt left on the router.
type mapping struct {
	method  string
	port    int
	ip      string
	renewAt time.Duration // 0 = never needs renewing
	remove  func()
}

func (m *portMapper) run(ctx context.Context, stopped chan struct{}) {
	defer close(stopped)
	var held *mapping
	defer func() {
		if held != nil && held.remove != nil {
			held.remove()
		}
	}()
	for {
		wait := mapRetry
		port := m.port()
		switch {
		case port == 0:
			m.set(PortMapStatus{State: MapNone})
		default:
			got := m.attempt(ctx, port)
			if ctx.Err() != nil {
				// Switched off meanwhile: take back what was just opened, and
				// do not report a result that nobody asked for any more.
				if got != nil {
					held = got
				}
				return
			}
			switch {
			case got != nil:
				if held != nil && held.remove != nil && held.method != got.method {
					held.remove()
				}
				held = got
				m.set(PortMapStatus{State: MapMapped, Method: got.method, Port: got.port, ExternalIP: got.ip})
				if got.renewAt > 0 {
					wait = got.renewAt
				} else {
					wait = 0 // UPnP mappings are kept until we remove them
				}
			case held != nil:
				// A renewal failed: say so, and try again soon.
				m.set(PortMapStatus{State: MapNone})
				held = nil
			default:
				m.set(PortMapStatus{State: MapNone})
			}
		}
		if wait == 0 {
			<-ctx.Done()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// attempt maps port once, by UPnP and then NAT-PMP. It returns nil when no
// router took it.
func (m *portMapper) attempt(ctx context.Context, port int) *mapping {
	if got := m.tryUPnP(ctx, port); got != nil {
		return got
	}
	if ctx.Err() != nil {
		return nil
	}
	got, err := m.tryNATPMP(ctx, port)
	if err != nil {
		slog.Info("torrent: no router opened the listen port", "port", port, "natpmp", err.Error())
		return nil
	}
	return got
}

func (m *portMapper) tryUPnP(ctx context.Context, port int) *mapping {
	devices := m.discover(upnpWait)
	if ctx.Err() != nil {
		return nil // switched off while looking: leave the router alone
	}
	for _, d := range devices {
		ext, err := d.AddPortMapping(upnp.TCP, port, port, "Mediarium", 0)
		if err != nil {
			slog.Info("torrent: a UPnP router refused the port", "port", port, "err", err.Error())
			continue
		}
		// Peers over uTP use the same port on UDP; it is no problem if that one fails.
		extUDP, udpErr := d.AddPortMapping(upnp.UDP, port, port, "Mediarium", 0)
		ip := ""
		if a, err := d.GetExternalIPAddress(); err == nil && a != nil {
			ip = a.String()
		}
		slog.Info("torrent: UPnP opened the listen port", "port", port, "external_port", ext)
		return &mapping{method: "UPnP", port: ext, ip: ip, remove: func() {
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = d.DeletePortMapping(upnp.TCP, ext)
				if udpErr == nil {
					_ = d.DeletePortMapping(upnp.UDP, extUDP)
				}
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
		}}
	}
	return nil
}

func (m *portMapper) tryNATPMP(ctx context.Context, port int) (*mapping, error) {
	gw, err := m.gateway()
	if err != nil {
		return nil, err
	}
	addr := &net.UDPAddr{IP: gw, Port: m.gwPort}
	ext, life, err := natpmpMap(ctx, addr, natpmpTCP, port, port, natpmpLifetime)
	if err != nil {
		return nil, err
	}
	// The UDP side is a bonus (uTP); the router may say no.
	_, _, udpErr := natpmpMap(ctx, addr, natpmpUDP, port, ext, natpmpLifetime)
	ip := ""
	if a, err := natpmpExternalIP(ctx, addr); err == nil {
		ip = a.String()
	}
	slog.Info("torrent: NAT-PMP opened the listen port", "port", port, "external_port", ext)
	renew := time.Duration(life) * time.Second / 2
	if renew < time.Minute {
		renew = time.Minute
	}
	return &mapping{method: "NAT-PMP", port: ext, ip: ip, renewAt: renew, remove: func() {
		// A lifetime of 0 removes the mapping (RFC 6886 section 3.4).
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _, _ = natpmpMap(rctx, addr, natpmpTCP, port, 0, 0)
		if udpErr == nil {
			_, _, _ = natpmpMap(rctx, addr, natpmpUDP, port, 0, 0)
		}
	}}, nil
}

// NAT-PMP (RFC 6886).
const (
	natpmpUDP = 1 // opcode for a UDP mapping
	natpmpTCP = 2 // opcode for a TCP mapping
)

// natpmpExchange sends req to the gateway and waits for a reply of the wanted
// kind, retrying with growing waits as the RFC says (shortened).
func natpmpExchange(ctx context.Context, gw *net.UDPAddr, req []byte, wantOp byte, wantLen int) ([]byte, error) {
	conn, err := net.DialUDP("udp", nil, gw)
	if err != nil {
		return nil, fmt.Errorf("reach the gateway: %w", err)
	}
	defer conn.Close()
	buf := make([]byte, 64)
	wait := 250 * time.Millisecond
	for try := 0; try < 3; try++ {
		if _, err := conn.Write(req); err != nil {
			return nil, fmt.Errorf("send to the gateway: %w", err)
		}
		deadline := time.Now().Add(wait)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetReadDeadline(deadline)
		n, err := conn.Read(buf)
		if err == nil {
			if n >= wantLen && buf[0] == 0 && buf[1] == wantOp {
				code := binary.BigEndian.Uint16(buf[2:4])
				if code != 0 {
					return nil, fmt.Errorf("the gateway answered with result code %d", code)
				}
				return append([]byte(nil), buf[:n]...), nil
			}
			continue // not for us
		}
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			return nil, fmt.Errorf("read from the gateway: %w", err)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		wait *= 2
	}
	return nil, errors.New("the gateway did not answer")
}

func natpmpExternalIP(ctx context.Context, gw *net.UDPAddr) (net.IP, error) {
	resp, err := natpmpExchange(ctx, gw, []byte{0, 0}, 128, 12)
	if err != nil {
		return nil, err
	}
	return net.IP(resp[8:12]), nil
}

// natpmpMap asks the gateway to forward external port suggested to this
// machine's internal port, for lifetime seconds. It returns the external port
// it was given and the lifetime granted.
func natpmpMap(ctx context.Context, gw *net.UDPAddr, op byte, internal, suggested int, lifetime uint32) (int, uint32, error) {
	req := make([]byte, 12)
	req[1] = op
	binary.BigEndian.PutUint16(req[4:], uint16(internal))
	binary.BigEndian.PutUint16(req[6:], uint16(suggested))
	binary.BigEndian.PutUint32(req[8:], lifetime)
	resp, err := natpmpExchange(ctx, gw, req, 128+op, 16)
	if err != nil {
		return 0, 0, err
	}
	return int(binary.BigEndian.Uint16(resp[10:12])), binary.BigEndian.Uint32(resp[12:16]), nil
}

// defaultGateway reads the default route from the system (Linux).
func defaultGateway() (net.IP, error) {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil, fmt.Errorf("find the router: %w", err)
	}
	return parseDefaultGateway(string(b))
}

// parseDefaultGateway finds the gateway of the default route in the text of
// /proc/net/route, where addresses are little-endian hex.
func parseDefaultGateway(table string) (net.IP, error) {
	for i, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 4 || f[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&0x2 == 0 { // RTF_GATEWAY
			continue
		}
		v, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil || v == 0 {
			continue
		}
		ip := make(net.IP, 4)
		binary.LittleEndian.PutUint32(ip, uint32(v))
		return ip, nil
	}
	return nil, errors.New("no default route")
}
