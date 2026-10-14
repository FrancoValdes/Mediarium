package musicbrainz

import (
	"testing"
	"time"
)

// The cache is bounded by bytes as well as entries, and a big answer is used
// but not kept.
func TestCacheBoundedByBytes(t *testing.T) {
	c := newCache(time.Hour, 100)
	c.maxBytes, c.maxBody = 1000, 400
	c.put("big", make([]byte, 401))
	if _, ok := c.get("big"); ok {
		t.Fatal("an answer over the size limit was kept")
	}
	for i := 0; i < 10; i++ {
		c.put(string(rune('a'+i)), make([]byte, 300))
		if c.size > c.maxBytes {
			t.Fatalf("after %d answers the cache holds %d bytes", i+1, c.size)
		}
	}
	if len(c.entries) != 3 || c.size != 900 {
		t.Fatalf("%d entries, %d bytes", len(c.entries), c.size)
	}
	// Putting the same key again counts its size once.
	c.put("j", make([]byte, 300))
	if c.size != 900 {
		t.Fatalf("replacing an entry: %d bytes", c.size)
	}
	if newCache(time.Hour, 10).maxBody != 512<<10 {
		t.Fatal("answers over 512 KB should not be kept")
	}
}
