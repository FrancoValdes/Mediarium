package quality

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TRaSH Guides quality profiles: what a profile JSON from
// docs/json/{radarr,sonarr}/quality-profiles holds, and how it maps onto a
// Mediarium profile.

// TrashProfile is a TRaSH Guides quality profile.
type TrashProfile struct {
	TrashID           string            `json:"trash_id"`
	Name              string            `json:"name"`
	Description       string            `json:"trash_description"`
	ScoreSet          string            `json:"trash_score_set"`
	UpgradeAllowed    bool              `json:"upgradeAllowed"`
	Cutoff            string            `json:"cutoff"`
	MinFormatScore    int               `json:"minFormatScore"`
	CutoffFormatScore int               `json:"cutoffFormatScore"`
	Items             []TrashItem       `json:"items"`
	FormatItems       map[string]string `json:"formatItems"` // format name -> trash id
}

// TrashItem is a quality, or a group of them, in a TRaSH profile.
type TrashItem struct {
	Name    string   `json:"name"`
	Allowed bool     `json:"allowed"`
	Items   []string `json:"items"`
}

// ParseTrashProfile reads a TRaSH Guides quality profile.
func ParseTrashProfile(data []byte) (TrashProfile, error) {
	var tp TrashProfile
	if err := json.Unmarshal(data, &tp); err != nil {
		return TrashProfile{}, fmt.Errorf("that isn't a TRaSH quality profile: %w", err)
	}
	if strings.TrimSpace(tp.Name) == "" || len(tp.Items) == 0 {
		return TrashProfile{}, fmt.Errorf("that isn't a TRaSH quality profile: it has no name or no qualities")
	}
	return tp, nil
}

// trashQualities maps Radarr and Sonarr quality names onto Mediarium's tiers.
// Mediarium doesn't tell WEBRip from WEBDL, and has no Raw-HD or BR-DISK
// tier; those have no entry and are left out.
var trashQualities = map[string]Tier{
	"Unknown":            TierUnknown,
	"WORKPRINT":          TierPreRelease,
	"CAM":                TierPreRelease,
	"TELESYNC":           TierPreRelease,
	"TELECINE":           TierPreRelease,
	"REGIONAL":           TierPreRelease,
	"DVDSCR":             TierPreRelease,
	"SDTV":               TierSDTV,
	"DVD":                TierDVD,
	"DVD-R":              TierDVD,
	"Bluray-480p":        TierDVD,
	"Bluray-576p":        TierDVD,
	"WEBDL-480p":         TierWebDL480p,
	"WEBRip-480p":        TierWebDL480p,
	"HDTV-720p":          TierHDTV720p,
	"WEBDL-720p":         TierWebDL720p,
	"WEBRip-720p":        TierWebDL720p,
	"Bluray-720p":        TierBluray720p,
	"HDTV-1080p":         TierHDTV1080p,
	"WEBDL-1080p":        TierWebDL1080p,
	"WEBRip-1080p":       TierWebDL1080p,
	"Bluray-1080p":       TierBluray1080p,
	"Remux-1080p":        TierRemux1080p,
	"Bluray-1080p Remux": TierRemux1080p,
	"HDTV-2160p":         TierHDTV2160p,
	"WEBDL-2160p":        TierWebDL2160p,
	"WEBRip-2160p":       TierWebDL2160p,
	"Bluray-2160p":       TierBluray2160p,
	"Remux-2160p":        TierRemux2160p,
	"Bluray-2160p Remux": TierRemux2160p,
}

// itemTiers is the tiers a quality or a group of them covers.
func itemTiers(it TrashItem) []Tier {
	names := it.Items
	if len(names) == 0 {
		names = []string{it.Name}
	}
	var out []Tier
	for _, n := range names {
		if t, ok := trashQualities[strings.TrimSpace(n)]; ok && t != TierUnknown {
			out = append(out, t)
		}
	}
	return out
}

// Tiers is the profile's allowed qualities and its cutoff as Mediarium
// tiers. A cutoff naming a group is the best tier in that group.
func (tp TrashProfile) Tiers() (allowed []Tier, cutoff Tier, err error) {
	seen := map[Tier]bool{}
	for _, it := range tp.Items {
		if !it.Allowed {
			continue
		}
		for _, t := range itemTiers(it) {
			seen[t] = true
		}
	}
	for _, t := range AllTiers() {
		if seen[t] {
			allowed = append(allowed, t)
		}
	}
	if len(allowed) == 0 {
		return nil, "", fmt.Errorf("%w: none of the qualities in %q exist in Mediarium", ErrInvalid, tp.Name)
	}
	for _, it := range tp.Items {
		if it.Name != tp.Cutoff {
			continue
		}
		for _, t := range itemTiers(it) {
			if seen[t] && (cutoff == "" || Rank(t) > Rank(cutoff)) {
				cutoff = t
			}
		}
	}
	if cutoff == "" {
		cutoff = allowed[len(allowed)-1]
	}
	return allowed, cutoff, nil
}

// FormatTrashIDs is the TRaSH ids of the profile's formats, sorted.
func (tp TrashProfile) FormatTrashIDs() []string {
	out := make([]string, 0, len(tp.FormatItems))
	for _, id := range tp.FormatItems {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ScoreFor is the score TRaSH gives a format in this profile: the score for
// the profile's score set when the format has one, otherwise its default.
func (tp TrashProfile) ScoreFor(f ImportedFormat) int {
	if tp.ScoreSet != "" {
		if s, ok := f.Scores[tp.ScoreSet]; ok {
			return s
		}
	}
	return f.DefaultScore
}
