package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// zipFiles builds an archive of real, compressed entries and opens it.
func zipFiles(t *testing.T, entries map[string][]byte) []*zip.File {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr.File
}

// forgedZip is an archive that is a few hundred bytes long and claims its
// entries unpack to the given sizes, which is all a zip bomb has to do before
// anyone reads it.
func forgedZip(t *testing.T, names []string, declared uint64) []*zip.File {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.CreateRaw(&zip.FileHeader{
			Name: name, Method: zip.Deflate,
			CompressedSize64: 2, UncompressedSize64: declared,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte{0x03, 0x00}); err != nil { // an empty deflate stream
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr.File
}

func TestCheckEpubZip(t *testing.T) {
	small := zipLimits{entries: 10, total: 4 << 20, entry: 2 << 20, markup: 1 << 20}
	zeros := func(n int) []byte { return make([]byte, n) }

	// Real archives, really compressed: 3 MB of zeros is a few kilobytes.
	book := map[string][]byte{
		"mimetype":              []byte("application/epub+zip"),
		"OEBPS/ch1.xhtml":       []byte("<html><body><p>Hello</p></body></html>"),
		"OEBPS/content.opf":     []byte("<package/>"),
		"OEBPS/images/cover.jp": zeros(1 << 20),
	}
	if err := checkEpubZip(zipFiles(t, book), small); err != nil {
		t.Errorf("a normal book was refused: %v", err)
	}

	tests := []struct {
		name  string
		files []*zip.File
		want  string
	}{
		{"one file too big", zipFiles(t, map[string][]byte{"a.jpg": zeros(3 << 20)}), "too big"},
		{"a page too big for memory", zipFiles(t, map[string][]byte{"a.xhtml": zeros(2 << 20)}), "too big"},
		{"a package file too big for memory", zipFiles(t, map[string][]byte{"content.OPF": zeros(2 << 20)}), "too big"},
		{"all of it too big", zipFiles(t, map[string][]byte{"a.jpg": zeros(2 << 20), "b.jpg": zeros(2 << 20), "c.jpg": zeros(1 << 20)}), "more than"},
		{"too many files", zipFiles(t, manyFiles(11)), "too many"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkEpubZip(tt.files, small)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func manyFiles(n int) map[string][]byte {
	m := map[string][]byte{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("f%d.txt", i)] = []byte("x")
	}
	return m
}

// The real limits, against archives that only claim to be big.
func TestCheckEpubZipRealLimits(t *testing.T) {
	tests := []struct {
		name     string
		names    []string
		declared uint64
		wantErr  bool
	}{
		{"a book of ordinary size", []string{"a.xhtml", "b.jpg"}, 4 << 20, false},
		{"a large picture", []string{"a.jpg"}, 400 << 20, false},
		{"a picture over 512 MB", []string{"a.jpg"}, 513 << 20, true},
		{"a page over 32 MB", []string{"a.xhtml"}, 33 << 20, true},
		{"three pictures that add up to over 1 GB", []string{"a.jpg", "b.jpg", "c.jpg"}, 400 << 20, true},
		{"two large pictures are fine", []string{"a.jpg", "b.jpg"}, 400 << 20, false},
		{"one huge claim", []string{"bomb.bin"}, 3 << 30, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkEpubZip(forgedZip(t, tt.names, tt.declared), kepubZipLimits)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}

	names := make([]string, 20001)
	for i := range names {
		names[i] = fmt.Sprintf("f%d.txt", i)
	}
	if err := checkEpubZip(forgedZip(t, names, 1), kepubZipLimits); err == nil {
		t.Error("20001 files were let through")
	}
	if err := checkEpubZip(forgedZip(t, names[:20000], 1), kepubZipLimits); err != nil {
		t.Errorf("20000 files were refused: %v", err)
	}
}
