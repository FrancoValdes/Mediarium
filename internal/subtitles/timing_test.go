package subtitles

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func srt(starts []int64, scale float64, offset int64) []byte {
	var b strings.Builder
	for i, s := range starts {
		a := int64(math.Round(float64(s)*scale)) + offset
		fmt.Fprintf(&b, "%d\n%s --> %s\nLine %d\n\n", i+1, formatStamp(a, "00:00:00,000"), formatStamp(a+1800, "00:00:00,000"), i+1)
	}
	return []byte(b.String())
}

func TestStampsRoundTrip(t *testing.T) {
	cases := []struct {
		in   string
		ms   int64
		back string
	}{
		{"00:01:02,345", 62345, "00:01:02,345"},
		{"01:02:03.004", 3723004, "01:02:03.004"},
		{"02:03.5", 123500, "02:03.500"},
		{"1:00:00,000", 3600000, "01:00:00,000"},
	}
	for _, tc := range cases {
		ms, ok := parseStamp(tc.in)
		if !ok || ms != tc.ms || formatStamp(ms, tc.in) != tc.back {
			t.Errorf("%q: %d %v %q", tc.in, ms, ok, formatStamp(ms, tc.in))
		}
	}
	if _, ok := parseStamp("nonsense"); ok {
		t.Error("nonsense parsed")
	}
	if ms, ok := parseStamp("999:00:00,000"); !ok || ms != 999*3600000 {
		t.Errorf("999 hours: %d %v", ms, ok)
	}
	for _, in := range []string{"1000:00:00,000", "1234567890123:00:00,000"} {
		if _, ok := parseStamp(in); ok {
			t.Errorf("%q parsed", in)
		}
	}
	// A timing line with absurd hours is not a cue at all.
	if got := Starts([]byte("1\n1234567890123:00:00,000 --> 1234567890123:00:01,000\nHi\n")); len(got) != 0 {
		t.Errorf("absurd hours read as %v", got)
	}
}

func TestAlignLimits(t *testing.T) {
	var ref []int64
	at := int64(5000)
	for i := 0; i < 400; i++ {
		ref = append(ref, at)
		at += 1500 + int64((i*7919)%5300)
	}
	// Cues that start together count once.
	var doubled []int64
	for _, s := range ref {
		doubled = append(doubled, s, s, s)
	}
	m, err := Align(srt(doubled, 1, 3000), srt(ref, 1, 0))
	if err != nil || m.OffsetMs < -3020 || m.OffsetMs > -2980 || m.Matched < 0.95 || m.Matched > 1 {
		t.Errorf("repeated starts: %+v %v", m, err)
	}

	// Too many cues.
	many := make([]int64, maxAlignCues+1)
	for i := range many {
		many[i] = int64(i) * 2000
	}
	if _, err := Align(srt(many, 1, 0), srt(ref, 1, 0)); !errors.Is(err, ErrTooManyCues) {
		t.Errorf("too many cues: %v", err)
	}

	// Under the cue cap, but so dense that every pair would vote.
	dense := make([]int64, 9000)
	for i := range dense {
		dense[i] = int64(i) * 30
	}
	start := time.Now()
	if _, err := Align(srt(dense, 1, 0), srt(dense, 1, 0)); !errors.Is(err, ErrTooManyCues) {
		t.Errorf("dense: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("refusing a dense pair took %v", d)
	}

	// A cancelled request stops the work.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AlignContext(ctx, srt(ref, 1, 0), srt(ref, 1, 0)); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

func TestRetimeKeepsEverythingElse(t *testing.T) {
	in := "1\n00:00:01,000 --> 00:00:02,500 X1:10 Y1:20\n<i>Hello</i>\n\n2\n00:00:03,000 --> 00:00:04,000\nWorld --> again\n"
	out, err := Retime([]byte(in), 1, 1500)
	if err != nil {
		t.Fatal(err)
	}
	want := "1\n00:00:02,500 --> 00:00:04,000 X1:10 Y1:20\n<i>Hello</i>\n\n2\n00:00:04,500 --> 00:00:05,500\nWorld --> again\n"
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	back, _ := Retime(out, 1, -5000)
	if !strings.Contains(string(back), "00:00:00,000 --> 00:00:00,500") {
		t.Fatalf("times below zero stop at zero: %s", back)
	}
	vtt := "WEBVTT\n\n00:01.000 --> 00:02.000\nHi\n"
	if out, _ := Retime([]byte(vtt), 1, 250); !strings.Contains(string(out), "00:01.250 --> 00:02.250") {
		t.Fatalf("vtt: %s", out)
	}
	if _, err := Retime([]byte("no times here"), 1, 10); err != ErrNoCues {
		t.Fatalf("no cues: %v", err)
	}
}

func TestAlign(t *testing.T) {
	// A reference with uneven gaps, like real dialogue.
	var ref []int64
	at := int64(5000)
	for i := 0; i < 400; i++ {
		ref = append(ref, at)
		at += 1500 + int64((i*7919)%5300)
	}
	cases := []struct {
		name   string
		scale  float64
		offset int64
	}{
		{"same", 1, 0},
		{"4.2 seconds late", 1, 4200},
		{"2.37 seconds early", 1, -2370},
		{"made for 25 fps", 23.976 / 25, 900},
	}
	for _, tc := range cases {
		// The target is the reference moved; aligning should undo it.
		target := srt(ref, tc.scale, tc.offset)
		m, err := Align(target, srt(ref, 1, 0))
		if err != nil {
			t.Fatal(err)
		}
		fixed, _ := Retime(target, m.Scale, m.OffsetMs)
		if again, _ := Align(fixed, srt(ref, 1, 0)); math.Abs(float64(again.OffsetMs)) > 20 || again.Scale != 1 || m.Matched < 0.95 {
			t.Errorf("%s: match %+v, after fixing %+v", tc.name, m, again)
		}
	}
	// Two subtitles that have nothing to do with each other barely match.
	var other []int64
	for i := 0; i < 300; i++ {
		other = append(other, int64(i)*3333+int64((i*104729)%2999))
	}
	if m, _ := Align(srt(other, 1, 0), srt(ref, 1, 0)); m.Matched > 0.6 {
		t.Errorf("unrelated subtitles matched %.2f", m.Matched)
	}
}
