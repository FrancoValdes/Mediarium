// Package usagestats builds the anonymous usage message that an install sends
// once a day, but only after the administrator pressed "Yes" (see
// docs/usage-stats.md). Everything that can be sent is in this file: a fixed set
// of fields, each one a number band, a yes/no or a name from a fixed list. No
// text typed by a person, no address, no title, no folder, no host name and no
// name of a site or provider ever goes through it.
//
// The receiving side (tools/stats-receiver) checks every field against the same
// lists, so anything else is thrown away.
package usagestats

import (
	"regexp"
	"sort"
	"time"
)

// Schema is the version of the message layout.
const Schema = 1

// URL is where the message goes. The code that receives it is in
// tools/stats-receiver and the numbers are public on https://mediarium.app/stats/.
const URL = "https://mediarium.app/stats/ping.php"

// Payload is the whole message. Nothing else is sent.
type Payload struct {
	Schema   int      `json:"schema"`
	ID       string   `json:"id"`       // a random number made on this install; "Reset my ID" makes a new one
	Version  string   `json:"version"`  // for example 3.0.0
	Arch     string   `json:"arch"`     // amd64, arm64 or other
	Docker   bool     `json:"docker"`   // runs in a container
	Cores    string   `json:"cores"`    // a band, such as 3-4
	Memory   string   `json:"memory"`   // a band, such as 4-8 GB
	Age      string   `json:"age"`      // how long this install has existed, as a band
	Library  string   `json:"library"`  // how many titles, as a band
	Accounts string   `json:"accounts"` // how many accounts, as a band
	Indexers string   `json:"indexers"` // how many indexers, as a band
	Media    []string `json:"media"`    // the kinds of media switched on
	Features []string `json:"features"` // which features are in use
	Servers  []string `json:"servers"`  // the kinds of media server connected
}

// The fixed lists. A name that is not in its list is dropped.
var (
	MediaKinds = []string{"movies", "tv", "music", "ebooks", "audiobooks"}
	Features   = []string{"usenet", "torrents", "vpn", "proxy", "debrid", "download-client", "irc", "import-lists", "requests", "sso", "passkeys", "kobo", "notifications", "subtitles"}
	Servers    = []string{"plex", "jellyfin", "emby", "audiobookshelf", "kavita", "silo", "webhook"}
	Arches     = []string{"amd64", "arm64", "other"}

	CoresBands    = []string{"1-2", "3-4", "5-8", "9-16", "17+"}
	MemoryBands   = []string{"under 2 GB", "2-4 GB", "4-8 GB", "8-16 GB", "16-32 GB", "over 32 GB"}
	AgeBands      = []string{"under 1 week", "1-4 weeks", "1-3 months", "3-12 months", "over 1 year"}
	LibraryBands  = []string{"empty", "1-100", "101-1000", "1001-5000", "5001-20000", "over 20000"}
	AccountsBands = []string{"1", "2-5", "6-20", "over 20"}
	IndexerBands  = []string{"0", "1-2", "3-5", "6-10", "over 10"}
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var versionPattern = regexp.MustCompile(`^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}(-[0-9A-Za-z.]{1,20})?$`)

// Facts are the plain numbers and names an install reads about itself.
type Facts struct {
	ID        string
	Version   string
	Arch      string
	Docker    bool
	Cores     int
	MemoryMB  uint64 // 0 when unknown
	Installed time.Time
	Titles    int
	Accounts  int
	Indexers  int
	Media     []string
	Features  []string
	Servers   []string
	Now       time.Time
}

// Build turns the facts into the message, keeping only what is allowed.
func Build(f Facts) Payload {
	now := f.Now
	if now.IsZero() {
		now = time.Now()
	}
	return Payload{
		Schema:   Schema,
		ID:       f.ID,
		Version:  cleanVersion(f.Version),
		Arch:     pick(f.Arch, Arches, "other"),
		Docker:   f.Docker,
		Cores:    coresBand(f.Cores),
		Memory:   memoryBand(f.MemoryMB),
		Age:      ageBand(f.Installed, now),
		Library:  libraryBand(f.Titles),
		Accounts: accountsBand(f.Accounts),
		Indexers: indexerBand(f.Indexers),
		Media:    keep(f.Media, MediaKinds),
		Features: keep(f.Features, Features),
		Servers:  keep(f.Servers, Servers),
	}
}

// Valid reports whether a message is exactly what Build can make. The
// receiver applies the same rules in PHP.
func Valid(p Payload) bool {
	return p.Schema == Schema && idPattern.MatchString(p.ID) && versionPattern.MatchString(p.Version) &&
		in(p.Arch, Arches) && in(p.Cores, CoresBands) && in(p.Memory, append([]string{"unknown"}, MemoryBands...)) &&
		in(p.Age, append([]string{"unknown"}, AgeBands...)) && in(p.Library, LibraryBands) && in(p.Accounts, AccountsBands) &&
		in(p.Indexers, IndexerBands) && subset(p.Media, MediaKinds) && subset(p.Features, Features) && subset(p.Servers, Servers)
}

// NewID makes the text form of a random 16-byte number from the given bytes.
func NewID(random []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, 32)
	for i := 0; i < 16 && i < len(random); i++ {
		out = append(out, hex[random[i]>>4], hex[random[i]&15])
	}
	return string(out)
}

// IsID reports whether s looks like an install ID.
func IsID(s string) bool { return idPattern.MatchString(s) }

func cleanVersion(v string) string {
	if versionPattern.MatchString(v) {
		return v
	}
	return "0.0.0"
}

func pick(v string, allowed []string, fallback string) string {
	if in(v, allowed) {
		return v
	}
	return fallback
}

func in(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func subset(list, allowed []string) bool {
	seen := map[string]bool{}
	for _, x := range list {
		if !in(x, allowed) || seen[x] {
			return false
		}
		seen[x] = true
	}
	return true
}

// keep returns the names that are in the allowed list, once each, in a fixed order.
func keep(names, allowed []string) []string {
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	out := []string{}
	for _, a := range allowed {
		if have[a] {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

func coresBand(n int) string {
	switch {
	case n <= 2:
		return "1-2"
	case n <= 4:
		return "3-4"
	case n <= 8:
		return "5-8"
	case n <= 16:
		return "9-16"
	}
	return "17+"
}

func memoryBand(mb uint64) string {
	switch {
	case mb == 0:
		return "unknown"
	case mb < 2048:
		return "under 2 GB"
	case mb < 4096:
		return "2-4 GB"
	case mb < 8192:
		return "4-8 GB"
	case mb < 16384:
		return "8-16 GB"
	case mb < 32768:
		return "16-32 GB"
	}
	return "over 32 GB"
}

func ageBand(installed, now time.Time) string {
	if installed.IsZero() {
		return "unknown"
	}
	d := now.Sub(installed)
	switch {
	case d < 7*24*time.Hour:
		return "under 1 week"
	case d < 28*24*time.Hour:
		return "1-4 weeks"
	case d < 90*24*time.Hour:
		return "1-3 months"
	case d < 365*24*time.Hour:
		return "3-12 months"
	}
	return "over 1 year"
}

func libraryBand(n int) string {
	switch {
	case n <= 0:
		return "empty"
	case n <= 100:
		return "1-100"
	case n <= 1000:
		return "101-1000"
	case n <= 5000:
		return "1001-5000"
	case n <= 20000:
		return "5001-20000"
	}
	return "over 20000"
}

func accountsBand(n int) string {
	switch {
	case n <= 1:
		return "1"
	case n <= 5:
		return "2-5"
	case n <= 20:
		return "6-20"
	}
	return "over 20"
}

func indexerBand(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n <= 2:
		return "1-2"
	case n <= 5:
		return "3-5"
	case n <= 10:
		return "6-10"
	}
	return "over 10"
}
