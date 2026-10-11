package metafiles

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/fileperms"
)

var (
	jpeg = append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("x"), 20)...)
	png  = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte("x"), 20)...)
)

func touch(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(items []Item, replace bool) []string {
	var out []string
	for _, it := range items {
		if it.Needed(replace) {
			out = append(out, it.Name)
		}
	}
	sort.Strings(out)
	return out
}

func TestMovieLayout(t *testing.T) {
	root := t.TempDir()
	alone := filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")
	touch(t, alone, "v")
	flat := filepath.Join(root, "Alien (1979).mkv")
	touch(t, flat, "v")
	shared := filepath.Join(root, "Double Bill", "First.mkv")
	touch(t, shared, "v")
	touch(t, filepath.Join(root, "Double Bill", "Second.mkv"), "v")
	withSample := filepath.Join(root, "Ronin (1998)", "Ronin (1998).mkv")
	touch(t, withSample, "v")
	touch(t, filepath.Join(root, "Ronin (1998)", "Ronin-sample.mkv"), "v")

	cases := []struct {
		name  string
		video string
		root  string
		ok    bool
		alone bool
	}{
		{"own folder", alone, root, true, true},
		{"straight in the library folder", flat, root, true, false},
		{"two videos in one folder", shared, root, true, false},
		{"a sample next to the movie doesn't count", withSample, root, true, true},
		{"outside the library", alone, filepath.Join(root, "elsewhere"), false, false},
		{"no root", alone, "", false, false},
	}
	for _, c := range cases {
		l, ok := MovieLayout(c.root, c.video)
		if ok != c.ok || (ok && l.Alone != c.alone) {
			t.Errorf("%s: MovieLayout = %+v, %v; want ok=%v alone=%v", c.name, l, ok, c.ok, c.alone)
		}
	}
}

func TestPlanMovieNamesAndExistingFiles(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")
	touch(t, video, "v")
	l, _ := MovieLayout(root, video)
	want := Want{NFO: true, Artwork: true}

	got := names(PlanMovie(l, video, want), false)
	if strings.Join(got, ",") != "clearlogo,fanart,movie.nfo,poster" {
		t.Fatalf("a movie in a folder of its own needs %v", got)
	}

	// The person's own files count, under any of the usual names and formats.
	touch(t, filepath.Join(root, "Heat (1995)", "folder.png"), "mine")
	touch(t, filepath.Join(root, "Heat (1995)", "Heat (1995).nfo"), "<movie/>")
	touch(t, filepath.Join(root, "Heat (1995)", "BACKDROP.JPG"), "mine")
	got = names(PlanMovie(l, video, want), false)
	if strings.Join(got, ",") != "clearlogo" {
		t.Fatalf("with their own poster, backdrop and NFO only the logo is left: %v", got)
	}

	// Replace only ever touches Mediarium's own names: nothing here is.
	if got := names(PlanMovie(l, video, want), true); strings.Join(got, ",") != "clearlogo" {
		t.Fatalf("replace must not reach the person's own files: %v", got)
	}

	// Off means not planned at all.
	if got := PlanMovie(l, video, Want{}); len(got) != 0 {
		t.Fatalf("nothing wanted, planned %v", got)
	}
	if got := names(PlanMovie(l, video, Want{NFO: true}), false); len(got) != 0 {
		t.Fatalf("only NFO wanted, yet %v", got)
	}

	// A movie sharing its folder gets names that carry its own name.
	flat := filepath.Join(root, "Alien (1979).mkv")
	touch(t, flat, "v")
	fl, _ := MovieLayout(root, flat)
	got = names(PlanMovie(fl, flat, want), false)
	if strings.Join(got, ",") != "Alien (1979)-clearlogo,Alien (1979)-fanart,Alien (1979)-poster,Alien (1979).nfo" {
		t.Fatalf("flat library names: %v", got)
	}
}

func TestPlanShow(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Breaking Bad")
	touch(t, filepath.Join(dir, "Season 01", "Breaking Bad - S01E01.mkv"), "v")
	touch(t, filepath.Join(dir, "Season 00", "Breaking Bad - S00E01.mkv"), "v")
	got := names(PlanShow(dir, []int{1, 0, 1}, Want{NFO: true, Artwork: true}), false)
	want := "clearlogo,fanart,poster,season-specials-poster,season01-poster,tvshow.nfo"
	if strings.Join(got, ",") != want {
		t.Fatalf("show files = %v, want %s", got, want)
	}

	// season01.jpg is the same poster as season01-poster.jpg.
	touch(t, filepath.Join(dir, "Season01.jpg"), "mine")
	touch(t, filepath.Join(dir, "TVSHOW.NFO"), "<tvshow/>")
	got = names(PlanShow(dir, []int{1}, Want{NFO: true, Artwork: true}), false)
	if strings.Join(got, ",") != "clearlogo,fanart,poster" {
		t.Fatalf("after the person's own files: %v", got)
	}
}

func TestShowDir(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name  string
		files []string
		want  string
		ok    bool
	}{
		{"season folders", []string{"Show/Season 01/a.mkv", "Show/Season 02/b.mkv"}, "Show", true},
		{"specials", []string{"Show/Specials/a.mkv", "Show/Season 1/b.mkv"}, "Show", true},
		{"short season folder", []string{"Show/S01/a.mkv", "Show/S02/b.mkv"}, "Show", true},
		{"no season folders", []string{"Show/a.mkv", "Show/b.mkv"}, "Show", true},
		{"most files decide", []string{"Show/Season 1/a.mkv", "Other/b.mkv", "Show/Season 1/c.mkv"}, "Show", true},
		{"straight in the library folder", []string{"a.mkv", "b.mkv"}, "", false},
		{"season folder straight in the library folder", []string{"Season 1/a.mkv"}, "", false},
	}
	for _, c := range cases {
		var files []string
		for _, f := range c.files {
			files = append(files, filepath.Join(root, filepath.FromSlash(f)))
		}
		got, ok := ShowDir(root, files)
		want := ""
		if c.want != "" {
			want = filepath.Join(root, c.want)
		}
		if got != want || ok != c.ok {
			t.Errorf("%s: ShowDir = %q, %v; want %q, %v", c.name, got, ok, want, c.ok)
		}
	}
	// A folder outside the library is never one.
	if _, ok := ShowDir(root, []string{filepath.Join(filepath.Dir(root), "Elsewhere", "a.mkv")}); ok {
		t.Error("a folder outside the library was accepted")
	}
}

func TestSeasonPosterNames(t *testing.T) {
	for season, want := range map[int]string{0: "season-specials-poster", 1: "season01-poster", 12: "season12-poster", 100: "season100-poster"} {
		if got := SeasonPosterName(season); got != want {
			t.Errorf("season %d = %q, want %q", season, got, want)
		}
	}
}

func TestWriteFileNeverOverwritesUnlessAsked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "movie.nfo")

	wrote, err := WriteFile(path, []byte("first"), false)
	if err != nil || !wrote {
		t.Fatalf("first write: %v %v", wrote, err)
	}
	wrote, err = WriteFile(path, []byte("second"), false)
	if err != nil || wrote {
		t.Fatalf("second write should leave the file: %v %v", wrote, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "first" {
		t.Fatalf("the file was overwritten: %q", b)
	}
	wrote, err = WriteFile(path, []byte("third"), true)
	if err != nil || !wrote {
		t.Fatalf("replace: %v %v", wrote, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "third" {
		t.Fatalf("replace didn't replace: %q", b)
	}
	// No staging file is left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftovers in the folder: %v", entries)
	}
}

func TestWriteFileReplacesALinkNotItsTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need rights on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.txt")
	touch(t, target, "precious")
	link := filepath.Join(dir, "movie.nfo")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if wrote, _ := WriteFile(link, []byte("x"), false); wrote {
		t.Fatal("wrote over a link without being asked")
	}
	if _, err := WriteFile(link, []byte("x"), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "precious" {
		t.Fatalf("the file behind the link changed: %q", b)
	}
}

func TestWriteFileGetsTheConfiguredMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	t.Cleanup(func() { fileperms.Set(fileperms.Modes{}) })
	fileperms.Set(fileperms.Modes{File: 0o640})
	path := filepath.Join(t.TempDir(), "poster.jpg")
	if _, err := WriteFile(path, jpeg, false); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
}

func TestSniffImage(t *testing.T) {
	if ext, ok := SniffImage(jpeg); !ok || ext != ".jpg" {
		t.Errorf("jpeg: %q %v", ext, ok)
	}
	if ext, ok := SniffImage(png); !ok || ext != ".png" {
		t.Errorf("png: %q %v", ext, ok)
	}
	for _, bad := range [][]byte{nil, []byte("<html>not found</html>"), []byte("GIF89a....."), {0xff, 0xd8}} {
		if _, ok := SniffImage(bad); ok {
			t.Errorf("%q was taken for a picture", bad)
		}
	}
}

// fakeSource gives fixed content and counts what was asked for.
type fakeSource struct {
	nfoCalls, imageCalls int
	image                []byte
	imageErr             error
}

func (f *fakeSource) NFO(_ context.Context, it Item) ([]byte, error) {
	f.nfoCalls++
	return []byte("<nfo for " + it.Name + "/>"), nil
}

func (f *fakeSource) Image(_ context.Context, it Item) ([]byte, error) {
	f.imageCalls++
	if it.Kind == KindLogo {
		return nil, nil // no logo for this one
	}
	return f.image, f.imageErr
}

func TestExecuteWritesOnlyWhatIsNeeded(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")
	touch(t, video, "v")
	touch(t, filepath.Join(root, "Heat (1995)", "poster.png"), "the person's poster")
	l, _ := MovieLayout(root, video)
	items := PlanMovie(l, video, Want{NFO: true, Artwork: true})

	src := &fakeSource{image: jpeg}
	res := Execute(context.Background(), items, src, false)
	if len(res.Failed) != 0 {
		t.Fatalf("failed: %v", res.Failed)
	}
	if res.Written[KindNFO] != 1 || res.Written[KindFanart] != 1 || res.Written[KindPoster] != 0 || res.Written[KindLogo] != 0 {
		t.Fatalf("written = %v", res.Written)
	}
	// The poster that was there is not asked for at all, and the logo was asked for but is not available.
	if src.imageCalls != 2 || src.nfoCalls != 1 {
		t.Fatalf("asked for %d pictures and %d NFOs", src.imageCalls, src.nfoCalls)
	}
	for _, f := range []string{"movie.nfo", "fanart.jpg"} {
		if _, err := os.Stat(filepath.Join(root, "Heat (1995)", f)); err != nil {
			t.Errorf("%s not written: %v", f, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "Heat (1995)", "poster.png")); string(b) != "the person's poster" {
		t.Errorf("the person's poster changed")
	}
	if _, err := os.Stat(filepath.Join(root, "Heat (1995)", "poster.jpg")); err == nil {
		t.Error("a second poster was written next to the person's")
	}

	// Run again: nothing more to do, nothing is asked for.
	src2 := &fakeSource{image: jpeg}
	res = Execute(context.Background(), PlanMovie(l, video, Want{NFO: true, Artwork: true}), src2, false)
	if src2.nfoCalls != 0 || src2.imageCalls != 1 { // only the logo, which still is not there
		t.Fatalf("second run asked for %d NFOs and %d pictures", src2.nfoCalls, src2.imageCalls)
	}
	if len(res.Written) != 0 {
		t.Fatalf("second run wrote %v", res.Written)
	}
}

func TestExecuteReplacesOnlyItsOwnFiles(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")
	touch(t, video, "v")
	touch(t, filepath.Join(root, "Heat (1995)", "movie.nfo"), "old")
	touch(t, filepath.Join(root, "Heat (1995)", "poster.jpg"), "old poster")
	l, _ := MovieLayout(root, video)

	src := &fakeSource{image: jpeg}
	res := Execute(context.Background(), PlanMovie(l, video, Want{NFO: true, Artwork: true}), src, true)
	if res.Written[KindNFO] != 1 || res.Written[KindPoster] != 1 {
		t.Fatalf("written = %v", res.Written)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "Heat (1995)", "movie.nfo")); string(b) != "<nfo for movie.nfo/>" {
		t.Errorf("movie.nfo = %q", b)
	}
}

func TestExecuteReportsAFailureAndCarriesOn(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")
	touch(t, video, "v")
	l, _ := MovieLayout(root, video)

	src := &fakeSource{imageErr: errors.New("TMDB is down")}
	res := Execute(context.Background(), PlanMovie(l, video, Want{NFO: true, Artwork: true}), src, false)
	if res.Written[KindNFO] != 1 {
		t.Errorf("the NFO should still be written: %v", res.Written)
	}
	if len(res.Failed) != 2 || !strings.Contains(res.Failed[0], "TMDB is down") {
		t.Errorf("failed = %v", res.Failed)
	}

	// Something that is not a picture is refused, not saved as one.
	src = &fakeSource{image: []byte("<html>Not found</html>")}
	res = Execute(context.Background(), PlanMovie(l, video, Want{Artwork: true}), src, false)
	if res.Written[KindPoster] != 0 || len(res.Failed) == 0 {
		t.Errorf("a page that isn't a picture was written: %v / %v", res.Written, res.Failed)
	}
	if _, err := os.Stat(filepath.Join(root, "Heat (1995)", "poster.jpg")); err == nil {
		t.Error("poster.jpg exists")
	}
}

func TestPNGLogoKeepsItsExtension(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")
	touch(t, video, "v")
	l, _ := MovieLayout(root, video)
	items := PlanMovie(l, video, Want{Artwork: true})
	src := pngSource{}
	res := Execute(context.Background(), items, src, false)
	if res.Written[KindLogo] != 1 {
		t.Fatalf("written = %v", res.Written)
	}
	if _, err := os.Stat(filepath.Join(root, "Heat (1995)", "clearlogo.png")); err != nil {
		t.Error(err)
	}
}

type pngSource struct{}

func (pngSource) NFO(context.Context, Item) ([]byte, error) { return nil, nil }
func (pngSource) Image(_ context.Context, it Item) ([]byte, error) {
	if it.Kind == KindLogo {
		return png, nil
	}
	return nil, nil
}

func TestCount(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")
	touch(t, video, "v")
	touch(t, filepath.Join(root, "Heat (1995)", "poster.jpg"), "x")
	l, _ := MovieLayout(root, video)
	items := PlanMovie(l, video, Want{NFO: true, Artwork: true})
	c := Count(items, false)
	if c[KindNFO] != 1 || c[KindPoster] != 0 || c[KindFanart] != 1 || c[KindLogo] != 1 {
		t.Errorf("counts = %v", c)
	}
	c = Count(items, true)
	if c[KindPoster] != 1 {
		t.Errorf("with replace the poster counts: %v", c)
	}
}

func TestPlanEpisode(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Show", "Season 01", "Show - S01E01.mkv")
	touch(t, video, "v")
	it := PlanEpisode(video)
	if it.Name != "Show - S01E01.nfo" || it.Dir != filepath.Dir(video) || it.Exists {
		t.Fatalf("episode item = %+v", it)
	}
	touch(t, filepath.Join(filepath.Dir(video), "show - s01e01.NFO"), "mine")
	if it := PlanEpisode(video); !it.Exists || it.Needed(false) {
		t.Fatalf("the person's own NFO isn't seen: %+v", it)
	}
}
