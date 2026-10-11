package api

import (
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/fsinfo"
)

// inspectFor is how long the dashboard and the health check reuse what they
// learned about a folder. Looking means creating and deleting a test file and
// asking the disk for its size, which on a network share takes a while, and
// the dashboard asks every few seconds.
const inspectFor = 30 * time.Second

var (
	inspectMu    sync.Mutex
	inspectSaved = map[string]savedInspect{}
)

type savedInspect struct {
	at     time.Time
	folder fsinfo.Folder
}

// inspectCached is fsinfo.Inspect, remembered for half a minute. The folder
// settings pages use the live one, so a fix shows straight away there.
func inspectCached(path string) fsinfo.Folder {
	inspectMu.Lock()
	if s, ok := inspectSaved[path]; ok && time.Since(s.at) < inspectFor {
		inspectMu.Unlock()
		return s.folder
	}
	inspectMu.Unlock()
	f := fsinfo.Inspect(path)
	inspectMu.Lock()
	if len(inspectSaved) > 64 { // folders come from settings; this only guards against a runaway
		inspectSaved = map[string]savedInspect{}
	}
	inspectSaved[path] = savedInspect{at: time.Now(), folder: f}
	inspectMu.Unlock()
	return f
}
