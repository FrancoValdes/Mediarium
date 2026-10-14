package configfile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// A quality profile points at other profiles (fallback) and at custom formats
// (formatScores) by their numbers in this Mediarium's database, which mean
// something else, or nothing, on another install. The file uses their names
// instead: fallback is a list of profile names, and each format score is
// {format: <name>, score: <number>}. The names are turned back into numbers
// when the file is applied.

// profileRefs maps the names of profiles and custom formats to their ids and back.
type profileRefs struct {
	profileID   map[string]int64 // lower-case name -> id
	profileName map[int64]string
	formatID    map[string]int64
	formatName  map[int64]string
}

// loadProfileRefs reads the profiles and the custom formats Mediarium has.
// profiles may be passed when already read.
func loadProfileRefs(ctx context.Context, c *Client, profiles []map[string]any) (*profileRefs, error) {
	if profiles == nil {
		ls, _ := listByName("qualityProfiles")
		var err error
		if profiles, err = currentList(ctx, c, ls); err != nil {
			return nil, err
		}
	}
	r := &profileRefs{profileID: map[string]int64{}, profileName: map[int64]string{}, formatID: map[string]int64{}, formatName: map[int64]string{}}
	for _, p := range profiles {
		id, ok := idOf(p)
		name, _ := p["name"].(string)
		if ok && name != "" {
			r.profileID[strings.ToLower(strings.TrimSpace(name))] = id
			r.profileName[id] = name
		}
	}
	data, err := c.doRaw(ctx, http.MethodGet, "/api/custom-formats", nil)
	if err != nil {
		return nil, fmt.Errorf("read the custom formats: %w", err)
	}
	var formats struct {
		Formats []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"formats"`
	}
	if err := json.Unmarshal(data, &formats); err != nil {
		return nil, fmt.Errorf("Mediarium's answer for /api/custom-formats wasn't what was expected")
	}
	for _, f := range formats.Formats {
		r.formatID[strings.ToLower(strings.TrimSpace(f.Name))] = f.ID
		r.formatName[f.ID] = f.Name
	}
	return r, nil
}

// toNames is a profile as Mediarium shows it with the ids replaced by names.
// An id that leads nowhere (a profile or format deleted since) is left out.
func (r *profileRefs) toNames(p map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range p {
		out[k] = v
	}
	if fb, ok := p["fallback"].([]any); ok {
		names := []any{}
		for _, x := range fb {
			if id, ok := x.(float64); ok {
				if n, ok := r.profileName[int64(id)]; ok {
					names = append(names, n)
				}
			}
		}
		out["fallback"] = names
	}
	if fs, ok := p["formatScores"].([]any); ok {
		scores := []any{}
		for _, x := range fs {
			m, _ := x.(map[string]any)
			id, _ := m["formatId"].(float64)
			if n, ok := r.formatName[int64(id)]; ok {
				scores = append(scores, map[string]any{"format": n, "score": m["score"]})
			}
		}
		out["formatScores"] = sortScores(scores)
	}
	return out
}

// sortScores puts format scores in the order of the formats' names, so the
// order they are written in never shows as a change.
func sortScores(scores []any) []any {
	sort.SliceStable(scores, func(i, j int) bool {
		a, _ := scores[i].(map[string]any)
		b, _ := scores[j].(map[string]any)
		an, _ := a["format"].(string)
		bn, _ := b["format"].(string)
		return strings.ToLower(an) < strings.ToLower(bn)
	})
	return scores
}

// checkNames checks the fallback and formatScores of a profile in the file
// and writes the names as Mediarium spells them. inFile are the (lower-case)
// names of the profiles in the file, which a fallback may name before they
// exist.
func (r *profileRefs) checkNames(item map[string]any, inFile map[string]bool) error {
	if v, ok := item["fallback"]; ok && v != nil {
		list, ok := v.([]any)
		if !ok {
			return fmt.Errorf("fallback must be a list of profile names")
		}
		names := []any{}
		for _, x := range list {
			n, ok := x.(string)
			if !ok || strings.TrimSpace(n) == "" {
				return fmt.Errorf("fallback must list profiles by name, not %v", show(x))
			}
			key := strings.ToLower(strings.TrimSpace(n))
			if id, ok := r.profileID[key]; ok {
				n = r.profileName[id]
			} else if !inFile[key] {
				return fmt.Errorf("the fallback profile %q isn't in Mediarium or in this file", n)
			}
			names = append(names, n)
		}
		item["fallback"] = names
	}
	if v, ok := item["formatScores"]; ok && v != nil {
		list, ok := v.([]any)
		if !ok {
			return fmt.Errorf("formatScores must be a list of format: name, score: number lines")
		}
		scores := []any{}
		for _, x := range list {
			m, ok := x.(map[string]any)
			if !ok {
				return fmt.Errorf("each of formatScores must be format: name, score: number")
			}
			for k := range m {
				if k != "format" && k != "score" {
					return fmt.Errorf("formatScores has %q: write each score as format: name, score: number", k)
				}
			}
			n, _ := m["format"].(string)
			if strings.TrimSpace(n) == "" {
				return fmt.Errorf("each of formatScores needs the custom format's name as format:")
			}
			id, ok := r.formatID[strings.ToLower(strings.TrimSpace(n))]
			if !ok {
				return fmt.Errorf("the custom format %q isn't in Mediarium", n)
			}
			scores = append(scores, map[string]any{"format": r.formatName[id], "score": m["score"]})
		}
		item["formatScores"] = sortScores(scores)
	}
	return nil
}

// toIDs turns the names in a profile about to be sent back into ids. It reads
// the profiles again, since one this file added a moment ago may be named.
func toIDs(ctx context.Context, c *Client, body map[string]any) error {
	_, hasFB := body["fallback"]
	_, hasFS := body["formatScores"]
	if !hasFB && !hasFS {
		return nil
	}
	r, err := loadProfileRefs(ctx, c, nil)
	if err != nil {
		return err
	}
	if list, ok := body["fallback"].([]any); ok {
		ids := []int64{}
		for _, x := range list {
			n, _ := x.(string)
			id, ok := r.profileID[strings.ToLower(strings.TrimSpace(n))]
			if !ok {
				return fmt.Errorf("the fallback profile %q isn't in Mediarium (put it before the profiles that fall back to it)", n)
			}
			ids = append(ids, id)
		}
		body["fallback"] = ids
	}
	if list, ok := body["formatScores"].([]any); ok {
		scores := []map[string]any{}
		for _, x := range list {
			m, _ := x.(map[string]any)
			n, _ := m["format"].(string)
			id, ok := r.formatID[strings.ToLower(strings.TrimSpace(n))]
			if !ok {
				return fmt.Errorf("the custom format %q isn't in Mediarium", n)
			}
			scores = append(scores, map[string]any{"formatId": id, "score": m["score"]})
		}
		body["formatScores"] = scores
	}
	return nil
}
