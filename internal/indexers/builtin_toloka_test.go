package indexers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rdborg/mediarium/internal/parser"
)

// tolokaRow is one result row laid out like Toloka's tracker.php: the forum
// in column 2, the topic in 3, the .torrent link in 6, size 7, completed 9,
// seeders 10, leechers 11 and the date in 13.
func tolokaRow(forum int, topic int, title, size string, seeders, leechers int, date, extra string) string {
	return fmt.Sprintf(`<tr class="prow1">
<td class="row1"><img src="images/icon_tick.gif"></td>
<td class="row1"><a class="gen" href="tracker.php?f=%d">Розділ</a></td>
<td class="row1"><a class="genmed" href="viewtopic.php?t=%d"><b>%s</b></a>%s</td>
<td class="row1"><a href="profile.php?u=7">автор</a></td>
<td class="row1">&nbsp;</td>
<td class="row1"><a class="genmed" href="download.php?id=%d">[DL]</a></td>
<td class="row1">%s</td>
<td class="row1">4</td>
<td class="row1">120</td>
<td class="row1"><b>%d</b></td>
<td class="row1"><b>%d</b></td>
<td class="row1">0 KB/s</td>
<td class="row1">%s</td>
</tr>`, forum, topic, title, extra, topic, size, seeders, leechers, date)
}

type tolokaSite struct {
	*httptest.Server
	logins atomic.Int32
	lastQS atomic.Value
}

func newTolokaSite(t *testing.T) *tolokaSite {
	t.Helper()
	s := &tolokaSite{}
	loggedIn := func(r *http.Request) bool {
		c, err := r.Cookie("toloka_sid")
		return err == nil && c.Value == "ok"
	}
	page := func(w http.ResponseWriter, body string, in bool) {
		nav := `<a href="login.php">Вхід</a>`
		if in {
			nav = `<a href="login.php?logout=true">Вихід</a>`
		}
		io.WriteString(w, `<html><body>`+nav+body+`</body></html>`)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login.php":
			_ = r.ParseForm()
			if r.Method != http.MethodPost || r.PostForm.Get("username") != "olena" || r.PostForm.Get("password") != "pass" || r.PostForm.Get("login") != "Вхід" {
				page(w, `<table class="forumline"><tr><td><table><tr><td><span class="gen">Ви ввели неправильне ім'я або пароль.</span></td></tr></table></td></tr></table>`, false)
				return
			}
			s.logins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "toloka_sid", Value: "ok", Path: "/"})
			page(w, "", true)
		case "/index.php":
			page(w, "", loggedIn(r))
		case "/tracker.php":
			if !loggedIn(r) {
				page(w, "", false)
				return
			}
			s.lastQS.Store(r.URL.RawQuery)
			rows := tolokaRow(96, 101, "Нічна гавань / Night Harbour (2021) BDRip 1080p Ukr/Eng | Sub Eng", "9.37 GB", 42, 3, "2024-03-15 14:30", `<img src="images/gold.gif">`) +
				tolokaRow(32, 102, "Тиха вулиця (Сезон 2, Серії 1-8 з 10) / Quiet Street (Season 2) (2023) WEB-DLRip 1080p Ukr/Eng", "14.2 GB", 17, 1, "2024-02-01 09:00", "") +
				tolokaRow(32, 103, "Тиха вулиця (Сезон 1) / Quiet Street (Season 1) (2022) WEB-DL 720p Ukr", "6.1 GB", 5, 0, "2023-11-20 20:15", `<img src="images/silver.gif">`)
			page(w, `<table class="forumline"><tbody><tr><th>head</th></tr>`+rows+`</tbody></table>`, true)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestTolokaIsBuiltIn(t *testing.T) {
	data, ok := builtinDefinitions()["toloka"]
	if !ok {
		t.Fatal("toloka is not shipped")
	}
	def, err := ParseDefinition(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Validate(); err != nil {
		t.Fatal(err)
	}
	if sum := summarize(def); !sum.Supported {
		t.Fatalf("not supported: %s", sum.Problem)
	}
}

func TestTolokaLoginSearchAndTitles(t *testing.T) {
	site := newTolokaSite(t)
	m := fixtureManager(t, "fixturepublic") // any download; toloka joins it as a built-in
	inst := Instance{ID: 9, Name: "Toloka", Kind: KindCardigann, DefinitionID: "toloka", BaseURL: site.URL + "/", Cardigann: m,
		Settings: map[string]string{"username": "olena", "password": "pass"}}

	results, err := m.Search(context.Background(), inst, "Quiet Street S02E03", []int{5000})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if site.logins.Load() != 1 {
		t.Fatalf("logins = %d", site.logins.Load())
	}
	qs, _ := url.ParseQuery(site.lastQS.Load().(string))
	if qs.Get("nm") != "Quiet Street" {
		t.Errorf("nm = %q, want the episode part left off", qs.Get("nm"))
	}
	if !strings.Contains(strings.Join(qs["f[]"], ","), "32") {
		t.Errorf("TV forums not asked for: %v", qs["f[]"])
	}
	if len(results) != 2 {
		t.Fatalf("got %d TV results: %+v", len(results), results)
	}
	movies, err := m.Search(context.Background(), inst, "Night Harbour", []int{2000})
	if err != nil {
		t.Fatalf("movie search: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("got %d movie results: %+v", len(movies), movies)
	}
	all := append(movies, results...)

	want := []struct {
		title      string
		season, ep int
		year       int
		res        string
		seeders    int
	}{
		{"Night Harbour", 0, 0, 2021, "1080p", 42},
		{"Quiet Street", 2, 1, 2023, "1080p", 17},
		{"Quiet Street", 1, 0, 2022, "720p", 5},
	}
	for i, w := range want {
		r := all[i]
		p := parser.Parse(r.Title)
		if !strings.EqualFold(p.Title, w.title) || p.Season != w.season || p.Episode != w.ep || p.Year != w.year || p.Resolution != w.res {
			t.Errorf("row %d: %q parsed as %+v", i, r.Title, p)
		}
		if strings.ContainsAny(r.Title, "абвгґдеєжзиіїйклмнопрстуфхцчшщьюяАБВГҐДЕЄЖЗИІЇЙКЛМНОПРСТУФХЦЧШЩЬЮЯ") {
			t.Errorf("row %d: Cyrillic left in %q", i, r.Title)
		}
		if r.Seeders != w.seeders {
			t.Errorf("row %d: seeders %d", i, r.Seeders)
		}
		if r.PublishDate.IsZero() {
			t.Errorf("row %d: no date", i)
		}
	}
	if p := parser.Parse(all[1].Title); len(p.Episodes) < 2 {
		t.Errorf("episode range lost: %q -> %+v", all[1].Title, p)
	}
	if all[0].SizeBytes < 9_000_000_000 {
		t.Errorf("size = %d", all[0].SizeBytes)
	}
	_, link, ok := DecodeLinkRef(all[0].DownloadURL)
	if !ok || link != site.URL+"/download.php?id=101" {
		t.Errorf("download = %q", link)
	}
}

func TestTolokaWrongPassword(t *testing.T) {
	site := newTolokaSite(t)
	m := fixtureManager(t, "fixturepublic")
	inst := Instance{ID: 10, Name: "Toloka", Kind: KindCardigann, DefinitionID: "toloka", BaseURL: site.URL + "/", Cardigann: m,
		Settings: map[string]string{"username": "olena", "password": "nope"}}
	_, err := m.Search(context.Background(), inst, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "неправильне") {
		t.Fatalf("err = %v, want the site's own message", err)
	}
}
