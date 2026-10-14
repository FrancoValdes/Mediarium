package books

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // covers come in all three
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// ErrNoCover is returned for a book file with no cover picture in it.
var ErrNoCover = errors.New("the book has no cover picture")

// maxEmbeddedCover bounds the cover read out of a book file.
const maxEmbeddedCover = 12 << 20

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type opfMeta struct {
	Name    string `xml:"name,attr"`
	Content string `xml:"content,attr"`
}

type opfPackage struct {
	Manifest []opfItem `xml:"manifest>item"`
	Meta     []opfMeta `xml:"metadata>meta"`
}

type containerXML struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

func readZipFile(zr *zip.Reader, name string, limit int64) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("%s is larger than %d bytes", name, limit)
		}
		return data, nil
	}
	return nil, fmt.Errorf("%s: %w", name, io.EOF)
}

// EPUBCover reads the cover picture out of an EPUB file. The bytes are a
// JPEG, PNG or GIF (checked by their content, not by the name).
func EPUBCover(file string) ([]byte, string, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, "", fmt.Errorf("open the book: %w", err)
	}
	defer zr.Close()
	return coverFromZip(&zr.Reader)
}

func coverFromZip(zr *zip.Reader) ([]byte, string, error) {
	cont, err := readZipFile(zr, "META-INF/container.xml", 1<<20)
	if err != nil {
		return nil, "", fmt.Errorf("read the book's container: %w", err)
	}
	var c containerXML
	if err := xml.Unmarshal(cont, &c); err != nil || len(c.Rootfiles) == 0 {
		return nil, "", ErrNoCover
	}
	opfPath := c.Rootfiles[0].FullPath
	opfData, err := readZipFile(zr, opfPath, 8<<20)
	if err != nil {
		return nil, "", fmt.Errorf("read the book's package: %w", err)
	}
	var pkg opfPackage
	dec := xml.NewDecoder(bytes.NewReader(opfData))
	dec.Strict = false
	if err := dec.Decode(&pkg); err != nil {
		return nil, "", ErrNoCover
	}
	item, ok := coverItem(pkg)
	if !ok {
		return nil, "", ErrNoCover
	}
	href := item.Href
	if u, err := url.PathUnescape(href); err == nil {
		href = u
	}
	if i := strings.IndexAny(href, "#?"); i >= 0 {
		href = href[:i]
	}
	name := path.Clean(path.Join(path.Dir(opfPath), href))
	data, err := readZipFile(zr, name, maxEmbeddedCover)
	if err != nil {
		return nil, "", ErrNoCover
	}
	switch ct := http.DetectContentType(data); ct {
	case "image/jpeg", "image/png", "image/gif":
		return data, ct, nil
	}
	return nil, "", ErrNoCover
}

func isImage(it opfItem) bool { return strings.HasPrefix(strings.ToLower(it.MediaType), "image/") }

// coverItem finds the cover in a book's manifest: the item flagged as the
// cover image (EPUB 3), the one the metadata names (EPUB 2), one called
// "cover", or the first picture with "cover" in its file name.
func coverItem(pkg opfPackage) (opfItem, bool) {
	for _, it := range pkg.Manifest {
		for _, p := range strings.Fields(it.Properties) {
			if p == "cover-image" && isImage(it) {
				return it, true
			}
		}
	}
	for _, m := range pkg.Meta {
		if strings.EqualFold(m.Name, "cover") && m.Content != "" {
			for _, it := range pkg.Manifest {
				if it.ID == m.Content && isImage(it) {
					return it, true
				}
			}
		}
	}
	for _, it := range pkg.Manifest {
		if isImage(it) && (strings.EqualFold(it.ID, "cover") || strings.EqualFold(it.ID, "cover-image") || strings.EqualFold(it.ID, "coverimage")) {
			return it, true
		}
	}
	for _, it := range pkg.Manifest {
		if isImage(it) && strings.Contains(strings.ToLower(path.Base(it.Href)), "cover") {
			return it, true
		}
	}
	return opfItem{}, false
}

// maxPixels bounds a picture that gets decoded (a 25 megapixel scan is
// already far more than any cover), and maxDecodeBytes the memory it takes
// once decoded: a 16-bit PNG takes 8 bytes a pixel.
const (
	maxPixels      = 25_000_000
	maxDecodeBytes = 120 << 20
)

// coverSlots lets two covers at most be decoded and resized at once, so
// several big pictures don't stack up in memory.
var coverSlots = make(chan struct{}, 2)

// bytesPerPixel is about how much memory a picture of model m takes decoded.
func bytesPerPixel(m color.Model) int {
	switch m {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.YCbCrModel:
		return 3
	case color.RGBAModel, color.NRGBAModel, color.CMYKModel, color.NYCbCrAModel:
		return 4
	}
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	return 8 // 16-bit colour, or not known
}

// ResizeCover shrinks a cover so it fits in maxW by maxH (the proportions
// stay; a smaller picture is left as it is) and writes it as a JPEG. Pictures
// with transparency are put on white.
func ResizeCover(data []byte, maxW, maxH int) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("read cover size: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxPixels || cfg.Height > maxPixels ||
		cfg.Width*cfg.Height > maxPixels || cfg.Width*cfg.Height*bytesPerPixel(cfg.ColorModel) > maxDecodeBytes {
		return nil, fmt.Errorf("cover is %dx%d", cfg.Width, cfg.Height)
	}
	if maxW <= 0 || maxH <= 0 {
		maxW, maxH = cfg.Width, cfg.Height
	}
	coverSlots <- struct{}{}
	defer func() { <-coverSlots }()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode cover: %w", err)
	}
	w, h := cfg.Width, cfg.Height
	nw, nh := w, h
	if w > maxW || h > maxH {
		scale := min(float64(maxW)/float64(w), float64(maxH)/float64(h))
		nw, nh = max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
	}
	out := shrink(img, w, h, nw, nh)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 85}); err != nil {
		return nil, fmt.Errorf("write cover: %w", err)
	}
	return buf.Bytes(), nil
}

// pixels reads one pixel as premultiplied 16-bit colour, without going
// through img.At for the kinds of picture most covers decode to.
func pixels(img image.Image) func(x, y int) (r, g, b, a uint32) {
	switch p := img.(type) {
	case *image.YCbCr:
		return func(x, y int) (uint32, uint32, uint32, uint32) { return p.YCbCrAt(x, y).RGBA() }
	case *image.NRGBA:
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			i := p.PixOffset(x, y)
			s := p.Pix[i : i+4 : i+4]
			a := uint32(s[3]) * 0x101
			return uint32(s[0]) * 0x101 * a / 0xffff, uint32(s[1]) * 0x101 * a / 0xffff, uint32(s[2]) * 0x101 * a / 0xffff, a
		}
	case *image.RGBA:
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			i := p.PixOffset(x, y)
			s := p.Pix[i : i+4 : i+4]
			return uint32(s[0]) * 0x101, uint32(s[1]) * 0x101, uint32(s[2]) * 0x101, uint32(s[3]) * 0x101
		}
	}
	return func(x, y int) (uint32, uint32, uint32, uint32) { return img.At(x, y).RGBA() }
}

// shrink averages img (w by h) down to nw by nh, on white.
func shrink(img image.Image, w, h, nw, nh int) *image.RGBA {
	at := pixels(img)
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	b := img.Bounds()
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+(y+1)*h/nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+(x+1)*w/nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, a, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					cr, cg, cb, ca := at(xx, yy)
					r += uint64(cr)
					g += uint64(cg)
					bl += uint64(cb)
					a += uint64(ca)
					n++
				}
			}
			// Put transparent areas on white: add the missing opacity as white.
			white := n*0xffff - a
			out.SetRGBA(x, y, color.RGBA{
				R: uint8((r + white) / n >> 8),
				G: uint8((g + white) / n >> 8),
				B: uint8((bl + white) / n >> 8),
				A: 0xff,
			})
		}
	}
	return out
}
