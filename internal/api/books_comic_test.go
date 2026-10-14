package api_test

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func comicPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 60))
	for x := 0; x < 40; x++ {
		for y := 0; y < 60; y++ {
			img.Set(x, y, color.RGBA{shade, 120, 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A books folder in the usual comic layout imports every issue, and the same
// issue number of two runs of a series stays two books.
func TestComicImportFolderOfIssues(t *testing.T) {
	server, base, client := loginNewServer(t)
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/modules", map[string]any{"ebooks": true}, http.StatusOK)
	root := filepath.Join(t.TempDir(), "ebooks")
	for _, rel := range []string{
		"Batman/Batman (2011)/Batman 001.cbz", "Batman/Batman (2011)/Batman 002.cbz",
		"Batman/Batman (2016)/Batman 001.cbz", "Batman/Batman (2016)/Batman 002.cbz", "Batman/Batman (2016)/Batman 003.cbz",
	} {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(file)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		w, _ := zw.Create("page1.png")
		_, _ = w.Write(comicPNG(t, 10))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/settings", map[string]any{"ebooksPath": root}, http.StatusOK)
	postJSON[map[string]any](t, client, base+"/api/books/import", map[string]any{}, http.StatusAccepted)
	waitBackground(t, server)
	st := getJSON[map[string]any](t, client, base+"/api/books/import")
	if st["phase"] != "done" || st["summary"].(map[string]any)["imported"] != float64(5) {
		t.Fatalf("import: %v", st)
	}
	list := getJSON[[]map[string]any](t, client, base+"/api/books")
	if len(list) != 5 {
		t.Fatalf("books: %v", list)
	}
	for _, b := range list {
		if b["seriesName"] != "Batman" || b["seriesPosition"] == "" {
			t.Errorf("issue: %v", b)
		}
	}
}

// A comic file goes into the library as it is, opens page by page in the
// reader, has its first page as its cover and remembers where you are.
func TestComicImportReadAndCover(t *testing.T) {
	server, base, client := loginNewServer(t)
	_ = server
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/modules", map[string]any{"ebooks": true}, http.StatusOK)
	root := filepath.Join(t.TempDir(), "ebooks")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(root, "Night Harbour 03.cbz"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range map[string][]byte{
		"ComicInfo.xml": []byte(`<ComicInfo><Title>Origins</Title><Series>Night Harbour</Series><Number>3</Number><Writer>Ana Gray</Writer></ComicInfo>`),
		"page10.png":    comicPNG(t, 30),
		"page2.png":     comicPNG(t, 20),
		"page1.png":     comicPNG(t, 10),
	} {
		w, _ := zw.Create(name)
		_, _ = w.Write(body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/settings", map[string]any{"ebooksPath": root}, http.StatusOK)

	postJSON[map[string]any](t, client, base+"/api/books/import", map[string]any{}, http.StatusAccepted)
	waitBackground(t, server)
	st := getJSON[map[string]any](t, client, base+"/api/books/import")
	if st["phase"] != "done" || st["summary"].(map[string]any)["imported"] != float64(1) {
		t.Fatalf("import: %v", st)
	}
	list := getJSON[[]map[string]any](t, client, base+"/api/books")
	if len(list) != 1 {
		t.Fatalf("books: %v", list)
	}
	b := list[0]
	id := int64(b["id"].(float64))
	if b["title"] != "Night Harbour #3: Origins" || b["author"] != "Ana Gray" || b["seriesName"] != "Night Harbour" || b["seriesPosition"] != "3" {
		t.Fatalf("the comic's own words: %v", b)
	}
	if b["coverUrl"] != "/api/books/"+itoa(id)+"/file-cover" {
		t.Fatalf("the cover is the first page: %v", b["coverUrl"])
	}

	info := getJSON[map[string]any](t, client, base+"/api/books/"+itoa(id)+"/comic")
	if info["pages"] != float64(3) || info["format"] != "cbz" {
		t.Fatalf("comic info: %v", info)
	}
	get := func(path string) (*http.Response, []byte) {
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, body
	}
	resp, body := get("/api/books/" + itoa(id) + "/comic/1")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || !bytes.Equal(body, comicPNG(t, 10)) {
		t.Fatalf("page 1 is page1.png: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if _, body = get("/api/books/" + itoa(id) + "/comic/3"); !bytes.Equal(body, comicPNG(t, 30)) {
		t.Fatal("page 3 is page10.png: natural order")
	}
	if resp, _ = get("/api/books/" + itoa(id) + "/comic/4"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no page 4: %d", resp.StatusCode)
	}
	if resp, _ = get("/api/books/" + itoa(id) + "/comic/0"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pages count from 1: %d", resp.StatusCode)
	}
	resp, body = get("/api/books/" + itoa(id) + "/file-cover")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" || len(body) == 0 {
		t.Fatalf("cover: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	// The page list is kept between requests, and still right the second time.
	if _, body = get("/api/books/" + itoa(id) + "/comic/2"); !bytes.Equal(body, comicPNG(t, 20)) {
		t.Fatal("page 2 is page2.png")
	}

	// Where you are is kept.
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/books/"+itoa(id)+"/progress", map[string]any{"format": "ebook", "position": "page:2", "percent": 66.7}, http.StatusOK)
	got := getJSON[map[string]any](t, client, base+"/api/books/"+itoa(id)+"/progress?format=ebook")
	if got["position"] != "page:2" {
		t.Fatalf("progress: %v", got)
	}
}

// A comic is opened by what it is, not what it is called: a .cbr that is
// really a ZIP opens, and a .cb7 is unpacked with 7z (when it is installed).
func TestComicOpensByItsContent(t *testing.T) {
	server, base, client := loginNewServer(t)
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/modules", map[string]any{"ebooks": true}, http.StatusOK)
	root := filepath.Join(t.TempDir(), "ebooks")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, shade := range map[string]uint8{"p2.png": 20, "p1.png": 10} {
		w, _ := zw.Create(name)
		_, _ = w.Write(comicPNG(t, shade))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Harbour Zip 04.cbr"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"Harbour Zip 04": 2}
	if sevenZip, err := exec.LookPath("7z"); err == nil {
		src := t.TempDir()
		for name, shade := range map[string]uint8{"p1.png": 40, "p2.png": 50, "p3.png": 60} {
			if err := os.WriteFile(filepath.Join(src, name), comicPNG(t, shade), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command(sevenZip, "a", "-t7z", filepath.Join(root, "Harbour Seven 05.cb7"), "p1.png", "p2.png", "p3.png")
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build the .cb7: %v: %s", err, out)
		}
		want["Harbour Seven 05"] = 3
	}
	postJSONMethod[map[string]any](t, client, http.MethodPut, base+"/api/settings", map[string]any{"ebooksPath": root}, http.StatusOK)
	postJSON[map[string]any](t, client, base+"/api/books/import", map[string]any{}, http.StatusAccepted)
	waitBackground(t, server)

	list := getJSON[[]map[string]any](t, client, base+"/api/books")
	if len(list) != len(want) {
		t.Fatalf("books: %v", list)
	}
	for _, b := range list {
		id := itoa(int64(b["id"].(float64)))
		pages, ok := want[b["title"].(string)]
		if !ok {
			t.Fatalf("unexpected book %v", b["title"])
		}
		info := getJSON[map[string]any](t, client, base+"/api/books/"+id+"/comic")
		if info["pages"] != float64(pages) {
			t.Fatalf("%s: comic info %v", b["title"], info)
		}
		resp, err := client.Get(base + "/api/books/" + id + "/comic/1")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || len(body) == 0 {
			t.Fatalf("%s: page 1: %d %s", b["title"], resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}
