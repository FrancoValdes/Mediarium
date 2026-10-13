package indexers

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"path"
	"sort"
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

// builtinFingerprint identifies the shipped definitions' exact contents, so a
// cached catalogue made with older ones can be told from one made with these.
func builtinFingerprint() string {
	defs := builtinDefinitions()
	ids := make([]string, 0, len(defs))
	for id := range defs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{0})
		h.Write(defs[id])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// missingBuiltin reports whether a cached catalogue was made before a
// shipped definition existed, or with an older copy of one (a fix to a
// shipped definition only reaches a cached catalogue this way), so it's
// refreshed to pick them up.
func missingBuiltin(idx *DefinitionIndex) bool {
	if idx == nil {
		return false
	}
	if idx.Builtins != builtinFingerprint() {
		return true
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
