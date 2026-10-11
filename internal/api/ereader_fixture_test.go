package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"testing"

	"github.com/rdborg/mediarium/internal/api"
	"github.com/rdborg/mediarium/internal/books"
)

// buildEPUB writes a small but valid EPUB 3: two chapters, a contents page and,
// when asked, a 600x900 cover picture.
func buildEPUB(t *testing.T, path, title string, cover bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string, store bool) {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if store {
			hdr.Method = zip.Store
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip", true)
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`, false)
	coverItem := ""
	if cover {
		coverItem = `<item id="cover" href="images/cover.jpg" media-type="image/jpeg" properties="cover-image"/>`
	}
	add("OEBPS/content.opf", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="id">urn:uuid:12345678-1234-1234-1234-123456789abc</dc:identifier>
    <dc:title>%s</dc:title>
    <dc:language>en</dc:language>
    <meta property="dcterms:modified">2020-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
    %s
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`, title, coverItem), false)
	add("OEBPS/nav.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head>
<body><nav epub:type="toc"><ol><li><a href="ch1.xhtml">One</a></li><li><a href="ch2.xhtml">Two</a></li></ol></nav></body></html>`, false)
	chapter := func(n, text string) string {
		return `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>` + n + `</title></head>
<body><h1>` + n + `</h1><p>` + text + `</p><p>Second paragraph of ` + n + `.</p></body></html>`
	}
	add("OEBPS/ch1.xhtml", chapter("One", "In a hole in the ground there lived a hobbit."), false)
	add("OEBPS/ch2.xhtml", chapter("Two", "Roast mutton."), false)
	if cover {
		img := image.NewRGBA(image.Rect(0, 0, 600, 900))
		for y := 0; y < 900; y++ {
			for x := 0; x < 600; x++ {
				img.Set(x, y, color.RGBA{R: uint8(x / 3), G: uint8(y / 4), B: 120, A: 255})
			}
		}
		var jb bytes.Buffer
		if err := jpeg.Encode(&jb, img, nil); err != nil {
			t.Fatal(err)
		}
		add("OEBPS/images/cover.jpg", jb.String(), false)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

type ereaderFixture struct {
	server *api.Server
	base   string
	admin  *http.Client
	root   string // the ebooks folder
	hobbit books.Book
	dune   books.Book
}

// addEbook puts a book with an ebook file in the library.
func (f *ereaderFixture) addEbook(t *testing.T, olKey, title, author string, year int, cover bool, extra func(*books.Book)) books.Book {
	t.Helper()
	file := filepath.Join(f.root, author, title, title+".epub")
	buildEPUB(t, file, title, cover)
	b := books.Book{OLKey: olKey, Title: title, Author: author, Year: year, WantEbook: true, Description: "About " + title + "."}
	if extra != nil {
		extra(&b)
	}
	added, err := f.server.BookRepo.Add(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.BookRepo.SetState(added.ID, books.Ebook, books.StatusDownloaded, "epub", file); err != nil {
		t.Fatal(err)
	}
	got, err := f.server.BookRepo.Get(added.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// newEReaderFixture is a server with ebooks switched on and two books: The
// Hobbit (with a cover picture inside, in a series) and Dune (without).
func newEReaderFixture(t *testing.T) *ereaderFixture {
	t.Helper()
	server, base, admin := loginNewServer(t)
	root := filepath.Join(t.TempDir(), "ebooks")
	postJSONMethod[map[string]any](t, admin, http.MethodPut, base+"/api/modules", map[string]any{"ebooks": true}, http.StatusOK)
	postJSONMethod[map[string]any](t, admin, http.MethodPut, base+"/api/settings", map[string]any{"ebooksPath": root}, http.StatusOK)
	f := &ereaderFixture{server: server, base: base, admin: admin, root: root}
	f.hobbit = f.addEbook(t, "OL1W", "The Hobbit", "J.R.R. Tolkien", 1937, true, func(b *books.Book) {
		b.SeriesName, b.SeriesPosition = "Middle-earth", "1"
	})
	f.dune = f.addEbook(t, "OL2W", "Dune", "Frank Herbert", 1965, false, nil)
	return f
}

// newClient is a client with no cookies: what an e-reader is.
func erClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type ereaderAccess struct {
	Username string `json:"username"`
	Password string `json:"password"`
	OPDS     struct {
		On   bool   `json:"on"`
		Path string `json:"path"`
	} `json:"opds"`
	Kobo struct {
		On   bool   `json:"on"`
		Path string `json:"path"`
	} `json:"kobo"`
}

// body reads and closes a response.
func erBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func erDecode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return v
}

// loginAs signs a user in on the web and returns the client that holds the session.
func erLoginAs(t *testing.T, base, user, pass string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	postJSON[map[string]any](t, c, base+"/api/auth/login", map[string]string{"username": user, "password": pass}, http.StatusOK)
	return c
}
