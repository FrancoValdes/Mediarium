package announce

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// A Template tells the engine how to read one tracker's announcements: one or
// more regular expressions with named groups, and how to build the download
// address from what they found. The idea (and the group names) follow the
// IRC section of autobrr's indexer definitions, so a definition from there can
// be adapted by copying its patterns.

// Template limits keep a pasted template from costing much.
const (
	MaxPatterns       = 8
	MaxPatternLen     = 1500
	MaxDownloadURLLen = 600
	multiLineWindow   = 20 * time.Second // the lines of one multi-line announcement arrive within this
)

// Template describes an announce format.
type Template struct {
	// Multi means one announcement is spread over several lines, matched by
	// Patterns in order. Otherwise every pattern is an alternative for a
	// single line and the first that matches is used.
	Multi    bool     `json:"multi"`
	Patterns []string `json:"patterns"`
	// SizeUnits is "binary" (1 GB = 1024 MB, what most trackers mean) or
	// "decimal" (1 GB = 1000 MB).
	SizeUnits string `json:"sizeUnits"`
	// DownloadURL builds the address of the .torrent file, for example
	// /download.php?id={{ .torrentId }}&passkey={{ .passkey }}. A group name
	// takes the value found in the announcement; any other name is one of the
	// tracker keys saved with the connection. Values are always escaped for
	// an address.
	DownloadURL string `json:"downloadUrl"`
}

var placeholderRe = regexp.MustCompile(`\{\{\s*\.([A-Za-z_][A-Za-z0-9_]*)(\s*\|\s*raw)?\s*\}\}`)

// Names with a meaning to the engine.
const (
	fieldRelease   = "releaseName"
	fieldSize      = "torrentSize" // also accepted as "size"
	fieldCategory  = "category"
	fieldUploader  = "uploader"
	fieldFreeleech = "freeleech"
	fieldTorrentID = "torrentId"
	fieldBaseURL   = "baseUrl"
)

// Compiled is a Template that has been checked and is ready to read lines.
type Compiled struct {
	T        Template
	res      []*regexp.Regexp
	groups   map[string]bool
	needKeys []string
}

// ErrTemplate is wrapped by every template problem; its message is written for
// the person editing the template.
var ErrTemplate = errors.New("template")

func templateErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrTemplate, fmt.Sprintf(format, a...))
}

// Compile checks a template and prepares it.
func Compile(t Template) (*Compiled, error) {
	if len(t.Patterns) == 0 {
		return nil, templateErr("add at least one pattern")
	}
	if len(t.Patterns) > MaxPatterns {
		return nil, templateErr("at most %d patterns", MaxPatterns)
	}
	if t.SizeUnits != "" && t.SizeUnits != "binary" && t.SizeUnits != "decimal" {
		return nil, templateErr("size units are \"binary\" or \"decimal\"")
	}
	c := &Compiled{T: t, groups: map[string]bool{}}
	for i, p := range t.Patterns {
		if strings.TrimSpace(p) == "" {
			return nil, templateErr("pattern %d is empty", i+1)
		}
		if len(p) > MaxPatternLen {
			return nil, templateErr("pattern %d is too long (at most %d characters)", i+1, MaxPatternLen)
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, templateErr("pattern %d is not a valid expression: %s", i+1, plainRegexpError(err))
		}
		c.res = append(c.res, re)
		for _, n := range re.SubexpNames() {
			if n != "" {
				c.groups[n] = true
			}
		}
	}
	if !c.groups[fieldRelease] {
		return nil, templateErr("a pattern has to capture the release name in a group called (?P<%s>...)", fieldRelease)
	}
	if !t.Multi {
		// Every pattern is a way to read a whole announcement on its own.
		for i, re := range c.res {
			has := false
			for _, n := range re.SubexpNames() {
				has = has || n == fieldRelease
			}
			if !has {
				return nil, templateErr("pattern %d has to capture the release name in a group called (?P<%s>...)", i+1, fieldRelease)
			}
		}
	}
	if err := c.checkDownloadURL(); err != nil {
		return nil, err
	}
	return c, nil
}

func plainRegexpError(err error) string {
	return strings.TrimPrefix(err.Error(), "error parsing regexp: ")
}

// checkDownloadURL validates the address template and works out which keys it
// needs.
func (c *Compiled) checkDownloadURL() error {
	d := strings.TrimSpace(c.T.DownloadURL)
	if d == "" {
		return templateErr("add the download address, for example /download.php?id={{ .torrentId }}&passkey={{ .passkey }}")
	}
	if len(d) > MaxDownloadURLLen {
		return templateErr("the download address is too long")
	}
	// Anything that looks like a placeholder but is not one is a typo.
	stripped := placeholderRe.ReplaceAllString(d, "")
	if strings.Contains(stripped, "{{") || strings.Contains(stripped, "}}") {
		return templateErr("a {{ ... }} in the download address is not written right. Use {{ .name }}")
	}
	if strings.ContainsAny(stripped, " \t\r\n") {
		return templateErr("the download address can't contain spaces")
	}
	low := strings.ToLower(d)
	switch {
	case strings.HasPrefix(d, "/") && !strings.HasPrefix(d, "//"):
	case strings.HasPrefix(low, "http://"), strings.HasPrefix(low, "https://"):
		// The host must be written out, not taken from the announcement.
		rest := d[strings.Index(d, "://")+3:]
		host := rest
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			host = rest[:i]
		}
		if host == "" || strings.Contains(host, "{{") || strings.Contains(host, "@") {
			return templateErr("the address of the tracker has to be written out in the download address, not taken from the announcement")
		}
	case strings.HasPrefix(low, "magnet:"):
	case strings.HasPrefix(d, "{{"):
		// A whole address taken from the announcement: allowed, but at run
		// time it must point at the tracker's own address.
	default:
		return templateErr("the download address starts with / (the tracker's address is added), with http:// or https://, or with magnet:")
	}
	need := map[string]bool{}
	for _, m := range placeholderRe.FindAllStringSubmatch(d, -1) {
		name := m[1]
		if c.groups[name] {
			continue
		}
		if !ValidKeyName(name) {
			return templateErr("%q can't be used as a name", name)
		}
		if m[2] != "" {
			return templateErr("only values from the announcement can be used raw, and %q is not one of them", name)
		}
		need[name] = true
	}
	for n := range need {
		c.needKeys = append(c.needKeys, n)
	}
	sort.Strings(c.needKeys)
	return nil
}

// NeedKeys lists the tracker keys (passkey, rsskey, ...) the download address
// uses that the announcement itself does not provide.
func (c *Compiled) NeedKeys() []string { return append([]string(nil), c.needKeys...) }

// Groups lists the names the patterns capture.
func (c *Compiled) Groups() []string {
	var out []string
	for n := range c.groups {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ValidKeyName reports whether s can name a tracker key.
func ValidKeyName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Fields are the values the patterns captured. Group names are kept as the
// template wrote them.
type Fields map[string]string

// Reader turns lines into Fields, remembering where a multi-line
// announcement has got to. One Reader belongs to one connection.
type Reader struct {
	c *Compiled

	mu      sync.Mutex
	partial map[string]*partialAnnounce // by channel|nick
}

type partialAnnounce struct {
	next   int
	fields Fields
	at     time.Time
}

// NewReader makes a reader for a compiled template.
func NewReader(c *Compiled) *Reader { return &Reader{c: c, partial: map[string]*partialAnnounce{}} }

// maxPartial bounds the number of half-read multi-line announcements.
const maxPartial = 64

// Feed offers one cleaned line. It returns the fields when the line finishes
// an announcement.
func (r *Reader) Feed(channel, nick, line string, now time.Time) (Fields, bool) {
	c := r.c
	if !c.T.Multi {
		for _, re := range c.res {
			if f, ok := match(re, line); ok {
				if f[fieldRelease] == "" {
					return nil, false
				}
				return f, true
			}
		}
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(channel) + "|" + strings.ToLower(nick)
	p := r.partial[key]
	if p != nil && now.Sub(p.at) > multiLineWindow {
		delete(r.partial, key)
		p = nil
	}
	if p != nil {
		if f, ok := match(c.res[p.next], line); ok {
			merge(p.fields, f)
			p.next++
			p.at = now
			if p.next == len(c.res) {
				delete(r.partial, key)
				if p.fields[fieldRelease] == "" {
					return nil, false
				}
				return p.fields, true
			}
			return nil, false
		}
		delete(r.partial, key) // out of step: start over with this line
	}
	if f, ok := match(c.res[0], line); ok {
		if len(c.res) == 1 {
			if f[fieldRelease] == "" {
				return nil, false
			}
			return f, true
		}
		if len(r.partial) >= maxPartial {
			for k, v := range r.partial { // drop the oldest
				if now.Sub(v.at) > multiLineWindow {
					delete(r.partial, k)
				}
			}
			if len(r.partial) >= maxPartial {
				return nil, false
			}
		}
		r.partial[key] = &partialAnnounce{next: 1, fields: f, at: now}
	}
	return nil, false
}

func match(re *regexp.Regexp, line string) (Fields, bool) {
	m := re.FindStringSubmatch(line)
	if m == nil {
		return nil, false
	}
	f := Fields{}
	for i, n := range re.SubexpNames() {
		if n != "" && i < len(m) {
			f[n] = strings.TrimSpace(m[i])
		}
	}
	return f, true
}

func merge(dst, src Fields) {
	for k, v := range src {
		if v != "" || dst[k] == "" {
			dst[k] = v
		}
	}
}

// Announce is one release read from an announcement.
type Announce struct {
	Release   string
	SizeBytes int64
	Category  string
	Uploader  string
	TorrentID string
	Freeleech bool
	Fields    Fields
}

// Build turns captured fields into an Announce.
func (c *Compiled) Build(f Fields) (Announce, error) {
	a := Announce{Fields: f}
	a.Release = cleanName(f[fieldRelease])
	if a.Release == "" {
		return a, errors.New("the announcement has no release name")
	}
	a.Category = truncate(f[fieldCategory], 80)
	a.Uploader = truncate(f[fieldUploader], 60)
	a.TorrentID = truncate(f[fieldTorrentID], 40)
	if v := strings.ToLower(strings.TrimSpace(f[fieldFreeleech])); v != "" && v != "0" && v != "false" && v != "no" && v != "none" {
		a.Freeleech = true
	}
	sz := f[fieldSize]
	if sz == "" {
		sz = f["size"]
	}
	if sz != "" {
		a.SizeBytes = ParseSize(sz, c.T.SizeUnits != "decimal")
	}
	return a, nil
}

func cleanName(s string) string {
	s = Clean(s)
	if utf8.RuneCountInString(s) > 300 {
		s = string([]rune(s)[:300])
	}
	return strings.TrimSpace(s)
}

var sizeRe = regexp.MustCompile(`(?i)^\s*([0-9][0-9.,]*)\s*([kmgtp]?i?b?)?\s*$`)

// ParseSize reads a size such as "7.63 GB", "37.61 GiB", "1,234.5 MB" or a
// plain number of bytes. binary says whether KB, MB and GB count in 1024s. It
// returns 0 when the text is not a size.
func ParseSize(s string, binary bool) int64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	num := m[1]
	// "1,234.5" has thousands separators; "7,63" is a decimal comma.
	if strings.Contains(num, ",") && strings.Contains(num, ".") {
		num = strings.ReplaceAll(num, ",", "")
	} else if strings.Count(num, ",") == 1 && len(num)-strings.Index(num, ",") <= 3 {
		num = strings.Replace(num, ",", ".", 1)
	} else {
		num = strings.ReplaceAll(num, ",", "")
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v < 0 {
		return 0
	}
	unit := strings.ToLower(m[2])
	base := 1000.0
	if binary || strings.Contains(unit, "i") {
		base = 1024
	}
	mult := 1.0
	switch strings.TrimSuffix(strings.TrimSuffix(unit, "b"), "i") {
	case "k":
		mult = base
	case "m":
		mult = base * base
	case "g":
		mult = base * base * base
	case "t":
		mult = base * base * base * base
	case "p":
		mult = base * base * base * base * base
	}
	out := v * mult
	if out > 1e16 {
		return 0
	}
	return int64(out)
}

// escape percent-encodes everything but the unreserved characters.
func escape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '.' || ch == '_' || ch == '~' {
			b.WriteByte(ch)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[ch>>4])
		b.WriteByte(hex[ch&15])
	}
	return b.String()
}

// URLOptions says what the download address is built from.
type URLOptions struct {
	// TrackerURL is the tracker's own address (https://tracker.example). A
	// download address that points anywhere else is refused, so a key is
	// never sent to a host the person did not name.
	TrackerURL string
	// Keys are the tracker keys by name. With Mask set their values are
	// replaced by the name in angle brackets (for showing the address).
	Keys map[string]string
	Mask bool
}

// DownloadURL builds the address of the release's file.
func (c *Compiled) DownloadURL(a Announce, o URLOptions) (string, error) {
	var missing string
	out := placeholderRe.ReplaceAllStringFunc(c.T.DownloadURL, func(ph string) string {
		m := placeholderRe.FindStringSubmatch(ph)
		name, raw := m[1], m[2] != ""
		if c.groups[name] {
			v := a.Fields[name]
			if name == fieldRelease {
				v = a.Release
			}
			if raw {
				return v
			}
			return escape(v)
		}
		if o.Mask {
			return "<" + name + ">"
		}
		v := o.Keys[name]
		if v == "" {
			missing = name
			return ""
		}
		return escape(v)
	})
	if missing != "" {
		return "", fmt.Errorf("the key %q isn't filled in", missing)
	}
	if strings.HasPrefix(strings.ToLower(out), "magnet:") {
		return out, nil
	}
	if strings.HasPrefix(out, "/") && !strings.HasPrefix(out, "//") {
		base, err := trackerBase(o.TrackerURL)
		if err != nil {
			return "", err
		}
		out = strings.TrimRight(base.String(), "/") + out
	}
	// Masked addresses contain < and >, which a URL parser would reject.
	check := out
	if o.Mask {
		check = strings.NewReplacer("<", "", ">", "").Replace(out)
	}
	u, err := url.Parse(check)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("the download address built from this announcement isn't a web address")
	}
	base, err := trackerBase(o.TrackerURL)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Host, base.Host) {
		return "", fmt.Errorf("the download address points to %s, not to the tracker (%s), so it is not used", u.Hostname(), base.Hostname())
	}
	return out, nil
}

func trackerBase(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, errors.New("add the tracker's web address (for example https://tracker.example) so the download address can be built")
	}
	return u, nil
}
