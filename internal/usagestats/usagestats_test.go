package usagestats

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// The message has exactly these fields. Adding a field means changing this test,
// the documentation, the receiver and the public page on purpose.
func TestPayloadHasOnlyTheDocumentedFields(t *testing.T) {
	want := []string{"schema", "id", "version", "arch", "docker", "cores", "memory", "age", "library", "accounts", "indexers", "media", "features", "servers"}
	var got []string
	typ := reflect.TypeOf(Payload{})
	for i := 0; i < typ.NumField(); i++ {
		got = append(got, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
}

func TestBuildKeepsOnlyApprovedNames(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	p := Build(Facts{
		ID: "0123456789abcdef0123456789abcdef", Version: "3.0.0", Arch: "amd64", Docker: true, Cores: 6, MemoryMB: 7000,
		Installed: now.Add(-40 * 24 * time.Hour), Titles: 1500, Accounts: 3, Indexers: 4, Now: now,
		Media:    []string{"tv", "movies", "secret-kind", "movies"},
		Features: []string{"usenet", "my-private-tracker.example", "torrents"},
		Servers:  []string{"plex", "http://192.168.1.5:32400"},
	})
	want := Payload{Schema: 1, ID: "0123456789abcdef0123456789abcdef", Version: "3.0.0", Arch: "amd64", Docker: true, Cores: "5-8", Memory: "4-8 GB", Age: "1-3 months",
		Library: "1001-5000", Accounts: "2-5", Indexers: "3-5", Media: []string{"movies", "tv"}, Features: []string{"torrents", "usenet"}, Servers: []string{"plex"}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got %+v\nwant %+v", p, want)
	}
	if !Valid(p) {
		t.Fatal("what Build makes must be valid")
	}
}

func TestBuildCleansUnknownValues(t *testing.T) {
	p := Build(Facts{ID: "x", Version: "not a version\n<script>", Arch: "riscv", Now: time.Now()})
	if p.Version != "0.0.0" || p.Arch != "other" || p.Memory != "unknown" || p.Age != "unknown" || p.Library != "empty" {
		t.Fatalf("unexpected: %+v", p)
	}
	if Valid(p) {
		t.Fatal("a message without a real ID must not be valid")
	}
}

func TestValidRefusesEverythingElse(t *testing.T) {
	good := Build(Facts{ID: NewID([]byte{0xab, 0xcd, 0xef, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}), Version: "3.0.0", Arch: "arm64", Now: time.Now()})
	if !Valid(good) {
		t.Fatalf("good message refused: %+v", good)
	}
	for name, mutate := range map[string]func(*Payload){
		"long id":       func(p *Payload) { p.ID += "0" },
		"upper id":      func(p *Payload) { p.ID = strings.ToUpper(p.ID) },
		"free text":     func(p *Payload) { p.Cores = "my home server" },
		"odd feature":   func(p *Payload) { p.Features = []string{"nzbgeek.info"} },
		"twice":         func(p *Payload) { p.Media = []string{"tv", "tv"} },
		"other version": func(p *Payload) { p.Version = "3.0.0; DROP TABLE" },
		"schema":        func(p *Payload) { p.Schema = 2 },
	} {
		p := good
		mutate(&p)
		if Valid(p) {
			t.Errorf("%s: should be refused", name)
		}
	}
}

func TestEveryMessageIsSmall(t *testing.T) {
	p := Build(Facts{ID: NewID(make([]byte, 16)), Version: "3.0.0", Arch: "amd64", Now: time.Now(), Media: MediaKinds, Features: Features, Servers: Servers})
	b, _ := json.Marshal(p)
	if len(b) > 1200 {
		t.Fatalf("message is %d bytes", len(b))
	}
}

func TestBands(t *testing.T) {
	for _, tc := range []struct {
		got, want string
	}{
		{coresBand(1), "1-2"}, {coresBand(4), "3-4"}, {coresBand(24), "17+"},
		{memoryBand(0), "unknown"}, {memoryBand(1000), "under 2 GB"}, {memoryBand(40000), "over 32 GB"},
		{libraryBand(0), "empty"}, {libraryBand(100), "1-100"}, {libraryBand(101), "101-1000"}, {libraryBand(30000), "over 20000"},
		{accountsBand(1), "1"}, {accountsBand(6), "6-20"}, {indexerBand(0), "0"}, {indexerBand(11), "over 10"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}
