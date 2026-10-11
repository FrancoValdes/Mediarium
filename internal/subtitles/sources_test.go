package subtitles

import "testing"

func TestSourcesInEveryCombination(t *testing.T) {
	// ready/switch for OpenSubtitles, SubDL, add-ons.
	type rs struct{ ready, sw bool }
	cases := []struct {
		name                   string
		os, sd, ad             rs
		wantOS, wantSD, wantAd bool
		any, setUp, others     bool
		count                  int
	}{
		{"nothing set up", rs{false, true}, rs{false, true}, rs{false, true}, false, false, false, false, false, false, 0},
		{"only OpenSubtitles", rs{true, true}, rs{false, true}, rs{false, true}, true, false, false, true, true, false, 1},
		{"only SubDL", rs{false, true}, rs{true, true}, rs{false, true}, false, true, false, true, true, true, 1},
		{"only add-ons", rs{false, true}, rs{false, true}, rs{true, true}, false, false, true, true, true, true, 1},
		{"all three", rs{true, true}, rs{true, true}, rs{true, true}, true, true, true, true, true, true, 3},
		{"OpenSubtitles off, SubDL on", rs{true, false}, rs{true, true}, rs{false, true}, false, true, false, true, true, true, 1},
		{"SubDL off, OpenSubtitles on", rs{true, true}, rs{true, false}, rs{false, true}, true, false, false, true, true, false, 1},
		{"add-ons only, the others off", rs{true, false}, rs{true, false}, rs{true, true}, false, false, true, true, true, true, 1},
		{"all switched off", rs{true, false}, rs{true, false}, rs{true, false}, false, false, false, false, true, false, 0},
		{"switch on without a key counts as off", rs{false, true}, rs{false, true}, rs{false, true}, false, false, false, false, false, false, 0},
		{"two of three", rs{true, true}, rs{false, true}, rs{true, true}, true, false, true, true, true, true, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Sources{
				OpenSubtitles: SourceSetup{Ready: c.os.ready, Switch: c.os.sw},
				SubDL:         SourceSetup{Ready: c.sd.ready, Switch: c.sd.sw},
				Addons:        SourceSetup{Ready: c.ad.ready, Switch: c.ad.sw},
			}
			if s.OpenSubtitles.On() != c.wantOS || s.SubDL.On() != c.wantSD || s.Addons.On() != c.wantAd {
				t.Errorf("on: os=%v subdl=%v addons=%v", s.OpenSubtitles.On(), s.SubDL.On(), s.Addons.On())
			}
			if s.Any() != c.any || s.AnySetUp() != c.setUp || s.OthersThanOpenSubtitles() != c.others || s.Count() != c.count {
				t.Errorf("any=%v setUp=%v others=%v count=%d", s.Any(), s.AnySetUp(), s.OthersThanOpenSubtitles(), s.Count())
			}
		})
	}
}

func TestSourceState(t *testing.T) {
	cases := []struct {
		name  string
		s     SourceSetup
		limit bool
		want  string
	}{
		{"no key", SourceSetup{Ready: false, Switch: true}, false, StateNeedsKey},
		{"no key and switched off", SourceSetup{Ready: false, Switch: false}, false, StateNeedsKey},
		{"key, switched off", SourceSetup{Ready: true, Switch: false}, false, StateOff},
		{"key, switched off, limit used up", SourceSetup{Ready: true, Switch: false}, true, StateOff},
		{"key, on", SourceSetup{Ready: true, Switch: true}, false, StateOn},
		{"key, on, limit used up", SourceSetup{Ready: true, Switch: true}, true, StateLimit},
	}
	for _, c := range cases {
		if got := c.s.State(c.limit); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
