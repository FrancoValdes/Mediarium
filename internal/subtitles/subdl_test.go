package subtitles

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const srtBody = "1\n00:00:01,000 --> 00:00:02,000\nHello\n"

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

// The answer below follows the shape in SubDL's API documentation.
const subdlEpisodeAnswer = `{"status":true,"results":[{"tmdb_id":1399,"type":"tv","name":"Show"}],"subtitles":[
 {"release_name":"Show.S02E05.1080p.WEB.h264-GRP","name":"a.zip","url":"/subtitle/100-200.zip","season":2,"episode":5,"releases":["Show.S02E05.720p.HDTV.x264-OTHER"]},
 {"release_name":"Show.S02E06.1080p.WEB.h264-GRP","name":"b.zip","url":"/subtitle/101-201.zip","season":2,"episode":6},
 {"release_name":"Show Season 2","name":"c.zip","url":"/subtitle/102-202.zip","season":2,"episode":null,"full_season":true},
 {"release_name":"Show.S02E01-E10","name":"d.zip","url":"/subtitle/103-203.zip","season":"2","episode":"1","episode_from":1,"episode_end":10},
 {"release_name":"bad link","name":"e.zip","url":"https://elsewhere.example/x.zip","season":2,"episode":5}
]}`

func TestSubDLFind(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		got = append(got, q.Encode())
		switch {
		case q.Get("api_key") != "good-key":
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"status":false,"statusCode":403,"error":"not_authorized","message":"Not Authorized"}`))
		case q.Get("tmdb_id") == "1399":
			w.Write([]byte(subdlEpisodeAnswer))
		default:
			w.Write([]byte(`{"status":false,"error":"can't find movie or tv"}`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	c := NewSubDLWithBaseURL("good-key", srv.URL, srv.URL)
	res, err := c.Find(ctx, Query{ParentTMDBID: 1399, Season: 2, Episode: 5, Type: "episode", Language: "pt-BR"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Link != "/subtitle/100-200.zip" || res[0].Source != SourceSubDL || res[0].Language != "pt-BR" {
		t.Fatalf("results: %+v", res)
	}
	if q := got[0]; !strings.Contains(q, "languages=BR_PT") || !strings.Contains(q, "season_number=2") || !strings.Contains(q, "episode_number=5") || !strings.Contains(q, "type=tv") {
		t.Fatalf("query: %s", q)
	}
	// The other releases the subtitle fits count when scoring.
	if Score(res[0], "Show.S02E05.720p.HDTV.x264-OTHER.mkv") < 150 {
		t.Fatalf("score with a matching listed release: %v", Score(res[0], "Show.S02E05.720p.HDTV.x264-OTHER.mkv"))
	}

	// A film SubDL doesn't have: no results, no error.
	if res, err := c.Find(ctx, Query{TMDBID: 5, Language: "en"}); err != nil || len(res) != 0 {
		t.Fatalf("unknown film: %v %v", res, err)
	}
	if _, err := NewSubDLWithBaseURL("bad-key", srv.URL, srv.URL).Find(ctx, Query{TMDBID: 5}); !errors.Is(err, ErrSubDLKey) {
		t.Fatalf("bad key: %v", err)
	}
	if _, err := NewSubDLWithBaseURL("", srv.URL, srv.URL).Find(ctx, Query{TMDBID: 5}); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("no key: %v", err)
	}
	if _, err := NewSubDLWithBaseURL("bad-key", srv.URL, srv.URL).Test(ctx); !errors.Is(err, ErrSubDLKey) {
		t.Fatalf("test with a bad key: %v", err)
	}
}

func TestSubDLDownload(t *testing.T) {
	files := map[string][]byte{
		"/subtitle/1-1.zip": zipOf(t, map[string]string{"Film.2020.1080p.srt": srtBody, "readme.txt": "hi"}),
		"/subtitle/2-2.zip": zipOf(t, map[string]string{
			"Show.S02E04.srt": srtBody + "four", "Show.S02E05.srt": srtBody + "five", "Show.S02E06.srt": srtBody + "six, the longest one",
		}),
		"/subtitle/3-3.zip": zipOf(t, map[string]string{"Film.ass": "[Script Info]"}),
		"/subtitle/4/file9": []byte(srtBody),
		"/subtitle/5-5.zip": []byte("PK not really a zip"),
		"/subtitle/6/file1": []byte("<html>an error page</html>"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(b)
	}))
	defer srv.Close()
	c := NewSubDLWithBaseURL("k", srv.URL, srv.URL)
	ctx := context.Background()

	tests := []struct {
		name, link      string
		season, episode int
		want            string // "" = an error
	}{
		{"one srt in the zip", "/subtitle/1-1.zip", 0, 0, srtBody},
		{"the episode out of a pack", "/subtitle/2-2.zip", 2, 5, srtBody + "five"},
		{"a film from a pack: the biggest", "/subtitle/2-2.zip", 0, 0, srtBody + "six, the longest one"},
		{"no srt inside", "/subtitle/3-3.zip", 0, 0, ""},
		{"a single file", "/subtitle/4/file9", 0, 0, srtBody},
		{"broken zip", "/subtitle/5-5.zip", 0, 0, ""},
		{"not a subtitle", "/subtitle/6/file1", 0, 0, ""},
		{"missing", "/subtitle/7-7.zip", 0, 0, ""},
		{"not a SubDL link", "https://elsewhere.example/x.zip", 0, 0, ""},
		{"a path trying to leave", "/subtitle/../../etc/passwd", 0, 0, ""},
	}
	for _, tt := range tests {
		got, err := c.Download(ctx, tt.link, tt.season, tt.episode)
		if tt.want == "" {
			if err == nil {
				t.Errorf("%s: no error, got %q", tt.name, got)
			}
			continue
		}
		if err != nil || string(got) != tt.want {
			t.Errorf("%s: %q %v", tt.name, got, err)
		}
	}
}

func TestSubDLLanguageCodes(t *testing.T) {
	for in, want := range map[string]string{"en": "EN", "pt-BR": "BR_PT", "pt-PT": "PT", "zh-TW": "ZH", "fr": "FR", "sr": "SR"} {
		if got := subdlLanguage(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
