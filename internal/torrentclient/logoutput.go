package torrentclient

import (
	"fmt"
	"io"
	"strings"

	alog "github.com/anacrolix/log"
)

// SetLogOutput sends the torrent engine's own log lines to w instead of
// straight to stderr, so they pass the same secret scrubber as the rest of the
// log (a tracker address that fails to parse can carry a private passkey).
// Call it once at start-up, before New.
func SetLogOutput(w io.Writer) {
	alog.Default.Handlers = []alog.Handler{alog.StreamHandler{W: w, Fmt: formatLogLine}}
}

// formatLogLine writes one engine record as one line.
func formatLogLine(r alog.Record) []byte {
	names := ""
	if len(r.Names) > 0 {
		names = " " + strings.Join(r.Names, " ")
	}
	text := strings.TrimRight(r.Msg.String(), "\n")
	return fmt.Appendf(nil, "torrent engine [%s%s]: %s\n", r.Level.LogString(), names, text)
}
