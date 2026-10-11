package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/png"
	"strconv"
	"testing"
	"time"
)

func pngDataURL(t *testing.T, w, h int) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestPowStampMeetsTheTarget(t *testing.T) {
	ts := time.Now().Unix()
	nonce, err := powStamp(context.Background(), ts, "Title", "A message long enough", 16)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("Title\nA message long enough"))
	d := sha256.Sum256([]byte("mediarium-report|" + strconv.FormatInt(ts, 10) + "|" + nonce + "|" + hex.EncodeToString(sum[:])))
	if leadingZeroBits(d[:]) < 16 {
		t.Fatalf("stamp %s has only %d zero bits", nonce, leadingZeroBits(d[:]))
	}
}

func TestDecodeScreenshot(t *testing.T) {
	if _, _, err := decodeScreenshot(pngDataURL(t, 40, 30)); err != nil {
		t.Fatalf("a real PNG must pass: %v", err)
	}
	php := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(`<?php system($_GET["c"]); ?>`))
	if _, _, err := decodeScreenshot(php); err == nil {
		t.Fatal("code labelled as a PNG must be refused")
	}
	if _, _, err := decodeScreenshot("data:image/svg+xml;base64,PHN2Zz4="); err == nil {
		t.Fatal("SVG must be refused")
	}
	if _, _, err := decodeScreenshot(pngDataURL(t, 9000, 2)); err == nil {
		t.Fatal("a huge image must be refused")
	}
}
