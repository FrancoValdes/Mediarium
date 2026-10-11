package usagestats

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// phpList reads `const NAME = ['a', 'b'];` from the receiver.
func phpList(t *testing.T, src, name string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)const ` + name + ` = \[(.*?)\];`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("receiver has no list %s", name)
	}
	var out []string
	for _, q := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	return out
}

// The receiver (PHP) and the sender (Go) must agree on every list, or a
// perfectly good message would be refused, or something unlisted accepted.
func TestReceiverListsMatchTheSender(t *testing.T) {
	b, err := os.ReadFile("../../tools/stats-receiver/lib.php")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for name, want := range map[string][]string{
		"MEDIA":    MediaKinds,
		"FEATURES": Features,
		"SERVERS":  Servers,
		"ARCHES":   Arches,
		"CORES":    CoresBands,
		"MEMORY":   append([]string{"unknown"}, MemoryBands...),
		"AGE":      append([]string{"unknown"}, AgeBands...),
		"LIBRARY":  LibraryBands,
		"ACCOUNTS": AccountsBands,
		"INDEXERS": IndexerBands,
		"FIELDS":   {"schema", "id", "version", "arch", "docker", "cores", "memory", "age", "library", "accounts", "indexers", "media", "features", "servers"},
	} {
		if got := phpList(t, src, name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: receiver has %v, sender has %v", name, got, want)
		}
	}
	if !strings.Contains(src, "const SCHEMA = 1;") {
		t.Error("the receiver is for another schema")
	}
}
