package books

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Comics are books too: a comic is a folder of pictures packed in a .cbz
// (a ZIP), .cbr (a RAR) or .cb7 (a 7z). Mediarium keeps them with the ebooks,
// reads them page by page in the reader and takes the series and number from
// the ComicInfo.xml most comic tools write into the file.

// ComicFormat reports whether ext is a comic archive.
func ComicFormat(ext string) bool {
	switch strings.TrimPrefix(strings.ToLower(ext), ".") {
	case "cbz", "cbr", "cb7":
		return true
	}
	return false
}

// ScanRank is how good a file format is when a books folder is read: an
// ebook format by its rank, a comic archive as good as a PDF.
func ScanRank(ext string) int {
	if r := EbookRank(ext); r > 0 {
		return r
	}
	if ComicFormat(ext) {
		return 1
	}
	return 0
}

// ComicInfo is what a comic's ComicInfo.xml says about it.
type ComicInfo struct {
	Title   string
	Series  string
	Number  string // "3", "0.5", "Annual 1"
	Writer  string
	Summary string
	Year    int
	Manga   string // "Yes", "YesAndRightToLeft" or "No"
}

type comicInfoXML struct {
	Title   string `xml:"Title"`
	Series  string `xml:"Series"`
	Number  string `xml:"Number"`
	Writer  string `xml:"Writer"`
	Summary string `xml:"Summary"`
	Year    string `xml:"Year"` // a string: some tools write "2021-05" or "c. 1990"
	Manga   string `xml:"Manga"`
}

var comicYear = regexp.MustCompile(`\d{4}`)

// parseComicYear takes the first four-digit year out of a ComicInfo <Year>,
// or 0 when there is none.
func parseComicYear(s string) int {
	y, _ := strconv.Atoi(comicYear.FindString(s))
	if y < 1000 || y > 2999 {
		return 0
	}
	return y
}

const maxComicInfo = 1 << 20

// ReadComicInfo reads ComicInfo.xml out of a .cbz file. ok is false when the
// file isn't a ZIP, has no such file or it can't be read.
func ReadComicInfo(file string) (info ComicInfo, ok bool) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return ComicInfo{}, false
	}
	defer zr.Close()
	return ReadComicInfoFrom(&zr.Reader)
}

// ReadComicInfoFrom is ReadComicInfo for a ZIP that is already open.
func ReadComicInfoFrom(zr *zip.Reader) (info ComicInfo, ok bool) {
	for _, f := range zr.File {
		if !strings.EqualFold(path.Base(f.Name), "ComicInfo.xml") || f.UncompressedSize64 > maxComicInfo {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ComicInfo{}, false
		}
		defer rc.Close()
		var x comicInfoXML
		if err := xml.NewDecoder(io.LimitReader(rc, maxComicInfo)).Decode(&x); err != nil {
			return ComicInfo{}, false
		}
		return ComicInfo{
			Title: strings.TrimSpace(x.Title), Series: strings.TrimSpace(x.Series), Number: strings.TrimSpace(x.Number),
			Writer: strings.TrimSpace(x.Writer), Summary: strings.TrimSpace(x.Summary), Year: parseComicYear(x.Year), Manga: strings.TrimSpace(x.Manga),
		}, true
	}
	return ComicInfo{}, false
}

// DisplayTitle is the name a comic goes by in the library: "Series #3: Title",
// or just the title or series when that is all there is. "" when the file says nothing.
func (c ComicInfo) DisplayTitle() string {
	name := c.Series
	if c.Number != "" && name != "" {
		name += " #" + c.Number
	}
	switch {
	case name == "":
		return c.Title
	case c.Title != "" && !strings.EqualFold(c.Title, c.Series) && !strings.EqualFold(c.Title, name):
		return name + ": " + c.Title
	}
	return name
}

var comicPictures = map[string]bool{"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true}

// ComicPages picks the pictures out of the names inside a comic archive and
// puts them in reading order: names are compared with their numbers as
// numbers ("page2" before "page10"), and the folders and hidden files of
// other tools (__MACOSX, Thumbs.db, ComicInfo.xml) are left out.
func ComicPages(names []string) []string {
	var pages []string
	for _, n := range names {
		clean := strings.ReplaceAll(n, "\\", "/")
		if strings.HasSuffix(clean, "/") || !comicPictures[strings.TrimPrefix(strings.ToLower(path.Ext(clean)), ".")] {
			continue
		}
		hidden := false
		for _, part := range strings.Split(clean, "/") {
			if strings.HasPrefix(part, ".") || part == "__MACOSX" {
				hidden = true
			}
		}
		if !hidden {
			pages = append(pages, n)
		}
	}
	sort.SliceStable(pages, func(i, j int) bool { return naturalLess(pages[i], pages[j]) })
	return pages
}

// naturalLess compares two names the way a person sorts them: a run of digits
// counts as one number, letters ignore case.
func naturalLess(a, b string) bool {
	ra, rb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if unicode.IsDigit(ra[i]) && unicode.IsDigit(rb[j]) {
			si := i
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			sj := j
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na, _ := strconv.ParseUint(strings.TrimLeft(string(ra[si:i]), "0"), 10, 64)
			nb, _ := strconv.ParseUint(strings.TrimLeft(string(rb[sj:j]), "0"), 10, 64)
			if na != nb {
				return na < nb
			}
			continue
		}
		if ra[i] != rb[j] {
			return ra[i] < rb[j]
		}
		i++
		j++
	}
	return len(ra)-i < len(rb)-j
}
