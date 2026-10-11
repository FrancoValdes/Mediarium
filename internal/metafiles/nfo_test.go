package metafiles

import (
	"bytes"
	"encoding/xml"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// golden compares got with testdata/name. Run with -update to rewrite them,
// then read the diff before committing.
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
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run the tests with -update to make it)", err)
	}
	// The files are kept with plain newlines whatever the checkout does to them.
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// wellFormed checks that every element in the text is closed properly. A file
// with several episodes holds several root elements, so the text is read as a
// stream.
func wellFormed(t *testing.T, name string, data []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Errorf("%s is not well-formed XML: %v", name, err)
			return
		}
	}
}

var heat = Movie{
	Title: "Heat", OriginalTitle: "Heat", Year: 1995, Premiered: "1995-12-15",
	Plot:    "A group of professional bank robbers start to feel the heat from police when they unknowingly leave a clue at their latest heist.",
	Tagline: "A Los Angeles crime saga", Runtime: 170, MPAA: "R", Rating: 7.9, Votes: 7234,
	TMDBID: 949, IMDBID: "tt0113277",
	Genres: []string{"Crime", "Drama", "Action"}, Countries: []string{"United States of America"},
	Studios: []string{"Regency Enterprises", "Warner Bros. Pictures"}, Directors: []string{"Michael Mann"}, Writers: []string{"Michael Mann"},
	Actors:     []Actor{{"Al Pacino", "Lt. Vincent Hanna", 0}, {"Robert De Niro", "Neil McCauley", 1}},
	TrailerKey: "2GfZl4kuVNI",
}

func TestMovieNFO(t *testing.T) {
	got := heat.NFO()
	wellFormed(t, "heat", got)
	golden(t, "heat.nfo", got)
}

func TestMovieNFOWithACollectionAndTextThatNeedsEscaping(t *testing.T) {
	m := Movie{
		Title: `Tom & Jerry: <The "Movie">`, OriginalTitle: "Tom & Jerry",
		Year: 2021, Plot: "Line one.\r\nLine two with a \x00 control character and a tab\there.",
		TMDBID: 587807, // no IMDb id known
		Set:    &Set{Name: "Tom and Jerry Collection", Overview: "Cat and mouse."},
		Genres: []string{"Family", "Family", " "}, // repeats and blanks are dropped
	}
	got := m.NFO()
	wellFormed(t, "escaping", got)
	golden(t, "tom-and-jerry.nfo", got)
	if bytes.Contains(got, []byte{0}) {
		t.Error("a control character reached the file")
	}
}

func TestMinimalMovieNFOLeavesEmptyThingsOut(t *testing.T) {
	got := string(Movie{Title: "Untitled", Year: 2024}.NFO())
	for _, bad := range []string{"<plot>", "<runtime>", "<ratings>", "<uniqueid", "<id>", "<tagline>", "<set>", "<originaltitle>"} {
		if strings.Contains(got, bad) {
			t.Errorf("an empty value produced %s:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "<title>Untitled</title>") || !strings.Contains(got, "<year>2024</year>") {
		t.Errorf("missing the basics:\n%s", got)
	}
}

func TestShowNFO(t *testing.T) {
	s := Show{
		Title: "Breaking Bad", OriginalTitle: "Breaking Bad", Year: 2008, Premiered: "2008-01-20",
		Plot: "A chemistry teacher diagnosed with cancer turns to making meth.", MPAA: "TV-MA", Status: "Ended",
		Rating: 8.9, Votes: 14000, TMDBID: 1396, TVDBID: 81189, IMDBID: "tt0903747",
		Genres: []string{"Drama", "Crime"}, Studios: []string{"AMC"},
		Actors: []Actor{{"Bryan Cranston", "Walter White", 0}},
	}
	got := s.NFO()
	wellFormed(t, "show", got)
	golden(t, "breaking-bad.nfo", got)

	// Without a TVDB id the TMDB id is the default one.
	got = Show{Title: "New Show", TMDBID: 5}.NFO()
	if !strings.Contains(string(got), `<uniqueid type="tmdb" default="true">5</uniqueid>`) || strings.Contains(string(got), "<id>") {
		t.Errorf("show without a TVDB id:\n%s", got)
	}
}

func TestEpisodeNFO(t *testing.T) {
	one := EpisodesNFO([]Episode{{ShowTitle: "Breaking Bad", Title: "Pilot", Season: 1, Episode: 1, Aired: "2008-01-20", Plot: "Walter gets bad news.", Runtime: 58}})
	wellFormed(t, "episode", one)
	golden(t, "episode-single.nfo", one)

	two := EpisodesNFO([]Episode{
		{ShowTitle: "Breaking Bad", Title: "Seven Thirty-Seven", Season: 2, Episode: 1, Aired: "2009-03-08", Plot: "Walt and Jesse."},
		{ShowTitle: "Breaking Bad", Title: "Grilled", Season: 2, Episode: 2, Aired: "2009-03-15"},
	})
	wellFormed(t, "episodes", two)
	golden(t, "episode-double.nfo", two)
	if n := strings.Count(string(two), "<episodedetails>"); n != 2 {
		t.Errorf("a file with two episodes has %d episodedetails", n)
	}
	if n := strings.Count(string(two), "<?xml"); n != 1 {
		t.Errorf("the XML declaration appears %d times, want once", n)
	}
}

func TestNFOIsTheSameEveryTime(t *testing.T) {
	a, b := heat.NFO(), heat.NFO()
	if !bytes.Equal(a, b) {
		t.Fatal("two renderings differ")
	}
}
