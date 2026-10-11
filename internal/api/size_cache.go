package api

import (
	"os"
	"sync"
	"time"
)

// File sizes for the Library's "Size" sort. Asking the disk for every file
// on each page load is slow on a NAS with a big library, so sizes are kept
// for a few minutes.
//
// A size that is asked for the first time is read at once. One that has only
// gone out of date is handed out as it was, and looked at again in the
// background, a few files at a time: a library of thousands of files would
// otherwise make whoever asks first after every ten minutes wait for all of
// them, and all of them go out of date together.

const sizeCacheFor = 10 * time.Minute

// refreshBatch is how many files the background refresh reads before it pauses,
// and refreshPause how long it pauses, so a slow drive is not hammered.
const (
	refreshBatch = 50
	refreshPause = 20 * time.Millisecond
)

type sizeEntry struct {
	size int64
	at   time.Time
}

var fileSizes = struct {
	sync.Mutex
	m       map[string]sizeEntry
	pending map[string]struct{} // out of date, waiting to be read again
	working bool                // the background refresh is running
}{m: map[string]sizeEntry{}, pending: map[string]struct{}{}}

// readSize is the size of the file at path, 0 when it is missing.
func readSize(path string) int64 {
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return info.Size()
	}
	return 0
}

// cachedFileSize is the size of the file at path (0 when it is missing). It
// is read from the disk the first time, and then at most once every
// sizeCacheFor, in the background.
func cachedFileSize(path string, now time.Time) int64 {
	if path == "" {
		return 0
	}
	fileSizes.Lock()
	e, ok := fileSizes.m[path]
	if ok && now.Sub(e.at) < sizeCacheFor {
		fileSizes.Unlock()
		return e.size
	}
	if ok {
		// Out of date: answer with what is known, and read it again soon.
		fileSizes.pending[path] = struct{}{}
		if !fileSizes.working {
			fileSizes.working = true
			go refreshSizes()
		}
		fileSizes.Unlock()
		return e.size
	}
	fileSizes.Unlock()

	size := readSize(path)
	fileSizes.Lock()
	if len(fileSizes.m) > 200000 { // a runaway library: start over rather than grow without end
		fileSizes.m = map[string]sizeEntry{}
	}
	fileSizes.m[path] = sizeEntry{size: size, at: now}
	fileSizes.Unlock()
	return size
}

// refreshSizes reads the out-of-date sizes again, a batch at a time, until
// there are none left.
func refreshSizes() {
	for {
		fileSizes.Lock()
		batch := make([]string, 0, refreshBatch)
		for p := range fileSizes.pending {
			batch = append(batch, p)
			delete(fileSizes.pending, p)
			if len(batch) == refreshBatch {
				break
			}
		}
		if len(batch) == 0 {
			fileSizes.working = false
			fileSizes.Unlock()
			return
		}
		fileSizes.Unlock()

		sizes := make([]int64, len(batch))
		for i, p := range batch {
			sizes[i] = readSize(p)
		}
		now := time.Now()
		fileSizes.Lock()
		for i, p := range batch {
			if _, kept := fileSizes.m[p]; kept {
				fileSizes.m[p] = sizeEntry{size: sizes[i], at: now}
			}
		}
		fileSizes.Unlock()
		time.Sleep(refreshPause)
	}
}
