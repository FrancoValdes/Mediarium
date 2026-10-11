package subtitles

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAddonURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://addon.example/manifest.json", "https://addon.example"},
		{"stremio://addon.example/abc123/manifest.json", "https://addon.example/abc123"},
		{"  https://addon.example/key/manifest.json/ ", "https://addon.example/key"},
		{"addon.example/manifest.json", "https://addon.example"},
		{"http://192.168.1.5:7000/manifest.json", "http://192.168.1.5:7000"},
		{"https://addon.example/cfg/", "https://addon.example/cfg"},
	} {
		got, err := ParseAddonURL(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseAddonURL(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "ftp://x/manifest.json", "https://"} {
		if _, err := ParseAddonURL(bad); err == nil {
			t.Errorf("ParseAddonURL(%q) should fail", bad)
		}
	}
}

func fakeAddon(t *testing.T, srt string) (*httptest.Server, *[]string) {
	t.Helper()
	var asked []string
	mux := http.NewServeMux()
	mux.HandleFunc("/key/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","name":"Subs Example","version":"1.0.0","resources":[{"name":"subtitles","types":["movie","series"],"idPrefixes":["tt"]}],"types":["movie","series"]}`))
	})
	mux.HandleFunc("/nosubs/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"y","name":"Only Streams","resources":["stream"]}`))
	})
	mux.HandleFunc("/key/subtitles/", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		_, _ = w.Write([]byte(`{"subtitles":[
			{"id":"1","url":"` + "http://" + r.Host + `/file/a.srt","lang":"ron","title":"Film.2020.1080p.BluRay-GRP"},
			{"id":"2","url":"` + "http://" + r.Host + `/file/b.srt","lang":"eng"},
			{"id":"3","url":"ftp://nope/c.srt","lang":"ron"},
			{"id":"4","url":"` + "http://" + r.Host + `/file/d.srt","lang":"Romanian","release":"Film.2020.WEB-DL"}]}`))
	})
	mux.HandleFunc("/file/a.srt", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(srt)) })
	mux.HandleFunc("/file/vtt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("WEBVTT\n\nNOTE x\n\n00:00:01.000 --> 00:00:02.500\nBună <c>ziua</c>\n\n01:00:03.000 --> 01:00:04.000\nLa revedere\n"))
	})
	mux.HandleFunc("/file/zip", func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		f, _ := zw.Create("Show.S01E01.srt")
		_, _ = f.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nOne\n"))
		f, _ = zw.Create("Show.S01E02.srt")
		_, _ = f.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nTwo\n"))
		_ = zw.Close()
		_, _ = w.Write(buf.Bytes())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &asked
}

func TestAddonManifest(t *testing.T) {
	srv, _ := fakeAddon(t, "")
	c := NewAddonClient()
	m, err := c.Manifest(context.Background(), srv.URL+"/key")
	if err != nil || m.Name != "Subs Example" {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	if _, err := c.Manifest(context.Background(), srv.URL+"/nosubs"); err == nil || !strings.Contains(err.Error(), "doesn't provide subtitles") {
		t.Fatalf("an add-on without subtitles should be refused, got %v", err)
	}
	if _, err := c.Manifest(context.Background(), srv.URL+"/missing"); err == nil {
		t.Fatal("a missing manifest should fail")
	}
}

func TestAddonFindAsksWithTheImdbIdAndKeepsTheWantedLanguage(t *testing.T) {
	srv, asked := fakeAddon(t, "")
	c := NewAddonClient()
	got, err := c.Find(context.Background(), srv.URL+"/key", AddonQuery{IMDBID: "tt0133093", Language: "ro", Filename: "Film.2020.mkv", VideoHash: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 1 || !strings.HasPrefix((*asked)[0], "/key/subtitles/movie/tt0133093/") || !strings.HasSuffix((*asked)[0], ".json") {
		t.Fatalf("asked %v", *asked)
	}
	if !strings.Contains((*asked)[0], "videoHash=abc") || !strings.Contains((*asked)[0], "filename=Film.2020.mkv") {
		t.Errorf("the file name and hash should go along: %v", *asked)
	}
	if len(got) != 2 {
		t.Fatalf("want the 2 Romanian results (the English one and the ftp link dropped), got %+v", got)
	}
	if got[0].Release != "Film.2020.1080p.BluRay-GRP" || got[0].Language != "ro" || got[1].Release != "Film.2020.WEB-DL" {
		t.Errorf("results = %+v", got)
	}

	if _, err := c.Find(context.Background(), srv.URL+"/key", AddonQuery{IMDBID: "tt0944947", Series: true, Season: 1, Episode: 2, Language: "en"}); err != nil {
		t.Fatal(err)
	}
	if last := (*asked)[len(*asked)-1]; !strings.HasPrefix(last, "/key/subtitles/series/tt0944947:1:2") {
		t.Errorf("an episode is asked as imdb:season:episode, got %q", last)
	}
	if got, _ := c.Find(context.Background(), srv.URL+"/key", AddonQuery{IMDBID: "", Language: "ro"}); got != nil {
		t.Error("with no IMDb id there is nothing to ask")
	}
}

func TestAddonDownload(t *testing.T) {
	// A Romanian file saved in Windows-1250: "ă" is 0xE3, "ţ" is 0xFE.
	srtBytes := []byte("1\n00:00:01,000 --> 00:00:02,000\nBun\xe3 diminea\xfe\xe3\n")
	srv, _ := fakeAddon(t, string(srtBytes))
	c := NewAddonClient()
	ctx := context.Background()

	data, err := c.Download(ctx, srv.URL+"/file/a.srt", "ro", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Bună dimineaţă") {
		t.Errorf("the Windows-1250 text should come out as UTF-8, got %q", data)
	}

	vtt, err := c.Download(ctx, srv.URL+"/file/vtt", "ro", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "1\n00:00:01,000 --> 00:00:02,500\nBună ziua\n\n2\n01:00:03,000 --> 01:00:04,000\nLa revedere\n\n"
	if string(vtt) != want {
		t.Errorf("vtt converted to %q, want %q", vtt, want)
	}

	zipped, err := c.Download(ctx, srv.URL+"/file/zip", "en", 1, 2)
	if err != nil || !strings.Contains(string(zipped), "Two") {
		t.Errorf("the episode's file should be taken from the zip, got %q, %v", zipped, err)
	}
	if _, err := c.Download(ctx, "ftp://x/y.srt", "en", 0, 0); err == nil {
		t.Error("only web addresses can be downloaded")
	}
}

func TestToUTF8LeavesUTF8AloneAndDropsTheBOM(t *testing.T) {
	if got := string(ToUTF8([]byte("Bună"), "ro")); got != "Bună" {
		t.Errorf("utf-8 changed: %q", got)
	}
	if got := string(ToUTF8([]byte("\xef\xbb\xbfBună"), "ro")); got != "Bună" {
		t.Errorf("BOM kept: %q", got)
	}
	if got := string(ToUTF8([]byte("\xcf\xe5\xf0\xe2\xfb\xe9"), "ru")); got != "Первый" {
		t.Errorf("windows-1251: %q", got)
	}
}
