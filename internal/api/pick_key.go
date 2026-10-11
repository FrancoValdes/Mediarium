package api

import (
	"github.com/rdborg/mediarium/internal/indexers"
	"github.com/rdborg/mediarium/internal/parser"
	"github.com/rdborg/mediarium/internal/quality"
)

// pickKey is what automation compares releases by when choosing the best one:
// the language first (a release with no language tag or only English before
// one that adds another language, whatever their quality), then the quality
// tier, then the profile's preferred-term score. Equal keys are settled by
// preferOver (indexer priority, protocol, then a cached release).
//
// cached sits between the tier and the score, and is only ever non-zero when
// the person allowed "a lower score for a cached release": a cached release
// then beats an uncached one of the same language and tier whatever the score.
// It never lifts a release over a better language or tier.
type pickKey struct {
	language, tier, cached, score int
}

// above reports whether k beats other.
func (k pickKey) above(other pickKey) bool {
	if k.language != other.language {
		return k.language > other.language
	}
	if k.tier != other.tier {
		return k.tier > other.tier
	}
	if k.cached != other.cached {
		return k.cached > other.cached
	}
	return k.score > other.score
}

// keyFor is the pickKey of a release that profile accepts.
func keyFor(profile quality.Profile, rel parser.Release, res *indexers.Result) pickKey {
	k := pickKey{language: profile.LanguageRank(rel), tier: quality.Rank(quality.Classify(rel)), score: profile.Score(res.Title)}
	if res.Cached && cachedOverScore.Load() {
		k.cached = 1
	}
	return k
}
