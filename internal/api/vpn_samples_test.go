package api

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// samplePayload reads one of the provider sample configs in
// internal/vpn/testdata/providers the way the Settings page does when a config
// is pasted: the first [Peer] only, DNS entries that are addresses, and the
// lists split on commas.
func samplePayload(t *testing.T, path string) vpnConfigPayload {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := vpnConfigPayload{Label: "Sample", Provider: strings.TrimSuffix(filepath.Base(path), ".conf")}
	section, peers := "", 0
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			if section == "peer" {
				if peers++; peers > 1 {
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
		var list []string
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				list = append(list, part)
			}
		}
		switch {
		case section == "interface" && key == "privatekey":
			p.PrivateKey = value
		case section == "interface" && key == "address":
			p.LocalAddresses = append(p.LocalAddresses, list...)
		case section == "interface" && key == "dns":
			for _, d := range list {
				if _, err := netip.ParseAddr(d); err == nil {
					p.DNS = append(p.DNS, d)
				}
			}
		case section == "peer" && key == "publickey":
			p.PeerPublicKey = value
		case section == "peer" && key == "presharedkey":
			p.PresharedKey = value
		case section == "peer" && key == "endpoint":
			p.Endpoint = value
		case section == "peer" && key == "allowedips":
			p.AllowedIPs = append(p.AllowedIPs, list...)
		}
	}
	return p
}

// A config written the way each provider documents theirs must pass the checks
// the server runs when a connection is added, so a pasted file is never turned
// away for a detail the provider's own format has (an address without a
// prefix, ::0/0, two DNS servers, a host name with a port).
func TestProviderSampleConfigsAreAccepted(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "vpn", "testdata", "providers", "*.conf"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no sample configs found: %v", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".conf")
		t.Run(name, func(t *testing.T) {
			p := cleanVPNConfig(samplePayload(t, file))
			if msg := checkVPNConfig(p); msg != "" {
				t.Fatalf("the %s sample was refused: %s", name, msg)
			}
		})
	}
}
