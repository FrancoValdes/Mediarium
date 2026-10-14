package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The comic cache keeps to its size limit by removing the comics opened
// longest ago, but never one being read or the one just unpacked; a folder a
// crash left half-unpacked goes, and so does anything not opened for weeks.
func TestPruneComicCache(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	add := func(name string, size int, used time.Time, done bool) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "p1.jpg"), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if done {
			mark := filepath.Join(dir, ".done")
			if err := os.WriteFile(mark, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(mark, used, used); err != nil {
				t.Fatal(err)
			}
		}
	}
	prefix := fmt.Sprintf("prune-%d-", now.UnixNano()) // comicLocks is shared by the whole package
	key := func(s string) string { return prefix + s }
	add(key("oldest-in-use"), 100, now.Add(-5*time.Hour), true)
	add(key("old"), 100, now.Add(-4*time.Hour), true)
	add(key("middle"), 100, now.Add(-3*time.Hour), true)
	add(key("recent"), 100, now.Add(-1*time.Hour), true)
	add(key("just-unpacked"), 100, now.Add(-10*time.Hour), true)
	add(key("crashed"), 100, now, false)
	add(key("weeks-old"), 1, now.Add(-30*24*time.Hour), true)

	reading := comicLock(key("oldest-in-use"))
	reading.RLock()
	pruneComicCache(root, key("just-unpacked"), 300)
	reading.RUnlock()

	left := map[string]bool{}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		left[e.Name()] = true
	}
	for name, want := range map[string]bool{
		"oldest-in-use": true,  // being read
		"old":           false, // the oldest that can go
		"middle":        false, // still over the limit after "old"
		"recent":        true,  // 300 bytes are left: within the limit
		"just-unpacked": true,
		"crashed":       false,
		"weeks-old":     false,
	} {
		if left[key(name)] != want {
			t.Errorf("%s: kept = %v, want %v", name, left[key(name)], want)
		}
	}
}

// A folder being unpacked (its lock held alone) is left alone.
func TestPruneComicCacheSkipsAnUnpack(t *testing.T) {
	root := t.TempDir()
	name := fmt.Sprintf("unpacking-%d", time.Now().UnixNano())
	if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
		t.Fatal(err)
	}
	mu := comicLock(name)
	mu.Lock()
	pruneComicCache(root, "", 0)
	mu.Unlock()
	if _, err := os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatalf("the folder being unpacked was removed: %v", err)
	}
}

func TestComicListCacheDropsTheLeastRecentlyUsed(t *testing.T) {
	c := &comicListCache{max: 2}
	c.put("a", comicList{pages: []string{"a"}})
	c.put("b", comicList{pages: []string{"b"}})
	c.get("a") // a is now the most recent
	c.put("c", comicList{pages: []string{"c"}})
	if _, ok := c.get("b"); ok {
		t.Error("b should have been dropped")
	}
	for _, k := range []string{"a", "c"} {
		if l, ok := c.get(k); !ok || l.pages[0] != k {
			t.Errorf("%s: %v %v", k, l, ok)
		}
	}
}
