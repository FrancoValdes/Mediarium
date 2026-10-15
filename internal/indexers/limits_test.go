package indexers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// A definition asking to merge an absurd number of rows is refused when it
// loads, and the merge itself can't overflow if one gets through.
func TestRowsAfterIsBounded(t *testing.T) {
	def := &Definition{ID: "x", Links: []string{"https://www.example.org/"}, Search: SearchBlock{
		Paths: []SearchPath{{Path: "s"}},
		Rows:  RowsBlock{Selector: "tr", After: math.MaxInt},
	}}
	def.Search.Fields = OrderedMap[SelectorField]{Keys: []string{"title"}, Values: map[string]SelectorField{"title": {Selector: "td"}}}
	if p := strings.Join(def.Problems(), "; "); !strings.Contains(p, "rows after") {
		t.Fatalf("problems = %q, want the rows after refusal", p)
	}

	s := hardeningSession(t, "https://www.example.org/")
	s.def.Search = def.Search
	doc, err := html.Parse(strings.NewReader("<table><tr><td>a</td></tr><tr><td>b</td></tr><tr><td>c</td></tr></table>"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.parseHTMLRows(doc, newTmplVars(), nil, ""); err != nil {
		t.Fatalf("rows: %v", err)
	}
}

// A crash inside one indexer's search costs that indexer's results, not the
// whole program.
func TestSearchAllSurvivesAPanic(t *testing.T) {
	m := fixtureManager(t, "fixturepublic")
	m.Transport = panicTransport{}
	inst := Instance{ID: 9, Name: "Crashy", Kind: KindCardigann, DefinitionID: "fixturepublic", BaseURL: "https://public.fixture.test/", Enabled: true, Cardigann: m}
	out := SearchAll(context.Background(), []Instance{inst}, "x", nil)
	if len(out) != 1 || out[0].Err == nil || !strings.Contains(out[0].Err.Error(), "internal error") {
		t.Fatalf("outcome = %+v", out)
	}
	// the session's turn was given back: the next search isn't stuck behind it
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out = SearchAll(ctx, []Instance{inst}, "x", nil)
	if len(out) != 1 || out[0].Err == nil || !strings.Contains(out[0].Err.Error(), "internal error") {
		t.Fatalf("second outcome = %+v", out)
	}
}

type panicTransport struct{}

func (panicTransport) RoundTrip(*http.Request) (*http.Response, error) { panic("index out of range") }

// nested builds n rows, each inside the one before, each with width elements
// of its own. (The HTML parser stops at 512 levels, so a page wide as well as
// deep is what makes nested rows costly.)
func nested(n, width int) *html.Node {
	level := `<div class="r"><span>title</span><b>x</b>` + strings.Repeat("<i>text</i>", width)
	page := "<html><body>" + strings.Repeat(level, n) + strings.Repeat("</div>", n) + "</body></html>"
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		panic(err)
	}
	return doc
}

// Rows nested inside each other would copy the page once per row when rows
// are merged: the copying is paid for, and the page given up on.
func TestMergingNestedRowsIsBounded(t *testing.T) {
	s := hardeningSession(t, "https://www.example.org/")
	s.def.Search = SearchBlock{
		Rows:   RowsBlock{Selector: "div.r", After: 1},
		Fields: OrderedMap[SelectorField]{Keys: []string{"title"}, Values: map[string]SelectorField{"title": {Selector: "span"}}},
	}
	doc := nested(450, 200)
	start := time.Now()
	_, _, _, err := s.parseHTMLRows(doc, newTmplVars(), nil, "")
	if took := time.Since(start); took > 10*time.Second*slowdown {
		t.Fatalf("took %s", took)
	}
	if err == nil || !strings.Contains(err.Error(), "too complex") {
		t.Fatalf("err = %v, want the page refused as too complex", err)
	}
}

// Reading a field of nested rows (text or a copy to remove parts from) reads
// the rest of the page each time: that work is paid for too.
func TestFieldsOfNestedRowsAreBounded(t *testing.T) {
	for _, f := range []SelectorField{{}, {Remove: "b"}} {
		s := hardeningSession(t, "https://www.example.org/")
		s.def.Search = SearchBlock{
			Rows:   RowsBlock{Selector: "div.r"},
			Fields: OrderedMap[SelectorField]{Keys: []string{"title"}, Values: map[string]SelectorField{"title": f}},
		}
		doc := nested(450, 2000)
		start := time.Now()
		s.parseHTMLRows(doc, newTmplVars(), nil, "")
		if took := time.Since(start); took > 10*time.Second*slowdown {
			t.Fatalf("remove=%q took %s", f.Remove, took)
		}
	}
}

// Templates keep within bounds even when they build text or loop counts in
// variables, which the output limit and the literal check never see.
func TestTemplateVariablesCannotRunAway(t *testing.T) {
	vars := newTmplVars()
	vars.Categories = []string{"2000", "2010"}
	cases := []struct {
		name, src, wantErr, want string
	}{
		{"loop count in a variable", `{{ $n := 1000000000 }}{{ range $n }}{{ range $n }}{{ end }}{{ end }}`, "at most", ""},
		{"loops that are each small but many together", `{{ $n := 9000 }}{{ range $n }}{{ range $n }}{{ end }}{{ end }}`, "loops too many", ""},
		{"a small loop count in a variable", `{{ $n := 3 }}{{ range $i := $n }}{{ $i }}{{ end }}`, "", "012"},
		{"loop with an else", `{{ range .Categories }}{{ . }}{{ else }}none{{ end }}`, "", "20002010"},
		{"loop over a variable list", `{{ $c := .Categories }}{{ range $i, $e := $c }}{{ $i }}{{ $e }}{{ end }}`, "", "0200012010"},
		{"a huge printf width", `{{ $s := printf "%01000000000d" 1 }}`, "too much text", ""},
		{"a width taken from an argument", `{{ $s := printf "%0*d" 1000000000 1 }}`, "too much text", ""},
		{"text doubled in a variable", `{{ $s := printf "%0900000d" 1 }}{{ $s = printf "%s%s" $s $s }}`, "too much text", ""},
		{"text grown by escaping", `{{ $s := printf "%0900000d" 1 }}{{ $s = html $s $s }}`, "too much text", ""},
		{"text grown by print", `{{ $s := printf "%0900000d" 1 }}{{ $s = print $s $s }}`, "too much text", ""},
		{"an ordinary printf", `{{ printf "%s-%02d" "a" 7 }}`, "", "a-07"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			out, err := applyTemplate(tc.src, vars, nil)
			if time.Since(start) > 3*time.Second {
				t.Fatalf("took %v", time.Since(start))
			}
			if tc.wantErr == "" {
				if err != nil || out != tc.want {
					t.Fatalf("got %q, %v; want %q", out, err, tc.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want an error mentioning %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// A search stuck on a site doesn't hold the next one for ever: waiting for
// the session's turn ends with the search's own time.
func TestSessionTurnRespectsTheDeadline(t *testing.T) {
	s := hardeningSession(t, "https://www.example.org/")
	if err := s.lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.lock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want the deadline", err)
	}
	s.unlock()
	if err := s.lock(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// JSON rows with many items each are counted item by item.
func TestJSONMultipleRowsAreCappedByItems(t *testing.T) {
	s := hardeningSession(t, "https://www.example.org/")
	s.def.Search = SearchBlock{
		Rows: RowsBlock{Selector: "data", Attribute: "items", Multiple: true},
		Fields: OrderedMap[SelectorField]{Keys: []string{"title", "download"}, Values: map[string]SelectorField{
			"title": {Selector: "name"}, "download": {Selector: "link"},
		}},
	}
	item := `{"name":"Some.Release.1080p","link":"https://www.example.org/d/1"}`
	row := `{"items":[` + strings.TrimSuffix(strings.Repeat(item+",", 3000), ",") + `]}`
	body := `{"data":[` + row + "," + row + "," + row + `]}`
	got, _, _, err := s.parseJSONRows([]byte(body), newTmplVars(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxSearchRows {
		t.Fatalf("%d results, want %d", len(got), maxSearchRows)
	}
}

func TestCostlyRegularExpressions(t *testing.T) {
	long := strings.Repeat("a", maxRegexLength+1)
	if _, err := compileDotNetRegex(long); !errors.Is(err, errRegexTooBig) {
		t.Fatalf("a %d character pattern: %v", len(long), err)
	}
	// failures are remembered, not worked out again on every row
	bad := `(?=x)y`
	compileDotNetRegex(bad)
	regexCacheMu.Lock()
	c, ok := regexCache[bad]
	regexCacheMu.Unlock()
	if !ok || c.err == nil {
		t.Fatal("the failed pattern was not cached")
	}

	// a re_replace that is too costly is refused when the definition loads;
	// one Go merely can't express still loads (it leaves the text as it is)
	def := func(pattern string) *Definition {
		d := &Definition{ID: "x", Links: []string{"https://www.example.org/"}, Search: SearchBlock{
			Paths: []SearchPath{{Path: "s"}},
			Rows:  RowsBlock{Selector: "tr"},
		}}
		d.Search.Fields = OrderedMap[SelectorField]{Keys: []string{"title"}, Values: map[string]SelectorField{"title": {
			Selector: "td", Filters: []FilterBlock{{Name: "re_replace", Args: []any{pattern, "x"}}},
		}}}
		return d
	}
	if p := def(strings.Repeat(`\pL{1000}`, 12)).Problems(); len(p) == 0 {
		t.Error("a costly re_replace pattern was accepted")
	}
	if p := def(long).Problems(); len(p) == 0 {
		t.Error("a very long re_replace pattern was accepted")
	}
	if p := def(`(?<=a)b`).Problems(); len(p) != 0 {
		t.Errorf("a look-behind re_replace should still load: %v", p)
	}

	// one page can only run so much pattern work
	budget := &workBudget{left: 1000}
	text := strings.Repeat("a", 5000)
	_, err := applyFiltersWith(text, []FilterBlock{{Name: "regexp", Args: []any{`(a+)`}}}, nil, budget)
	if !errors.Is(err, errPageTooComplex) {
		t.Fatalf("got %v, want the page refused", err)
	}
}

// Links and redirects a page hands out can't point a site on the internet at
// this machine or the home network.
func TestPageAddressesCannotReachTheLocalNetwork(t *testing.T) {
	public := hardeningSession(t, "https://www.example.org/")
	local := hardeningSession(t, "http://192.168.1.10:9117/")
	cases := []struct {
		s    *cgSession
		link string
		ok   bool
	}{
		{public, "https://www.example.org/download/1", true},
		{public, "https://files.mirror.example.net/a.torrent", true},
		{public, "magnet:?xt=urn:btih:abc", true},
		{public, "http://127.0.0.1:8080/api", false},
		{public, "http://192.168.1.1/admin", false},
		{public, "http://[::1]/", false},
		{public, "http://169.254.169.254/latest/meta-data/", false},
		{public, "http://localhost:9696/", false},
		{public, "http://router/", false},
		{public, "http://nas.local/", false},
		{public, "file:///etc/passwd", false},
		{local, "http://192.168.1.10:9117/dl/1", true},
		{local, "http://192.168.1.20/x", true}, // a site on the home network links within it
	}
	for _, tc := range cases {
		err := tc.s.checkPageAddress(tc.link)
		if tc.ok != (err == nil) {
			t.Errorf("checkPageAddress(%q) from %s = %v", tc.link, tc.s.base, err)
		}
	}

	// a result row whose download link points inside is dropped
	rb := &releaseBuilder{}
	if _, err := public.applyField(rb, "download", "http://127.0.0.1/x", nil, nil); err == nil || rb.link != "" {
		t.Errorf("a row link to this machine was kept: %v %q", err, rb.link)
	}
}

// A redirect the site sends towards this machine is not followed.
func TestRedirectToTheLocalNetworkIsRefused(t *testing.T) {
	s := hardeningSession(t, "https://www.example.org/")
	s.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		h := http.Header{"Location": []string{"http://127.0.0.1:8080/secret"}}
		return &http.Response{StatusCode: http.StatusFound, Header: h, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	_, err := s.doOnce(context.Background(), cgRequest{url: "https://www.example.org/search"})
	if err == nil || !strings.Contains(err.Error(), "own network") {
		t.Fatalf("got %v, want the redirect refused", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A reply bigger than what is read is an error, not a page silently cut off.
func TestOversizedSiteReplyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.CopyN(w, zeroReader{}, maxResponseBytes+10)
	}))
	defer srv.Close()
	s := hardeningSession(t, srv.URL+"/")
	_, err := s.doOnce(context.Background(), cgRequest{url: srv.URL + "/"})
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("got %v, want the reply refused", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

// A feed of nothing but empty items stops being read at the item limit, and
// dates and sizes no release can have are dropped.
func TestNewznabFeedLimits(t *testing.T) {
	feed := "<rss><channel>" + strings.Repeat("<item/>", 200000) + "</channel></rss>"
	got, err := parseNewznabFeed([]byte(feed), "Idx", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxNewznabItems {
		t.Fatalf("%d items, want %d", len(got), maxNewznabItems)
	}

	feed = fmt.Sprintf(`<rss><channel>
<item><title>A</title><pubDate>Fri, 31 Dec 9999 23:59:59 +0000</pubDate><attr name="size" value="%d"/><attr name="seeders" value="%d"/></item>
<item><title>B</title><pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate><attr name="size" value="1048576"/></item>
</channel></rss>`, int64(math.MaxInt64), int64(math.MaxInt64))
	got, err = parseNewznabFeed([]byte(feed), "Idx", "")
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if !got[0].PublishDate.IsZero() || got[0].SizeBytes != 0 || got[0].Seeders != 0 {
		t.Errorf("absurd values kept: %+v", got[0])
	}
	if got[1].PublishDate.Year() != 2006 || got[1].SizeBytes != 1048576 {
		t.Errorf("normal values lost: %+v", got[1])
	}
}
