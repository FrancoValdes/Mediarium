package vpn

import (
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The files in testdata/providers are sample WireGuard configs written in the
// shape each provider documents, with made-up keys and documentation-range
// addresses where the real value is not the point. The Settings page reads the
// same files in its own tests (web/src/vpnProviders.test.ts), so the form
// parser and the tunnel are checked against one set of samples.

// sampleConf is one provider's file, read the way wg-quick reads it.
type sampleConf struct {
	cfg Config
	// hostName is the endpoint's host when the file names the server by name.
	hostName string
}

func readSampleConf(t *testing.T, path string) sampleConf {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var s sampleConf
	section, peers := "", 0
	for _, line := range strings.Split(string(raw), "\n") {
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			if section == "peer" {
				peers++
				if peers > 1 {
					section = "ignored"
				}
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		list := func() []string {
			var out []string
			for _, p := range strings.Split(value, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			return out
		}
		switch section {
		case "interface":
			switch key {
			case "privatekey":
				s.cfg.PrivateKey = value
			case "address":
				s.cfg.LocalAddresses = append(s.cfg.LocalAddresses, list()...)
			case "dns":
				for _, d := range list() {
					if _, err := netip.ParseAddr(d); err == nil {
						s.cfg.DNS = append(s.cfg.DNS, d)
					}
				}
			case "mtu":
				s.cfg.MTU, _ = strconv.Atoi(value)
			}
		case "peer":
			switch key {
			case "publickey":
				s.cfg.PeerPublicKey = value
			case "presharedkey":
				s.cfg.PresharedKey = value
			case "endpoint":
				s.cfg.Endpoint = value
			case "allowedips":
				s.cfg.AllowedIPs = append(s.cfg.AllowedIPs, list()...)
			case "persistentkeepalive":
				s.cfg.PersistentKeepalive, _ = strconv.Atoi(value)
			}
		}
	}
	if host, _, err := net.SplitHostPort(s.cfg.Endpoint); err == nil {
		if _, err := netip.ParseAddr(host); err != nil {
			s.hostName = host
		}
	}
	return s
}

// Every provider's sample config must be accepted by the tunnel and reach a
// WireGuard handshake with a stand-in server on this machine. The keys in the
// files are made up, so the test swaps in a fresh pair on each side; everything
// else (addresses, DNS, allowed IPs, preshared key, MTU, keepalive, a host name
// as the endpoint) is used as the file has it. No real VPN is contacted.
func TestProviderSamplesReachHandshake(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "providers", "*.conf"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no sample configs found: %v", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".conf")
		t.Run(name, func(t *testing.T) {
			sample := readSampleConf(t, file)
			cfg := sample.cfg
			if cfg.PrivateKey == "" || cfg.PeerPublicKey == "" || cfg.Endpoint == "" || len(cfg.LocalAddresses) == 0 {
				t.Fatalf("the sample is missing a field: %+v", cfg)
			}
			if _, err := base64KeyToHex(cfg.PrivateKey); err != nil {
				t.Fatalf("the sample's private key is not a valid key: %v", err)
			}
			if _, err := base64KeyToHex(cfg.PeerPublicKey); err != nil {
				t.Fatalf("the sample's public key is not a valid key: %v", err)
			}
			if cfg.PresharedKey != "" {
				if _, err := base64KeyToHex(cfg.PresharedKey); err != nil {
					t.Fatalf("the sample's preshared key is not a valid key: %v", err)
				}
			}

			client, err := GenerateKeyPair()
			if err != nil {
				t.Fatal(err)
			}
			server, err := GenerateKeyPair()
			if err != nil {
				t.Fatal(err)
			}

			// The stand-in server only needs to know the client's tunnel addresses.
			// A file may write an address without a /prefix (PIA's does), which
			// means a single address.
			var serverAllowed []string
			for _, a := range cfg.LocalAddresses {
				if !strings.Contains(a, "/") {
					if strings.Contains(a, ":") {
						a += "/128"
					} else {
						a += "/32"
					}
				}
				serverAllowed = append(serverAllowed, a)
			}
			srv, err := Up(Config{
				PrivateKey:     server.PrivateKey,
				PeerPublicKey:  client.PublicKey,
				PresharedKey:   cfg.PresharedKey,
				AllowedIPs:     serverAllowed,
				LocalAddresses: []string{"10.200.0.1/32"},
			})
			if err != nil {
				t.Fatalf("bring up the stand-in server: %v", err)
			}
			defer srv.Close()
			port, err := srv.ListenPort()
			if err != nil {
				t.Fatal(err)
			}

			// A provider that names its servers is looked up by name each time,
			// so keep the name and point it at this machine.
			cfg.PrivateKey = client.PrivateKey
			cfg.PeerPublicKey = server.PublicKey
			if sample.hostName != "" {
				old := lookupIP
				defer func() { lookupIP = old }()
				lookupIP = func(_ context.Context, _, host string) ([]netip.Addr, error) {
					if host == sample.hostName {
						return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
					}
					return old(context.Background(), "ip", host)
				}
				cfg.Endpoint = net.JoinHostPort(sample.hostName, strconv.Itoa(port))
			} else {
				cfg.Endpoint = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
			}
			// The same default the manager applies when the file has none.
			cfg = withKeepalive(cfg)

			tun, err := Up(cfg)
			if err != nil {
				t.Fatalf("the tunnel refused the %s sample: %v", name, err)
			}
			defer tun.Close()

			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				at, err := tun.LastHandshake()
				if err != nil {
					t.Fatal(err)
				}
				if !at.IsZero() {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
			t.Fatalf("no handshake with the stand-in server for the %s sample", name)
		})
	}
}
