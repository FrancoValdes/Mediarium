package api

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// nonNil makes a list that is always sent as [] and never null.
func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// checkExtraFolders tidies and checks the folders added next to a kind's main
// folder. Each has to be a full path of its own: not the same folder as, or
// inside or around, the main folders, Mediarium's own settings folder, or
// another of the list. It returns the cleaned list, or a message.
func (s *Server) checkExtraFolders(list []string, main, moviesMain, tvMain, what string) ([]string, string) {
	label := "A folder for " + what
	var out []string
	for _, raw := range list {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) && !strings.HasPrefix(p, "/") {
			return nil, label + " has to be a full path, starting with /"
		}
		if len(p) > maxPathLen {
			return nil, label + " is too long."
		}
		if m := s.checkLibraryFolder(p, label); m != "" {
			return nil, m
		}
		c := filepath.Clean(p)
		if slices.Contains(out, c) {
			continue // the same folder twice is one folder
		}
		for _, other := range append([]string{moviesMain, tvMain, main}, out...) {
			if other == "" {
				continue
			}
			if overlaps(c, filepath.Clean(other)) {
				return nil, fmt.Sprintf("%s (%s) has to be separate from %s, not the same folder or one inside it.", label, c, other)
			}
		}
		out = append(out, c)
	}
	return out, ""
}

// overlaps reports whether two folders are the same or one holds the other.
func overlaps(a, b string) bool {
	sep := string(filepath.Separator)
	return a == b || strings.HasPrefix(a+sep, b+sep) || strings.HasPrefix(b+sep, a+sep)
}
