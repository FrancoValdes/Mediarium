package torrentclient

import (
	"bytes"
	"strings"
	"testing"

	alog "github.com/anacrolix/log"
)

// The engine's log lines go where SetLogOutput says, not straight to stderr.
func TestSetLogOutput(t *testing.T) {
	saved := alog.Default.Handlers
	defer func() { alog.Default.Handlers = saved }()
	var buf bytes.Buffer
	SetLogOutput(&buf)
	alog.Levelf(alog.Warning, "bad tracker %s", "udp://tracker.example/announce?passkey=x")
	got := buf.String()
	if !strings.Contains(got, "bad tracker udp://tracker.example") || !strings.HasPrefix(got, "torrent engine [") || strings.Count(got, "\n") != 1 {
		t.Fatalf("log line: %q", got)
	}
}
