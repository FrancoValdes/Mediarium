package migrate

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/indexers"
)

func TestTitleFolderProblem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix paths")
	}
	roots := []string{"/movies", "/data/media/tv/", ""}
	cases := []struct {
		folder string
		want   string
	}{
		{"/movies/Heat (1995)", ""},
		{"/data/media/tv/Breaking Bad", ""},
		{"", ""},
		{"/movies", "too near the top"},
		{"/", "too near the top"},
		{"/data", "too near the top"},
		{"/data/media/tv", "whole library folder"},
		{"/data/media/tv/", "whole library folder"},
	}
	for _, tc := range cases {
		got := titleFolderProblem(tc.folder, roots)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("titleFolderProblem(%q) = %q, want %q", tc.folder, got, tc.want)
		}
	}
}

// A movie whose folder (as Radarr reports it) is a whole library folder is
// skipped: linking "its" file would pick any movie in the library.
func TestPlanSkipsLibraryFolderAsTitleFolder(t *testing.T) {
	h := newHarness(t)
	st, err := h.im.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.libraryRoots = []string{st.moviesRoot, st.tvRoot}
	d := &radarrData{Movies: []arrMovie{
		{Title: "Heat", Year: 1995, TMDBID: 949, HasFile: true, Path: filepath.Join(h.moviesRoot, "Heat (1995)")},
		{Title: "Odd", Year: 2001, TMDBID: 4242, HasFile: true, Path: h.moviesRoot},
	}}
	p := &plan{}
	tp := &TitlesPreview{}
	h.im.planMovies(p, tp, d, st, nil, nil, false)
	if len(p.movies) != 2 {
		t.Fatalf("planned %+v", p.movies)
	}
	if heat := p.movies[0]; heat.item.Action != ActionAdd || heat.folderBad != "" {
		t.Errorf("heat: %+v", heat.item)
	}
	if odd := p.movies[1]; odd.item.Action != ActionSkip || !strings.Contains(odd.item.Reason, "whole library folder") || odd.folderBad == "" {
		t.Errorf("a library folder as the movie's folder: %+v", odd.item)
	}
}

func TestIndexerProblem(t *testing.T) {
	ok := indexers.Instance{Name: "NZBgeek", BaseURL: "https://api.nzbgeek.info", APIKey: "abc123"}
	cases := []struct {
		name   string
		change func(*indexers.Instance)
		want   string
	}{
		{"fine", func(*indexers.Instance) {}, ""},
		{"no address", func(i *indexers.Instance) { i.BaseURL = "" }, "address"},
		{"not http", func(i *indexers.Instance) { i.BaseURL = "ftp://api.example.com" }, "address"},
		{"no scheme", func(i *indexers.Instance) { i.BaseURL = "api.example.com" }, "address"},
		{"bad port", func(i *indexers.Instance) { i.BaseURL = "https://api.example.com:99999" }, "address"},
		{"login in it", func(i *indexers.Instance) { i.BaseURL = "https://me:secret@api.example.com" }, "username or password"},
		{"newline", func(i *indexers.Instance) { i.BaseURL = "https://api.example.com/\nx" }, "address"},
		{"long name", func(i *indexers.Instance) { i.Name = strings.Repeat("n", 81) }, "name"},
		{"key with a space", func(i *indexers.Instance) { i.APIKey = "abc 123" }, "API key"},
		{"huge setting", func(i *indexers.Instance) { i.Settings = map[string]string{"cookie": strings.Repeat("c", 8001)} }, "setting"},
	}
	for _, tc := range cases {
		inst := ok
		tc.change(&inst)
		got := indexerProblem(inst)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q", tc.name, got)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("%s: the reason repeats the address: %q", tc.name, got)
		}
	}
}

func TestPlanServerChecksHostAndPort(t *testing.T) {
	cases := []struct {
		host   string
		port   int
		action string
	}{
		{"news.example.com", 563, ActionAdd},
		{"news.example.com", 0, ActionAdd}, // the default port
		{"news.example.com:563", 563, ActionSkip},
		{"https://news.example.com", 563, ActionSkip},
		{"news example", 563, ActionSkip},
		{"news.example.com", 70000, ActionSkip},
		{"news.example.com", -1, ActionSkip},
	}
	for _, tc := range cases {
		p, sp := &plan{}, &ServersPreview{}
		planServer(p, sp, "NZBGet", newsServer{Name: "x", Host: tc.host, Port: tc.port, SSL: true, Enabled: true}, map[serverKey]bool{})
		if got := sp.Items[0].Action; got != tc.action {
			t.Errorf("%s:%d: %s (%s)", tc.host, tc.port, got, sp.Items[0].Reason)
		}
	}
}
