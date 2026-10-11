package announce

import (
	"strings"
	"testing"
	"time"
)

func mustCompile(t *testing.T, tpl Template) *Compiled {
	t.Helper()
	c, err := Compile(tpl)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return c
}

func presetTemplate(t *testing.T, id string) Template {
	t.Helper()
	p, ok := PresetByID(id)
	if !ok {
		t.Fatalf("no preset %q", id)
	}
	return p.Template
}

// feed reads lines the way the engine does and returns the finished announcement.
func feed(t *testing.T, c *Compiled, lines ...string) (Announce, bool) {
	t.Helper()
	r := NewReader(c)
	now := time.Now()
	for _, l := range lines {
		if f, ok := r.Feed("#chan", "bot", Clean(l), now); ok {
			a, err := c.Build(f)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			return a, true
		}
		now = now.Add(100 * time.Millisecond)
	}
	return Announce{}, false
}

func TestPresetsReadTheirExamples(t *testing.T) {
	for _, p := range Presets() {
		t.Run(p.ID, func(t *testing.T) {
			c := mustCompile(t, p.Template)
			opts := URLOptions{TrackerURL: "https://tracker.example", Keys: map[string]string{}}
			for _, k := range p.Keys {
				opts.Keys[k.Name] = "KEY-" + k.Name
			}
			var listed []string
			for _, k := range p.Keys {
				listed = append(listed, k.Name)
			}
			if got, want := strings.Join(c.NeedKeys(), ","), strings.Join(listed, ","); got != want {
				t.Fatalf("needed keys %q, preset lists %q", got, want)
			}
			if p.Template.Multi {
				a, ok := feed(t, c, p.Examples...)
				if !ok {
					t.Fatal("the whole example was not read")
				}
				if a.Release == "" {
					t.Fatal("no release name")
				}
				return
			}
			for _, ex := range p.Examples {
				a, ok := feed(t, c, ex)
				if !ok {
					t.Fatalf("example not read: %s", ex)
				}
				u, err := c.DownloadURL(a, opts)
				if err != nil {
					t.Fatalf("url for %q: %v", ex, err)
				}
				if !strings.HasPrefix(u, "https://tracker.example/") {
					t.Fatalf("url = %q", u)
				}
			}
		})
	}
}

func TestSingleLineFormats(t *testing.T) {
	type want struct {
		release, category, uploader, torrentID string
		freeleech                              bool
		size                                   int64
		url                                    string
	}
	keys := map[string]string{"passkey": "PASS123", "rsskey": "RSS456"}
	cases := []struct {
		name   string
		preset string
		line   string
		want   want
	}{
		{
			"details link with size, freeleech", "one-line-details-link",
			"[Movies/XviD] The Movie 2010 DVDRip XviD-GROUP FREELEECH - https://tracker.example/details.php?id=000123 - 7.63 GB",
			want{"The Movie 2010 DVDRip XviD-GROUP", "Movies/XviD", "", "000123", true, scaled(7.63, 1<<30),
				"https://tracker.example/download.php/000123/The%20Movie%202010%20DVDRip%20XviD-GROUP.torrent?torrent_pass=PASS123"},
		},
		{
			"details link, not freeleech", "one-line-details-link",
			"[TV/Web-DL] The Show S02E05 720p WEB-DL-GRP - https://tracker.example/details.php?id=7 - 850 MB",
			want{"The Show S02E05 720p WEB-DL-GRP", "TV/Web-DL", "", "7", false, 850 * 1024 * 1024,
				"https://tracker.example/download.php/7/The%20Show%20S02E05%20720p%20WEB-DL-GRP.torrent?torrent_pass=PASS123"},
		},
		{
			"quoted name with uploader", "one-line-quoted-name",
			"New Torrent Announcement: <TV :: Episodes HD>  Name:'The Show S01E02 1080p WEB-DL' uploaded by 'anon' -  https://tracker.example/torrent/999",
			want{"The Show S01E02 1080p WEB-DL", "TV :: Episodes HD", "anon", "999", false, 0,
				"https://tracker.example/rss/download/999/RSS456/The%20Show%20S01E02%201080p%20WEB-DL.torrent"},
		},
		{
			"quoted name, freeleech", "one-line-quoted-name",
			"New Torrent Announcement: <Movies>  Name:'Name With (Brackets) & Ampersand' uploaded by 'u' freeleech - https://tracker.example/torrent/12",
			want{"Name With (Brackets) & Ampersand", "Movies", "u", "12", true, 0,
				"https://tracker.example/rss/download/12/RSS456/Name%20With%20%28Brackets%29%20%26%20Ampersand.torrent"},
		},
		{
			"new torrent with type and size", "one-line-new-torrent",
			"New Torrent: The Movie 2011 720p BluRay DD-EX 5.1 x264-GROUP - Type: Movie (H.264, Encode) Internal! - Uploaded by: someone - Size: 4.54 GiB - https://tracker.example/details.php?id=12346&hit=1",
			want{"The Movie 2011 720p BluRay DD-EX 5.1 x264-GROUP", "Movie", "someone", "12346", false, scaled(4.54, 1<<30),
				"https://tracker.example/download.php?id=12346&passkey=PASS123"},
		},
		{
			"new torrent whose name has a colon", "one-line-new-torrent",
			"New Torrent: Show Name: The Subtitle 2019 1080p Blu-ray AVC DTS-HD MA 1.0-GROUP - Type: Documentary (H.264, Blu-ray/HD DVD) Exclusive! - Uploaded by: anon - Size: 23.14 GiB - https://tracker.example/details.php?id=5&hit=1",
			want{"Show Name: The Subtitle 2019 1080p Blu-ray AVC DTS-HD MA 1.0-GROUP", "Documentary", "anon", "5", false, scaled(23.14, 1<<30),
				"https://tracker.example/download.php?id=5&passkey=PASS123"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := mustCompile(t, presetTemplate(t, tc.preset))
			a, ok := feed(t, c, tc.line)
			if !ok {
				t.Fatal("line not read")
			}
			w := tc.want
			if a.Release != w.release || a.Category != w.category || a.Uploader != w.uploader || a.TorrentID != w.torrentID || a.Freeleech != w.freeleech {
				t.Fatalf("got %+v", a)
			}
			if d := a.SizeBytes - w.size; d < -1 || d > 1 {
				t.Fatalf("size %d, want %d", a.SizeBytes, w.size)
			}
			u, err := c.DownloadURL(a, URLOptions{TrackerURL: "https://tracker.example", Keys: keys})
			if err != nil {
				t.Fatal(err)
			}
			if u != w.url {
				t.Fatalf("url\n got  %s\n want %s", u, w.url)
			}
		})
	}
}

func TestMultiLineAnnouncement(t *testing.T) {
	c := mustCompile(t, presetTemplate(t, "multi-line"))
	p, _ := PresetByID("multi-line")
	a, ok := feed(t, c, p.Examples...)
	if !ok {
		t.Fatal("not read")
	}
	if a.Release != "The Show S06E07 720p WEB-DL DD 5.1 H.264 - LP" || a.Category != "Episode" || a.Uploader != "someone" || a.TorrentID != "9116" {
		t.Fatalf("got %+v", a)
	}
	u, err := c.DownloadURL(a, URLOptions{TrackerURL: "https://tracker.example", Keys: map[string]string{"authkey": "AK", "torrent_pass": "TP"}})
	if err != nil {
		t.Fatal(err)
	}
	if u != "https://tracker.example/torrents.php?action=download&id=9116&authkey=AK&torrent_pass=TP" {
		t.Fatalf("url = %s", u)
	}
	// Needs the two keys, nothing else.
	if got := strings.Join(c.NeedKeys(), ","); got != "authkey,torrent_pass" {
		t.Fatalf("keys = %s", got)
	}
}

func TestMultiLineOutOfStepAndTimeout(t *testing.T) {
	c := mustCompile(t, presetTemplate(t, "multi-line"))
	p, _ := PresetByID("multi-line")
	ex := p.Examples

	// Lines of another announcement in the middle start things over; the
	// announcement that follows is still read whole.
	r := NewReader(c)
	now := time.Now()
	for _, l := range []string{ex[0], ex[1], "some chatter", ex[0], ex[1], ex[2]} {
		if _, ok := r.Feed("#c", "bot", l, now); ok {
			t.Fatal("finished too early")
		}
	}
	if _, ok := r.Feed("#c", "bot", ex[3], now); !ok {
		t.Fatal("a restart in the middle lost the announcement")
	}

	// Lines that arrive too late are not joined to the first ones.
	r = NewReader(c)
	r.Feed("#c", "bot", ex[0], now)
	r.Feed("#c", "bot", ex[1], now.Add(time.Second))
	r.Feed("#c", "bot", ex[2], now.Add(time.Second))
	if _, ok := r.Feed("#c", "bot", ex[3], now.Add(2*time.Minute)); ok {
		t.Fatal("joined lines that were minutes apart")
	}

	// Two announcers at once do not mix their lines.
	r = NewReader(c)
	for i, l := range ex[:3] {
		r.Feed("#c", "botA", l, now)
		if i == 0 {
			r.Feed("#c", "botB", "NOW BROADCASTING! [ Other.Show.S01E01.1080p ]", now)
		}
	}
	f, ok := r.Feed("#c", "botA", ex[3], now)
	if !ok || !strings.HasPrefix(f["releaseName"], "The Show") {
		t.Fatalf("mixed up: ok=%v %v", ok, f)
	}
}

func TestMultiLineStateIsBounded(t *testing.T) {
	c := mustCompile(t, presetTemplate(t, "multi-line"))
	r := NewReader(c)
	now := time.Now()
	for i := 0; i < 1000; i++ {
		r.Feed("#c", "bot"+strings.Repeat("x", i%300)+string(rune('a'+i%26)), "NOW BROADCASTING! [ Some.Show.S01E01 ]", now)
	}
	if len(r.partial) > maxPartial {
		t.Fatalf("%d half-read announcements kept", len(r.partial))
	}
}

func TestCompileRefusesBadTemplates(t *testing.T) {
	good := Template{Patterns: []string{`(?P<releaseName>.+)`}, DownloadURL: "/d/{{ .releaseName }}"}
	if _, err := Compile(good); err != nil {
		t.Fatalf("good template refused: %v", err)
	}
	tooMany := good
	tooMany.Patterns = make([]string, MaxPatterns+1)
	for i := range tooMany.Patterns {
		tooMany.Patterns[i] = `(?P<releaseName>.+)`
	}
	for name, tc := range map[string]struct {
		mut  func(*Template)
		want string
	}{
		"no patterns":       {func(t *Template) { t.Patterns = nil }, "at least one pattern"},
		"too many patterns": {func(t *Template) { *t = tooMany }, "at most"},
		"bad expression":    {func(t *Template) { t.Patterns = []string{`(?P<releaseName>.+`} }, "not a valid expression"},
		"no release group":  {func(t *Template) { t.Patterns = []string{`(?P<name>.+)`} }, "release name"},
		"long pattern": {func(t *Template) {
			t.Patterns = []string{`(?P<releaseName>` + strings.Repeat("a", MaxPatternLen) + `)`}
		}, "too long"},
		"no address":           {func(t *Template) { t.DownloadURL = "" }, "download address"},
		"host from the line":   {func(t *Template) { t.DownloadURL = "https://{{ .releaseName }}/x" }, "written out"},
		"user in host":         {func(t *Template) { t.DownloadURL = "https://a@b.example/x" }, "written out"},
		"odd start":            {func(t *Template) { t.DownloadURL = "ftp://x/y" }, "starts with"},
		"protocol-relative":    {func(t *Template) { t.DownloadURL = "//evil.example/x" }, "starts with"},
		"spaces":               {func(t *Template) { t.DownloadURL = "/a b" }, "spaces"},
		"broken placeholder":   {func(t *Template) { t.DownloadURL = "/a/{{ releaseName }}" }, "not written right"},
		"raw key":              {func(t *Template) { t.DownloadURL = "/a/{{ .passkey | raw }}" }, "raw"},
		"key name with symbol": {func(t *Template) { t.DownloadURL = "/a/{{ .pass-key }}" }, "not written right"},
		"bad units":            {func(t *Template) { t.SizeUnits = "weird" }, "binary"},
	} {
		t.Run(name, func(t *testing.T) {
			tpl := good
			tc.mut(&tpl)
			_, err := Compile(tpl)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	// Single-line alternatives each need the release name.
	if _, err := Compile(Template{Patterns: []string{`(?P<releaseName>a)`, `(?P<other>b)`}, DownloadURL: "/x"}); err == nil {
		t.Fatal("an alternative without a release name was accepted")
	}
}

func TestDownloadURLNeverLeavesTheTracker(t *testing.T) {
	keys := map[string]string{"passkey": "SECRETPASS"}
	opts := URLOptions{TrackerURL: "https://tracker.example", Keys: keys}

	// The address of the tracker comes from settings. A "baseUrl" in the
	// announcement is not used.
	c := mustCompile(t, Template{
		Patterns:    []string{`(?P<releaseName>\S+) (?P<baseUrl>\S+) (?P<torrentId>\d+)`},
		DownloadURL: "/dl/{{ .torrentId }}?pk={{ .passkey }}",
	})
	a, _ := feed(t, c, "Some.Release https://evil.example/ 5")
	u, err := c.DownloadURL(a, opts)
	if err != nil || !strings.HasPrefix(u, "https://tracker.example/dl/5?pk=SECRETPASS") {
		t.Fatalf("u=%q err=%v", u, err)
	}

	// A value cannot break out of its place in the address.
	c = mustCompile(t, Template{Patterns: []string{`(?P<releaseName>.+)`}, DownloadURL: "/dl?name={{ .releaseName }}&pk={{ .passkey }}"})
	a, _ = feed(t, c, "x&pk=STOLEN/../../etc?#@evil.example")
	u, err = c.DownloadURL(a, opts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(u, "pk=") != 1 || strings.Contains(u, "@") || strings.Contains(u, "#") || strings.Contains(u, "STOLEN/") {
		t.Fatalf("value escaped its place: %s", u)
	}

	// A full address taken from the announcement is only used when it is the tracker's own.
	c = mustCompile(t, Template{Patterns: []string{`(?P<releaseName>\S+) (?P<torrentUrl>\S+)`}, DownloadURL: "{{ .torrentUrl | raw }}"})
	a, _ = feed(t, c, "Rel https://tracker.example/get/1.torrent")
	if u, err := c.DownloadURL(a, opts); err != nil || u != "https://tracker.example/get/1.torrent" {
		t.Fatalf("u=%q err=%v", u, err)
	}
	for _, evil := range []string{"https://evil.example/get/1.torrent", "http://127.0.0.1:8080/x", "https://tracker.example.evil.example/x", "https://tracker.example@evil.example/x", "ftp://tracker.example/x", "javascript:alert(1)"} {
		a, _ = feed(t, c, "Rel "+evil)
		if u, err := c.DownloadURL(a, opts); err == nil {
			t.Errorf("%q was accepted as %q", evil, u)
		}
	}

	// A missing key is said, a missing tracker address too.
	c = mustCompile(t, Template{Patterns: []string{`(?P<releaseName>.+)`}, DownloadURL: "/dl?pk={{ .passkey }}"})
	a, _ = feed(t, c, "Rel")
	if _, err := c.DownloadURL(a, URLOptions{TrackerURL: "https://tracker.example"}); err == nil || !strings.Contains(err.Error(), "passkey") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.DownloadURL(a, URLOptions{Keys: keys}); err == nil || !strings.Contains(err.Error(), "tracker") {
		t.Fatalf("err = %v", err)
	}

	// Showing the address never shows a key.
	u, err = c.DownloadURL(a, URLOptions{TrackerURL: "https://tracker.example", Keys: keys, Mask: true})
	if err != nil || strings.Contains(u, "SECRETPASS") || !strings.Contains(u, "<passkey>") {
		t.Fatalf("masked = %q err=%v", u, err)
	}

	// An absolute template with the host written out must match the tracker.
	c = mustCompile(t, Template{Patterns: []string{`(?P<releaseName>.+)`}, DownloadURL: "https://cdn.other.example/x/{{ .passkey }}"})
	a, _ = feed(t, c, "Rel")
	if _, err := c.DownloadURL(a, opts); err == nil {
		t.Fatal("a download address on another host was accepted")
	}
}

func TestMagnetTemplate(t *testing.T) {
	c := mustCompile(t, Template{Patterns: []string{`(?P<releaseName>\S+) (?P<infoHash>[0-9a-f]{40})`}, DownloadURL: "magnet:?xt=urn:btih:{{ .infoHash }}&dn={{ .releaseName }}"})
	a, ok := feed(t, c, "Some.Release.2024 0123456789abcdef0123456789abcdef01234567")
	if !ok {
		t.Fatal("not read")
	}
	u, err := c.DownloadURL(a, URLOptions{})
	if err != nil || u != "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Some.Release.2024" {
		t.Fatalf("u=%q err=%v", u, err)
	}
}

func TestParseSize(t *testing.T) {
	for _, tc := range []struct {
		in     string
		binary bool
		want   int64
	}{
		{"7.63 GB", true, scaled(7.63, 1<<30)},
		{"7.63 GB", false, scaled(7.63, 1000000000)},
		{"37.61 GiB", false, scaled(37.61, 1<<30)},
		{"850MB", true, 850 * 1024 * 1024},
		{"1,234.5 MB", true, scaled(1234.5, 1<<20)},
		{"7,63 GB", true, scaled(7.63, 1<<30)},
		{"1.5 TB", true, scaled(1.5, 1<<40)},
		{"2048", true, 2048},
		{"700 KB", true, 700 * 1024},
		{"", true, 0},
		{"big", true, 0},
		{"-5 GB", true, 0},
		{"1e400 GB", true, 0},
		{"99999999999 PB", true, 0},
	} {
		got := ParseSize(tc.in, tc.binary)
		if d := got - tc.want; d < -1 || d > 1 {
			t.Errorf("ParseSize(%q, %v) = %d, want %d", tc.in, tc.binary, got, tc.want)
		}
	}
}

func TestClean(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain text", "plain text"},
		{"\x02bold\x02 and \x1funderline\x1f", "bold and underline"},
		{"\x0304red\x03 \x0304,12red on blue\x03 \x033green\x03", "red red on blue green"},
		{"\x03,5not a colour pair", ",5not a colour pair"},
		{"\x04FF0000hex red\x04 \x04ff0000,00ff00both\x0f", "hex red both"},
		{"tab\there", "tab here"},
		{"line\r\nbreak\x00nul\x07bell\x1b[31mesc", "linebreaknulbell[31mesc"},
		{"zero\u200bwidth\u202ertl\ufeffbom", "zerowidthrtlbom"},
		{"c1\u0085control", "c1control"},
		{"bad \xff\xfe bytes", "bad  bytes"},
		{"  padded  ", "padded"},
		{"ünïcödé 日本語 ✓", "ünïcödé 日本語 ✓"},
		{"\x01ACTION x\x01", "ACTION x"},
	} {
		if got := Clean(tc.in); got != tc.want {
			t.Errorf("Clean(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := Clean(strings.Repeat("é", 5000))
	if n := len([]rune(long)); n != MaxTextRunes {
		t.Errorf("long text kept %d characters", n)
	}
}

func TestFreeleechValues(t *testing.T) {
	c := mustCompile(t, Template{Patterns: []string{`(?P<releaseName>\S+) fl=(?P<freeleech>\S*)`}, DownloadURL: "/x"})
	for in, want := range map[string]bool{"fl=yes": true, "fl=FREELEECH": true, "fl=": false, "fl=0": false, "fl=no": false, "fl=false": false, "fl=1": true} {
		a, ok := feed(t, c, "Rel "+in)
		if !ok {
			t.Fatalf("%s not read", in)
		}
		if a.Freeleech != want {
			t.Errorf("%s: freeleech = %v", in, a.Freeleech)
		}
	}
}

// scaled is x times unit as a whole number of bytes (a constant expression
// with a fraction cannot be converted to int64 directly).
func scaled(x float64, unit int64) int64 { return int64(x * float64(unit)) }
