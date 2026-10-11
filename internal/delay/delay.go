// Package delay holds back automatic grabs from the protocol you like less.
//
// It follows the delay profiles of Radarr and Sonarr: a preferred protocol,
// a wait in minutes for Usenet releases and one for torrents, and the option
// to skip the wait for a release that is already the best quality the profile
// allows. The wait counts from when the release was posted. Only searches
// Mediarium starts by itself are delayed; a search or grab you start by hand
// never waits.
package delay

import (
	"errors"
	"fmt"
	"time"
)

// Protocols a release can come by.
const (
	Usenet  = "usenet"
	Torrent = "torrent"
)

// MaxMinutes is the longest wait that can be set (30 days).
const MaxMinutes = 30 * 24 * 60

// Config is the delay profile. The zero value does nothing: no wait for
// either protocol.
type Config struct {
	// PreferredProtocol is "usenet" or "torrent". Between two equally good
	// releases the one from this protocol is taken, and only this protocol's
	// releases can skip the wait (see BypassIfHighestQuality).
	PreferredProtocol string `json:"preferredProtocol"`
	// UsenetMinutes and TorrentMinutes are how long to wait, from the time a
	// release was posted, before an automatic search may take it. 0 = no wait.
	UsenetMinutes  int `json:"usenetMinutes"`
	TorrentMinutes int `json:"torrentMinutes"`
	// BypassIfHighestQuality takes a release of the preferred protocol at once
	// when it is the best quality the title's profile allows.
	BypassIfHighestQuality bool `json:"bypassIfHighestQuality"`
}

// Default is what an install has until someone changes it: usenet preferred,
// no wait.
func Default() Config { return Config{PreferredProtocol: Usenet} }

// Active reports whether any release can be held back.
func (c Config) Active() bool { return c.UsenetMinutes > 0 || c.TorrentMinutes > 0 }

// Preferred returns the preferred protocol, usenet when none is set.
func (c Config) Preferred() string {
	if c.PreferredProtocol == Torrent {
		return Torrent
	}
	return Usenet
}

// Validate says what is wrong with c, in plain words, or returns nil.
func (c Config) Validate() error {
	switch c.PreferredProtocol {
	case "", Usenet, Torrent:
	default:
		return errors.New(`Choose "usenet" or "torrent" as the preferred protocol.`)
	}
	for _, m := range []struct {
		name string
		v    int
	}{{"The Usenet wait", c.UsenetMinutes}, {"The torrent wait", c.TorrentMinutes}} {
		if m.v < 0 || m.v > MaxMinutes {
			return fmt.Errorf("%s must be from 0 to %d minutes.", m.name, MaxMinutes)
		}
	}
	return nil
}

// Minutes is the wait for a protocol.
func (c Config) Minutes(protocol string) int {
	if protocol == Torrent {
		return c.TorrentMinutes
	}
	return c.UsenetMinutes
}

// Release is what the delay needs to know about a release.
type Release struct {
	Protocol string
	// Posted is when the indexer says the release was posted; zero when it says
	// nothing. Then FirstSeen, when Mediarium first saw it, counts instead.
	Posted    time.Time
	FirstSeen time.Time
	// Highest is true when the release is the best quality the title's profile
	// allows.
	Highest bool
}

// Decision is whether a release has to wait, and until when.
type Decision struct {
	Hold  bool
	Until time.Time
}

// Decide says whether an automatic search must leave the release alone for
// now. now is passed in so the answer can be tested.
func (c Config) Decide(r Release, now time.Time) Decision {
	wait := time.Duration(c.Minutes(r.Protocol)) * time.Minute
	if wait <= 0 {
		return Decision{}
	}
	if c.BypassIfHighestQuality && r.Highest && r.Protocol == c.Preferred() {
		return Decision{}
	}
	start := r.Posted
	if start.IsZero() {
		start = r.FirstSeen
	}
	if start.IsZero() || start.After(now) { // not known, or a clock that runs ahead: it was seen just now
		start = now
	}
	until := start.Add(wait)
	if !now.Before(until) {
		return Decision{}
	}
	return Decision{Hold: true, Until: until}
}

// Note is the sentence shown beside a title whose release is being held back.
// zone is the server's time zone.
func Note(until, now time.Time, zone *time.Location) string {
	if zone == nil {
		zone = time.Local
	}
	u := until.In(zone)
	when := u.Format("15:04")
	if u.Format("2006-01-02") != now.In(zone).Format("2006-01-02") {
		when = u.Format("2 Jan 15:04")
	}
	return "Waiting for a better protocol until " + when
}

// ProtocolName is the protocol as a person says it.
func ProtocolName(p string) string {
	if p == Torrent {
		return "torrent"
	}
	return "Usenet"
}
