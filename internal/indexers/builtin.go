package indexers

import (
	"embed"
	"path"
	"strings"
	"sync"
)

// Definitions that ship with Mediarium, for sites the shared list doesn't
// have (Prowlarr runs some sites from its own code instead). They join the
// downloaded catalogue every time it's refreshed; when the shared list gains
// a definition with the same id, that one is used instead.
//
//go:embed builtin/*.yml
var builtinFS embed.FS

var (
	builtinOnce sync.Once
	builtinDefs map[string][]byte // id -> YAML
)

func builtinDefinitions() map[string][]byte {
	builtinOnce.Do(func() {
		builtinDefs = map[string][]byte{}
		entries, err := builtinFS.ReadDir("builtin")
		if err != nil {
			return
		}
		for _, e := range entries {
			data, err := builtinFS.ReadFile(path.Join("builtin", e.Name()))
			if err != nil {
				continue
			}
			builtinDefs[strings.TrimSuffix(e.Name(), ".yml")] = data
		}
	})
	return builtinDefs
}

// addBuiltins puts the shipped definitions into a freshly extracted set,
// leaving any the download already has.
func addBuiltins(files map[string][]byte) {
	for id, data := range builtinDefinitions() {
		if _, ok := files[id]; !ok {
			files[id] = data
		}
	}
}

// missingBuiltin reports whether a cached catalogue was made before a
// shipped definition existed, so it's refreshed to pick it up.
func missingBuiltin(idx *DefinitionIndex) bool {
	if idx == nil {
		return false
	}
	have := map[string]bool{}
	for _, d := range idx.Definitions {
		have[d.ID] = true
	}
	for id := range builtinDefinitions() {
		if !have[id] {
			return true
		}
	}
	return false
}
