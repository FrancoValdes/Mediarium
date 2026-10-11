package indexers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomDefinitionsJoinTheList(t *testing.T) {
	archive := tarGz(t, map[string][]byte{
		"definitions/v11/fixturepublic.yml":  fixtureDef(t, "fixturepublic"),
		"definitions/v11/fixtureprivate.yml": fixtureDef(t, "fixtureprivate"),
	})
	srv, hits := archiveServer(t, archive)
	store := NewDefinitionStore(filepath.Join(t.TempDir(), "indexer-definitions"))
	store.CustomDir = filepath.Join(t.TempDir(), "custom")
	store.SourceURL = srv.URL
	before := store.Generation()

	// A local helper, the way self-hosted indexers ship their file.
	local := []byte(strings.Replace(string(fixtureDef(t, "fixturejson")), "id: fixturejson", "id: myhelper", 1))
	local = []byte(strings.Replace(string(local), "https://json.fixture.test/", "http://192.168.1.20:8080/", 1))
	sum, err := store.AddCustom(local)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !sum.Custom || sum.ID != "myhelper" || !sum.Supported {
		t.Fatalf("summary = %+v", sum)
	}
	if hits.Load() != 0 {
		t.Fatal("adding a file must not download the shared list")
	}
	// It can be used before the shared list was ever downloaded.
	got, err := store.Summary(context.Background(), "myhelper")
	if err != nil || !got.Custom {
		t.Fatalf("summary before download: %+v, %v", got, err)
	}
	if !got.HasLink("http://nas.local:9117/") || got.HasLink("ftp://x/") || got.HasLink("http://user:pw@x/") {
		t.Error("an added file may point anywhere on the web, but only at a plain web address")
	}
	def, err := store.Load(context.Background(), "myhelper")
	if err != nil || def.ID != "myhelper" {
		t.Fatalf("load: %v", err)
	}
	if store.Generation() == before {
		t.Error("adding a file must change the generation, so running indexers pick it up")
	}

	// A fixed copy of a shared site wins over the downloaded one.
	fixed := []byte(strings.Replace(string(fixtureDef(t, "fixturepublic")), "name: Fixture Public", "name: Fixture Public (my copy)", 1))
	if _, err := store.AddCustom(fixed); err != nil {
		t.Fatalf("add fixed copy: %v", err)
	}
	idx, err := store.Catalogue(context.Background(), false)
	if err != nil {
		t.Fatalf("catalogue: %v", err)
	}
	count := map[string]int{}
	for _, d := range idx.Definitions {
		count[d.ID]++
		if d.ID == "fixturepublic" && (!d.Custom || d.Name != "Fixture Public (my copy)") {
			t.Errorf("the added copy should replace the shared one, got %+v", d)
		}
	}
	if count["fixturepublic"] != 1 || count["myhelper"] != 1 || count["fixtureprivate"] != 1 {
		t.Fatalf("list = %v", count)
	}
	// The cached list on disk is left alone.
	cached, _ := os.ReadFile(filepath.Join(store.Dir, "fixturepublic.yml"))
	if strings.Contains(string(cached), "my copy") {
		t.Error("the downloaded copy was overwritten")
	}

	if err := store.RemoveCustom("fixturepublic"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if d, _ := store.Summary(context.Background(), "fixturepublic"); d.Custom {
		t.Error("after removing the added copy, the shared one should be back")
	}
	if err := store.RemoveCustom("fixturepublic"); !errors.Is(err, ErrDefinitionNotFound) {
		t.Errorf("removing twice: %v", err)
	}
	if err := store.RemoveCustom("../index"); !errors.Is(err, ErrDefinitionNotFound) {
		t.Errorf("a path as id: %v", err)
	}
	if len(store.CustomDefinitions()) != 1 {
		t.Errorf("custom = %+v", store.CustomDefinitions())
	}
}

func TestCustomDefinitionsTurnAwayBadFiles(t *testing.T) {
	store := NewDefinitionStore(filepath.Join(t.TempDir(), "defs"))
	store.CustomDir = filepath.Join(t.TempDir(), "custom")
	search := "search:\n  paths: [{path: x}]\n  rows: {selector: tr}\n  fields: {title: {selector: a}}\n"
	cases := map[string]string{
		"empty":       "",
		"not yaml":    "id: [unclosed\n",
		"no id":       "name: Nothing\nlinks: [https://x.test/]\n" + search,
		"path id":     "id: ../../evil\nname: Evil\nlinks: [https://x.test/]\n" + search,
		"no links":    "id: nolinks\nname: No links\n" + search,
		"file link":   "id: filelink\nname: File\nlinks: [file:///etc/passwd]\n" + search,
		"unsupported": "id: unsup\nname: Needs XPath\nlinks: [https://u.test/]\nsearch:\n  paths: [{path: x}]\n  rows: {selector: 'tr:xpath(1)'}\n  fields: {title: {selector: a}}\n",
		"binary":      "id: bin\n\xff\xfe\x00",
		"too large":   "id: big\nname: Big\nlinks: [https://x.test/]\n" + search + "# " + strings.Repeat("x", maxCustomDefinitionBytes),
		"alias bomb":  "id: bomb\na: &a [x,x,x,x,x,x,x,x,x]\nb: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]\nc: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]\nd: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]\ne: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]\nf: &f [*e,*e,*e,*e,*e,*e,*e,*e,*e]\ng: &g [*f,*f,*f,*f,*f,*f,*f,*f,*f]\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := store.AddCustom([]byte(body)); !errors.Is(err, ErrBadCustomDefinition) {
				t.Fatalf("want a bad-file error, got %v", err)
			}
		})
	}
	if entries, _ := os.ReadDir(store.CustomDir); len(entries) != 0 {
		t.Fatalf("turned-away files were kept: %v", entries)
	}
}
