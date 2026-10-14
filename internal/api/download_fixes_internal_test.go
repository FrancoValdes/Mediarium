package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/books"
)

func TestNotAnNZB(t *testing.T) {
	const link = "https://indexer.example/getnzb?id=42&apikey=secretkey123"
	tests := []struct {
		name    string
		body    string
		refused bool   // a plain error, not left to ParseNZB
		hint    string // part of the message
	}{
		{"an NZB", `<?xml version="1.0" encoding="utf-8"?><!DOCTYPE nzb><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file/></nzb>`, false, ""},
		{"an NZB with a byte order mark", "\xef\xbb\xbf<nzb><file/></nzb>", false, ""},
		{"an NZB in another charset", `<?xml version="1.0" encoding="iso-8859-1"?><nzb></nzb>`, false, ""},
		{"a broken NZB is left to the parser", `<nzb><file><segments>`, false, ""},
		{"daily limit", `<?xml version="1.0" encoding="UTF-8"?><error code="429" description="Request limit reached"/>`, true, "Request limit reached"},
		{"error repeating the key", `<error code="100" description="Incorrect user credentials: secretkey123"/>`, true, "Incorrect user credentials: ..."},
		{"error without a description", `<error code="300"/>`, true, "refused"},
		{"login page", "<!DOCTYPE html><html><body>Sign in</body></html>", true, "web page"},
		{"bare html", "  <html><head></head></html>", true, "web page"},
		{"json", `{"error":"limit"}`, true, "didn't send an NZB"},
		{"empty", "", true, "didn't send an NZB"},
		{"other xml", `<rss><channel/></rss>`, true, "didn't send an NZB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := notAnNZB([]byte(tt.body), link)
			if (err != nil) != tt.refused {
				t.Fatalf("err = %v, refused want %v", err, tt.refused)
			}
			if err == nil {
				return
			}
			if isBadRelease(err) {
				t.Fatal("an indexer problem must not blocklist the release")
			}
			if !strings.Contains(err.Error(), tt.hint) {
				t.Fatalf("message %q does not mention %q", err, tt.hint)
			}
			if strings.Contains(err.Error(), "secretkey123") {
				t.Fatal("the indexer key is in the message")
			}
		})
	}
}

// The commands the arr apps send run one after another, each title in turn,
// and only so many may wait.
func TestArrCommandsRunOneAfterAnother(t *testing.T) {
	s := &Server{}
	l := s.arrCommandLine()
	var running, most, done atomic.Int32
	job := func() {
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		running.Add(-1)
		done.Add(1)
	}
	gate := make(chan struct{})
	started := make(chan struct{})
	if !l.add(s, []func(){func() { close(started); <-gate }}) {
		t.Fatal("the first command was refused")
	}
	<-started // it is running, so the line itself is empty
	for i := 0; i < arrMaxWaitingCommands; i++ {
		if !l.add(s, []func(){job, job, job}) {
			t.Fatalf("command %d was refused, the line holds %d", i+1, arrMaxWaitingCommands)
		}
	}
	if l.add(s, []func(){job}) {
		t.Fatal("a command beyond the limit was taken")
	}
	close(gate)
	s.bg.Wait()
	if got, want := done.Load(), int32(arrMaxWaitingCommands*3); got != want {
		t.Fatalf("%d jobs ran, want %d", got, want)
	}
	if most.Load() != 1 {
		t.Fatalf("%d jobs ran at the same time, want 1", most.Load())
	}
	// Once the line is empty a new command starts it again.
	if !l.add(s, []func(){job}) {
		t.Fatal("a command after the line emptied was refused")
	}
	s.bg.Wait()
	if done.Load() != int32(arrMaxWaitingCommands*3+1) {
		t.Fatal("the line did not start again")
	}
}

// One failed status check of a download client or debrid service is passed
// over; checks that keep failing beyond pollGrace, or a refused login, are not.
func TestPollFailuresTolerateABlip(t *testing.T) {
	errAuth := errors.New("refused")
	blip := errors.New("connection reset")
	var p pollFailures
	for i := 0; i < 2; i++ {
		if !p.tolerate(blip, errAuth) {
			t.Fatal("a failed check right after a good one should be passed over")
		}
	}
	if p.tolerate(fmt.Errorf("login: %w", errAuth), errAuth) {
		t.Fatal("a refused login must fail at once")
	}
	p.since = time.Now().Add(-pollGrace - time.Second)
	if p.tolerate(blip, errAuth) {
		t.Fatal("checks failing for longer than the grace must fail the download")
	}
	p.reset()
	if !p.tolerate(blip, errAuth) {
		t.Fatal("a good check starts the grace again")
	}
}

// A book gets as many automatic retries after bad releases per day as a movie,
// counted per format.
func TestBookRetryBudget(t *testing.T) {
	b := bookRetriesOf(&Server{})
	now := time.Now()
	for i := 1; i <= maxAutoRetries; i++ {
		if n, ok := b.allow(7, books.Ebook, now); !ok || n != i {
			t.Fatalf("retry %d: n=%d ok=%v", i, n, ok)
		}
	}
	if _, ok := b.allow(7, books.Ebook, now); ok {
		t.Fatal("a retry beyond the daily budget went ahead")
	}
	if _, ok := b.allow(7, books.Audiobook, now); !ok {
		t.Fatal("the audiobook has its own budget")
	}
	if n, ok := b.allow(7, books.Ebook, now.Add(retryWindow+time.Minute)); !ok || n != 1 {
		t.Fatalf("a day later the budget is back: n=%d ok=%v", n, ok)
	}
}

func TestWatchTunnelStopsOnlyAfterTwoMisses(t *testing.T) {
	t.Run("a tunnel that stays is left alone", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		var gone atomic.Bool
		watchTunnel(ctx, 5*time.Millisecond, func() bool { return true }, func() { gone.Store(true) })
		if gone.Load() {
			t.Fatal("a tunnel that is still in use was reported gone")
		}
	})
	t.Run("one miss is forgiven", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		var calls atomic.Int32
		var gone atomic.Bool
		// Every other check fails: never two in a row.
		watchTunnel(ctx, 5*time.Millisecond, func() bool { return calls.Add(1)%2 == 0 }, func() { gone.Store(true) })
		if gone.Load() {
			t.Fatal("a single missed check stopped the torrent")
		}
	})
	t.Run("a replaced tunnel stops the torrent", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := make(chan struct{})
		watchTunnel(ctx, 5*time.Millisecond, func() bool { return false }, func() { close(done) })
		select {
		case <-done:
		default:
			t.Fatal("a tunnel that is gone was not reported")
		}
	})
}
