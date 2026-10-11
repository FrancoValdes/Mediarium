package library_test

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/rdborg/mediarium/internal/library"
	"github.com/rdborg/mediarium/internal/store"
)

func TestMovieCountsLeaveCopiesOut(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := library.NewRepo(db)
	add := func(tmdb int, status library.Status) library.Movie {
		m, err := repo.Add(library.Movie{TMDBID: tmdb, Title: "Film", Status: status, Monitored: true})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	add(1, library.StatusDownloaded)
	add(2, library.StatusDownloaded)
	add(3, library.StatusDownloading)
	main := add(4, library.StatusMissing)
	add(5, library.StatusMissing)
	// A second copy of a film is not counted twice.
	if _, err := repo.Add(library.Movie{TMDBID: 4, Title: "Film", Monitored: true}); err == nil {
		t.Fatal("expected a second main row for the same film to be refused")
	}
	if _, err := db.Exec(`INSERT INTO quality_profiles (id, name, allowed_tiers, cutoff) VALUES (90, '4K', 'WEBDL-2160p', 'WEBDL-2160p')`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddCopy(main.ID, 90); err != nil {
		t.Fatalf("add a second copy: %v", err)
	}
	got, err := repo.MovieCounts()
	if err != nil {
		t.Fatal(err)
	}
	want := library.MovieCounts{Total: 5, Downloaded: 2, Downloading: 1, Missing: 2}
	if got != want {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
}

func TestSeriesCountsFollowTheShowTotals(t *testing.T) {
	repo := newRepo(t)
	main, err := repo.AddSeries(library.Series{TMDBID: 10, Title: "Show", Monitored: true}, []library.Episode{
		{Season: 0, Episode: 1, Title: "Special", AirDate: "2020-01-01"},
		{Season: 1, Episode: 1, Title: "One", AirDate: "2020-01-08"},
		{Season: 1, Episode: 2, Title: "Two", AirDate: "2020-01-15"},
	})
	if err != nil {
		t.Fatal(err)
	}
	eps, _ := repo.ListEpisodes(main.ID)
	for _, e := range eps {
		if e.Season == 1 && e.Episode == 1 {
			if err := repo.SetEpisodeStatus(e.ID, library.StatusDownloaded, "WEBDL-1080p", "/tv/one.mkv"); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := repo.SeriesCounts()
	if err != nil {
		t.Fatal(err)
	}
	// The special starts unmonitored and has no file: it is not counted, like in the show's own totals.
	if want := (library.SeriesCounts{Series: 1, Episodes: 2, EpisodesDownloaded: 1}); got != want {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
	shows, _ := repo.ListSeries()
	if len(shows) != 1 || shows[0].EpisodeCount != got.Episodes || shows[0].DownloadedCount != got.EpisodesDownloaded {
		t.Fatalf("the totals disagree with the show's own: %+v vs %+v", got, shows)
	}
}

func TestMissingFilesAndWantedEpisodes(t *testing.T) {
	repo := newRepo(t)
	// Movies: monitored+missing are listed, others are not.
	a, _ := repo.Add(library.Movie{TMDBID: 1, Title: "A", Monitored: true})
	b, _ := repo.Add(library.Movie{TMDBID: 2, Title: "B", Monitored: false})
	c, _ := repo.Add(library.Movie{TMDBID: 3, Title: "C", Monitored: true})
	if err := repo.SetStatus(c.ID, library.StatusDownloaded, "WEBDL-1080p", "/movies/c.mkv"); err != nil {
		t.Fatal(err)
	}
	_ = a
	_ = b
	files, err := repo.MissingMovieFiles()
	if err != nil || len(files) != 1 || files[0] != "" {
		t.Fatalf("missing movies = %v, %v", files, err)
	}

	sr, err := repo.AddSeries(library.Series{TMDBID: 20, Title: "Show", Monitored: true, Year: 2020}, []library.Episode{
		{Season: 1, Episode: 1, Title: "Aired", AirDate: "2020-01-01"},
		{Season: 1, Episode: 2, Title: "Later", AirDate: "2999-01-01"},
		{Season: 1, Episode: 3, Title: "Unknown date"},
		{Season: 1, Episode: 4, Title: "Have", AirDate: "2020-02-01"},
	})
	if err != nil {
		t.Fatal(err)
	}
	eps, _ := repo.ListEpisodes(sr.ID)
	for _, e := range eps {
		if e.Episode == 4 {
			_ = repo.SetEpisodeStatus(e.ID, library.StatusDownloaded, "WEBDL-720p", "/tv/4.mkv")
		}
	}
	paths, err := repo.MissingEpisodeFiles("2026-10-09")
	if err != nil || len(paths) != 1 {
		t.Fatalf("missing episodes = %v, %v (only the aired one counts)", paths, err)
	}
	missing, err := repo.ListWantedEpisodes(library.StatusMissing, "2026-10-09")
	if err != nil || len(missing) != 1 || missing[0].Title != "Aired" || missing[0].SeriesTitle != "Show" || missing[0].SeriesYear != 2020 {
		t.Fatalf("wanted missing = %+v, %v", missing, err)
	}
	have, err := repo.ListWantedEpisodes(library.StatusDownloaded, "2026-10-09")
	if err != nil || len(have) != 1 || have[0].Quality != "WEBDL-720p" {
		t.Fatalf("wanted downloaded = %+v, %v", have, err)
	}
}

func TestCalendarQueriesRespectTheWindow(t *testing.T) {
	repo := newRepo(t)
	repo.Add(library.Movie{TMDBID: 1, Title: "Old", Monitored: true, ReleaseDate: "2019-05-05"})
	repo.Add(library.Movie{TMDBID: 2, Title: "Soon", Monitored: true, ReleaseDate: "2026-11-05"})
	repo.Add(library.Movie{TMDBID: 3, Title: "No date", Monitored: true})
	repo.Add(library.Movie{TMDBID: 4, Title: "Unmonitored", Monitored: false, ReleaseDate: "2026-11-06"})
	if _, err := repo.AddSeries(library.Series{TMDBID: 30, Title: "Show", Monitored: true}, []library.Episode{
		{Season: 1, Episode: 1, AirDate: "2026-09-01"},
		{Season: 1, Episode: 2, AirDate: "2026-10-12"},
		{Season: 1, Episode: 3, AirDate: "2027-12-01"},
	}); err != nil {
		t.Fatal(err)
	}
	all, err := repo.CalendarMovies("")
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, m := range all {
		titles = append(titles, m.Title)
	}
	sort.Strings(titles)
	if !reflect.DeepEqual(titles, []string{"Old", "Soon"}) {
		t.Fatalf("calendar movies = %v", titles)
	}
	later, _ := repo.CalendarMovies("2026-10-09")
	if len(later) != 1 || later[0].Title != "Soon" {
		t.Fatalf("movies from today = %+v", later)
	}
	eps, err := repo.CalendarEpisodes("2026-09-09", "2027-02-06")
	if err != nil || len(eps) != 1 || eps[0].Episode != 2 || eps[0].SeriesName != "Show" {
		t.Fatalf("calendar episodes = %+v, %v", eps, err)
	}
}

func TestBriefsLookUpManyAtOnce(t *testing.T) {
	repo := newRepo(t)
	m1, _ := repo.Add(library.Movie{TMDBID: 1, Title: "One", Year: 2001, PosterPath: "/p1.jpg", Monitored: true})
	m2, _ := repo.Add(library.Movie{TMDBID: 2, Title: "Two", Year: 2002, Monitored: true})
	if err := repo.SetStatus(m1.ID, library.StatusDownloaded, "Bluray-1080p", "/m/1.mkv"); err != nil {
		t.Fatal(err)
	}
	sr, _ := repo.AddSeries(library.Series{TMDBID: 5, Title: "Show", Year: 2010, Monitored: true}, nil)
	got, err := repo.MovieBriefs([]int64{m1.ID, m2.ID, 999})
	if err != nil || len(got) != 2 {
		t.Fatalf("movie briefs = %+v, %v", got, err)
	}
	if b := got[m1.ID]; b.Title != "One" || b.Year != 2001 || b.PosterPath != "/p1.jpg" || b.Status != library.StatusDownloaded || b.Quality != "Bluray-1080p" || b.TMDBID != 1 {
		t.Fatalf("brief = %+v", b)
	}
	shows, err := repo.SeriesBriefs([]int64{sr.ID})
	if err != nil || shows[sr.ID].Title != "Show" || shows[sr.ID].Year != 2010 {
		t.Fatalf("series briefs = %+v, %v", shows, err)
	}
	if none, err := repo.MovieBriefs(nil); err != nil || len(none) != 0 {
		t.Fatalf("no ids = %+v, %v", none, err)
	}
	// More ids than fit in one statement still come back.
	ids := make([]int64, 0, 1000)
	for i := int64(1); i <= 1000; i++ {
		ids = append(ids, i)
	}
	if many, err := repo.MovieBriefs(ids); err != nil || len(many) != 2 {
		t.Fatalf("many ids = %d, %v", len(many), err)
	}
}
