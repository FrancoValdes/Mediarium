package opds

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var day = time.Date(2026, 10, 9, 12, 30, 0, 0, time.UTC)

func navFeed() Feed {
	return Feed{
		ID: "urn:mediarium:opds:root", Title: "Mediarium books", Updated: day, Kind: Navigation,
		Links: []Link{
			{Rel: RelSelf, Href: "/opds/", Type: TypeNavigation},
			{Rel: RelStart, Href: "/opds/", Type: TypeNavigation},
			{Rel: RelSearch, Href: "/opds/opensearch.xml", Type: TypeSearch, Title: "Search"},
			{Rel: RelSearch, Href: "/opds/search?q={searchTerms}", Type: TypeAcquisition, Title: "Search"},
		},
		Entries: []Entry{
			{ID: "urn:mediarium:opds:new", Title: "Recently added", Updated: day, Content: "The newest books in the library.",
				Links: []Link{{Rel: RelNew, Href: "/opds/new", Type: TypeAcquisition}}},
			{ID: "urn:mediarium:opds:authors", Title: "Authors & friends", Updated: day, Content: "Books by author.",
				Links: []Link{{Rel: RelSubsection, Href: "/opds/authors", Type: TypeNavigation}}},
		},
	}
}

func acqFeed() Feed {
	return Feed{
		ID: "urn:mediarium:opds:books", Title: "All books", Updated: day, Kind: Acquisition,
		Links: []Link{
			{Rel: RelSelf, Href: "/opds/books?page=2", Type: TypeAcquisition},
			{Rel: RelStart, Href: "/opds/", Type: TypeNavigation},
			{Rel: RelUp, Href: "/opds/", Type: TypeNavigation},
			{Rel: RelFirst, Href: "/opds/books", Type: TypeAcquisition},
			{Rel: RelPrevious, Href: "/opds/books", Type: TypeAcquisition},
			{Rel: RelNext, Href: "/opds/books?page=3", Type: TypeAcquisition},
			{Rel: RelLast, Href: "/opds/books?page=3", Type: TypeAcquisition},
		},
		TotalResults: 120, ItemsPerPage: 50, StartIndex: 51, Page: 2,
		Entries: []Entry{
			{
				ID: "urn:mediarium:book:7", Title: `The Hobbit, or "There and Back Again"`, Updated: day,
				Authors: []string{"J.R.R. Tolkien"}, Summary: "A hobbit goes <out> & comes back.\x01", Issued: "1937",
				Series: "Middle-earth", SeriesPos: "1", Identifier: "urn:mediarium:book:7",
				Links: []Link{
					{Rel: RelAcquisition, Href: "/opds/books/7/download", Type: "application/epub+zip", Length: 123456},
					{Rel: RelImage, Href: "/opds/books/7/cover", Type: "image/jpeg"},
					{Rel: RelThumbnail, Href: "/opds/books/7/thumbnail", Type: "image/jpeg"},
				},
			},
			{
				ID: "urn:mediarium:book:8", Title: "No Author Book", Updated: day,
				Links: []Link{{Rel: RelAcquisition, Href: "/opds/books/8/download", Type: "application/pdf"}},
			},
		},
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run with -update to make it)", path, err)
	}
	// Git may turn line ends on a checkout; the content is what counts.
	if strings.ReplaceAll(string(want), "\r\n", "\n") != string(got) {
		t.Fatalf("%s differs from the golden file:\n%s", name, got)
	}
}

func TestAtomGolden(t *testing.T) {
	golden(t, "navigation.xml", navFeed().Atom())
	golden(t, "acquisition.xml", acqFeed().Atom())
	golden(t, "opensearch.xml", SearchDescription("Mediarium books", "Search the ebooks in Mediarium", "/opds/search?q={searchTerms}"))
}

func TestJSONGolden(t *testing.T) {
	nav := navFeed()
	for i := range nav.Entries {
		for j := range nav.Entries[i].Links {
			nav.Entries[i].Links[j].Type = TypeFeedJSON
		}
	}
	data, err := nav.JSON()
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "navigation.json", append(data, '\n'))
	acq := acqFeed()
	data, err = acq.JSON()
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "acquisition.json", append(data, '\n'))
}

// The output must be well-formed XML with the elements the OPDS 1.2 examples
// have, whatever odd characters a book title holds.
func TestAtomIsWellFormedAndShaped(t *testing.T) {
	for name, f := range map[string]Feed{"navigation": navFeed(), "acquisition": acqFeed()} {
		dec := xml.NewDecoder(strings.NewReader(string(f.Atom())))
		var feeds, entries, links int
		var rootNS string
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok {
				switch se.Name.Local {
				case "feed":
					feeds++
					rootNS = se.Name.Space
				case "entry":
					entries++
				case "link":
					links++
				}
			}
		}
		if feeds != 1 || rootNS != "http://www.w3.org/2005/Atom" || entries != len(f.Entries) || links == 0 {
			t.Errorf("%s: feeds=%d ns=%q entries=%d links=%d", name, feeds, rootNS, entries, links)
		}
		var v struct{}
		if err := xml.Unmarshal(f.Atom(), &v); err != nil {
			t.Errorf("%s is not well-formed: %v", name, err)
		}
	}
}

func TestAtomEscapesAndDropsControlCharacters(t *testing.T) {
	out := string(acqFeed().Atom())
	if strings.Contains(out, "\x01") {
		t.Error("a control character reached the XML")
	}
	if !strings.Contains(out, "&lt;out&gt; &amp; comes back.") {
		t.Errorf("text is not escaped: %s", out)
	}
	if !strings.Contains(out, "&#34;There and Back Again&#34;") {
		t.Errorf("quotes in the title are not escaped: %s", out)
	}
}

func TestJSONShape(t *testing.T) {
	data, err := acqFeed().JSON()
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Metadata struct {
			Title         string `json:"title"`
			NumberOfItems int    `json:"numberOfItems"`
			ItemsPerPage  int    `json:"itemsPerPage"`
			CurrentPage   int    `json:"currentPage"`
		} `json:"metadata"`
		Links []struct {
			Rel any `json:"rel"`
		} `json:"links"`
		Publications []struct {
			Metadata struct {
				Type   string   `json:"@type"`
				Author []string `json:"author"`
			} `json:"metadata"`
			Links []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"links"`
			Images []struct{ Href string } `json:"images"`
		} `json:"publications"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f.Metadata.NumberOfItems != 120 || f.Metadata.ItemsPerPage != 50 || f.Metadata.CurrentPage != 2 {
		t.Errorf("paging: %+v", f.Metadata)
	}
	if len(f.Publications) != 2 || f.Publications[0].Metadata.Type != "http://schema.org/Book" || len(f.Publications[0].Images) != 2 {
		t.Fatalf("publications: %+v", f.Publications)
	}
	if got := f.Publications[0].Links[0]; got.Rel != RelOpenAccess || got.Href != "/opds/books/7/download" {
		t.Errorf("download link: %+v", got)
	}
}
