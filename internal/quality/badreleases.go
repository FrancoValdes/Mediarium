package quality

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rdborg/mediarium/internal/parser"
)

// "Skip bad releases": a short built-in list of releases nobody wants, on in
// every profile unless switched off, so good downloads need no custom formats
// or scores. It only leaves out what is broken or misleading for watching at
// home; it never ranks codecs, audio or sources against each other.

var badReleaseRules = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`(?i)\b(BDMV|BR[ ._-]?DISK|BD(25|50|66|100)|COMPLETE[ ._-]+(UHD[ ._-]+)?BLU[ ._-]?RAY)\b`), "is a whole Blu-ray disc, which media players can't play as a file"},
	// A 3D release carries how its two pictures are packed (SBS, OU, MVC);
	// "3D" alone is often just part of a title.
	{regexp.MustCompile(`(?i)\b(H[ ._-]?SBS|H[ ._-]?OU|HALF[ ._-]?(SBS|OU)|MVC|3D[ ._-]+(SBS|OU|BLU[ ._-]?RAY))\b`), "is a 3D release"},
	{regexp.MustCompile(`(?i)\b(AI[ ._-]?)?UPSCALE[DS]?\b`), "is upscaled from a lower resolution"},
	// Some groups only write "Ai" after the resolution and leave out the word upscale.
	// Anchored to the resolution, so a title like "A.I. Artificial Intelligence" is safe.
	{regexp.MustCompile(`(?i)\b(2160p|1080p|720p|4K|UHD)[ ._-]AI\b`), "is upscaled from a lower resolution"},
	{regexp.MustCompile(`(?i)\b(HC|HARDSUBS?|HARDCODED|KORSUBS?)\b`), "has subtitles burned into the picture"},
}

// lowQualityGroups re-encode releases down to very small files, often with
// poor picture and audio.
var lowQualityGroups = map[string]bool{}

func init() {
	for _, g := range []string{
		"aXXo", "CrEwSaDe", "d3g", "DNL", "FaNGDiNG0", "HDTime", "iPlanet", "KiNGDOM", "mHD", "mSD", "nHD", "nikt0",
		"nSD", "NhaNc3", "PRODJi", "RDN", "SANTi", "STUTTERSHIT", "TERMiNAL", "ViSION", "WAF", "x0r", "YIFY", "YTS",
		"YTS.MX", "YTS.AM", "YTS.LT",
	} {
		lowQualityGroups[strings.ToLower(g)] = true
	}
}

// BadRelease says why a release title is one "Skip bad releases" leaves out
// ("" when it isn't).
func BadRelease(title string) string {
	for _, rule := range badReleaseRules {
		if rule.re.MatchString(title) {
			return rule.why
		}
	}
	r := parser.Parse(title)
	if g := strings.ToLower(r.Group); g != "" && lowQualityGroups[g] {
		return fmt.Sprintf("comes from %s, a group known for low-quality re-encodes", r.Group)
	}
	low := strings.ToLower(title)
	for _, tag := range []string{"[yts", "yts.mx", "yts.am", "yts.lt", "[yify"} {
		if strings.Contains(low, tag) {
			return "comes from YTS/YIFY, known for low-quality re-encodes"
		}
	}
	return ""
}

var fixedRelease = regexp.MustCompile(`(?i)\b(PROPER|REPACK|RERIP|REAL)\b`)

// fixBonus is what a PROPER or REPACK (a release fixed by its group) adds to
// the score, so it wins over the broken one at the same quality.
const fixBonus = 5

func (p Profile) skipBadReason(title string) string {
	if !p.SkipBadReleases {
		return ""
	}
	if why := BadRelease(title); why != "" {
		return fmt.Sprintf("%s, which \"Skip bad releases\" leaves out", why)
	}
	return ""
}

func (p Profile) fixScore(title string) int {
	if p.SkipBadReleases && fixedRelease.MatchString(title) {
		return fixBonus
	}
	return 0
}
