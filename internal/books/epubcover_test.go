package books

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPNG(w, h int, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// epubWith writes an EPUB whose package file is opf and whose other files are given.
func epubWith(t *testing.T, opf string, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.epub")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
	}
	put("META-INF/container.xml", []byte(`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`))
	put("OEBPS/content.opf", []byte(opf))
	for n, d := range files {
		put(n, d)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEPUBCoverFindsTheCover(t *testing.T) {
	pic := testPNG(10, 20, color.RGBA{200, 0, 0, 255})
	other := testPNG(5, 5, color.RGBA{0, 0, 200, 255})
	tests := []struct {
		name  string
		opf   string
		files map[string][]byte
		found bool
	}{
		{"EPUB 3 cover-image property", `<package><manifest><item id="a" href="img/other.png" media-type="image/png"/><item id="b" href="img/c.png" media-type="image/png" properties="cover-image"/></manifest></package>`,
			map[string][]byte{"OEBPS/img/other.png": other, "OEBPS/img/c.png": pic}, true},
		{"EPUB 2 meta cover", `<package><metadata><meta name="cover" content="ci"/></metadata><manifest><item id="x" href="x.png" media-type="image/png"/><item id="ci" href="c.png" media-type="image/png"/></manifest></package>`,
			map[string][]byte{"OEBPS/x.png": other, "OEBPS/c.png": pic}, true},
		{"item called cover", `<package><manifest><item id="x" href="x.png" media-type="image/png"/><item id="cover" href="c%20one.png" media-type="image/png"/></manifest></package>`,
			map[string][]byte{"OEBPS/x.png": other, "OEBPS/c one.png": pic}, true},
		{"file name has cover in it", `<package><manifest><item id="x" href="x.png" media-type="image/png"/><item id="y" href="Cover_large.png" media-type="image/png"/></manifest></package>`,
			map[string][]byte{"OEBPS/x.png": other, "OEBPS/Cover_large.png": pic}, true},
		{"no cover at all", `<package><manifest><item id="x" href="x.png" media-type="image/png"/></manifest></package>`,
			map[string][]byte{"OEBPS/x.png": other}, false},
		{"cover file missing", `<package><manifest><item id="cover" href="gone.png" media-type="image/png" properties="cover-image"/></manifest></package>`, nil, false},
		{"not a picture", `<package><manifest><item id="cover" href="c.png" media-type="image/png" properties="cover-image"/></manifest></package>`,
			map[string][]byte{"OEBPS/c.png": []byte("<html>not an image</html>")}, false},
	}
	for _, tt := range tests {
		data, ct, err := EPUBCover(epubWith(t, tt.opf, tt.files))
		if tt.found {
			if err != nil || ct != "image/png" || !bytes.Equal(data, pic) {
				t.Errorf("%s: %v %s", tt.name, err, ct)
			}
		} else if !errors.Is(err, ErrNoCover) {
			t.Errorf("%s: want ErrNoCover, got %v", tt.name, err)
		}
	}
	// Not an EPUB at all.
	bad := filepath.Join(t.TempDir(), "x.epub")
	_ = os.WriteFile(bad, []byte("not a zip"), 0o644)
	if _, _, err := EPUBCover(bad); err == nil {
		t.Error("a file that is not a zip should fail")
	}
}

func decodeJPEG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestResizeCover(t *testing.T) {
	big := testPNG(600, 900, color.RGBA{10, 120, 200, 255})
	out, err := ResizeCover(big, 200, 300)
	if err != nil {
		t.Fatal(err)
	}
	if b := decodeJPEG(t, out).Bounds(); b.Dx() != 200 || b.Dy() != 300 {
		t.Errorf("fit 200x300: %v", b)
	}
	// The proportions stay: a wide box is limited by the height.
	out, _ = ResizeCover(big, 1000, 450)
	if b := decodeJPEG(t, out).Bounds(); b.Dx() != 300 || b.Dy() != 450 {
		t.Errorf("fit 1000x450: %v", b)
	}
	// A small picture is not blown up.
	out, _ = ResizeCover(testPNG(50, 80, color.White), 800, 1200)
	if b := decodeJPEG(t, out).Bounds(); b.Dx() != 50 || b.Dy() != 80 {
		t.Errorf("small picture: %v", b)
	}
	// Transparent areas go on white.
	out, _ = ResizeCover(testPNG(40, 40, color.NRGBA{0, 0, 0, 0}), 20, 20)
	r, g, b, _ := decodeJPEG(t, out).At(5, 5).RGBA()
	if r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Errorf("transparent should become white, got %d %d %d", r>>8, g>>8, b>>8)
	}
	// Colours survive the shrinking.
	out, _ = ResizeCover(big, 100, 150)
	r, g, b, _ = decodeJPEG(t, out).At(10, 10).RGBA()
	if int(r>>8) > 40 || int(g>>8) < 90 || int(b>>8) < 170 {
		t.Errorf("colour changed: %d %d %d", r>>8, g>>8, b>>8)
	}
	if _, err := ResizeCover([]byte("junk"), 100, 100); err == nil {
		t.Error("junk should fail")
	}
}

// pngHeader is the start of a PNG that says it is w by h, with the given bit
// depth and colour type: enough for image.DecodeConfig.
func pngHeader(w, h uint32, depth, colourType byte) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = depth, colourType
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestResizeCoverRefusesHugePictures(t *testing.T) {
	cases := []struct {
		name              string
		w, h              uint32
		depth, colourType byte
	}{
		{"30 megapixels", 6000, 5000, 8, 6},
		{"16-bit, 16 megapixels", 4000, 4000, 16, 6},
		{"a very long strip", 1, 1 << 30, 8, 0},
	}
	for _, tc := range cases {
		if _, err := ResizeCover(pngHeader(tc.w, tc.h, tc.depth, tc.colourType), 200, 300); err == nil || !strings.Contains(err.Error(), "cover is") {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	// Within the limits the size check passes, and the missing pixels are what fails.
	if _, err := ResizeCover(pngHeader(4000, 4000, 8, 6), 200, 300); err == nil || strings.Contains(err.Error(), "cover is") {
		t.Errorf("8-bit 16 megapixels: %v", err)
	}
}

// plainImage hides an image's type, so shrink takes the slow, general way.
type plainImage struct{ image.Image }

func TestShrinkFastPathsMatch(t *testing.T) {
	nrgba := image.NewNRGBA(image.Rect(0, 0, 37, 53))
	rgba := image.NewRGBA(image.Rect(0, 0, 37, 53))
	for y := 0; y < 53; y++ {
		for x := 0; x < 37; x++ {
			nrgba.SetNRGBA(x, y, color.NRGBA{uint8(x * 7), uint8(y * 5), uint8(x * y), uint8(255 - x*3)})
			rgba.Set(x, y, nrgba.At(x, y))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgba, nil); err != nil {
		t.Fatal(err)
	}
	ycc, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ycc.(*image.YCbCr); !ok {
		t.Fatalf("jpeg decoded to %T", ycc)
	}
	for _, img := range []image.Image{nrgba, rgba, ycc} {
		b := img.Bounds()
		fast := shrink(img, b.Dx(), b.Dy(), 11, 17)
		slow := shrink(plainImage{img}, b.Dx(), b.Dy(), 11, 17)
		if !bytes.Equal(fast.Pix, slow.Pix) {
			t.Errorf("%T: the fast way gives other pixels", img)
		}
	}
}
