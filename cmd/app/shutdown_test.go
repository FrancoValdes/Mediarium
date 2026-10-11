package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// A stream that never ends must not hold the program up: after the grace the
// connection is cut and stopServing returns.
func TestStopServingCutsAStreamThatOutlastsTheGrace(t *testing.T) {
	started := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		close(started)
		chunk := make([]byte, 32<<10)
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(10 * time.Millisecond)
		}
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)

	resp, err := http.Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	<-started

	begin := time.Now()
	stopServing(srv, 200*time.Millisecond)
	if took := time.Since(begin); took > 3*time.Second {
		t.Fatalf("stopServing took %s with a stream still open", took)
	}
	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, resp.Body) // ends when the server cuts the connection
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream was still open after stopServing")
	}
}

func TestStopServingReturnsAtOnceWhenNothingIsRunning(t *testing.T) {
	srv := &http.Server{Handler: http.NotFoundHandler()}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	begin := time.Now()
	stopServing(srv, 5*time.Second)
	if took := time.Since(begin); took > time.Second {
		t.Fatalf("stopServing took %s with nothing running", took)
	}
}
