package subtitles

import (
	"encoding/json"
	"sort"
	"strings"
)

// Subtitle profiles. A subtitle can be the full dialogue, the full dialogue
// with sound descriptions for the deaf and hard of hearing (SDH), or only the
// lines in a foreign language ("forced", OpenSubtitles' "foreign parts only").
// A profile says, per language, which of those to look for.
//
// Files are named the way Plex, Jellyfin and Emby read them:
// "Movie.en.srt", "Movie.en.sdh.srt", "Movie.en.forced.srt".

// Variant is which kind of subtitle a file or a search result is.
type Variant string

const (
	VariantFull   Variant = ""       // the full dialogue
	VariantSDH    Variant = "sdh"    // the full dialogue plus sound descriptions
	VariantForced Variant = "forced" // only the foreign-language lines
)

// Suffix is what goes between the language and the extension: "", ".sdh", ".forced".
func (v Variant) Suffix() string {
	if v == VariantFull {
		return ""
	}
	return "." + string(v)
}

// ValidVariant reports whether s names a variant ("" is the full one).
func ValidVariant(s string) (Variant, bool) {
	switch Variant(s) {
	case VariantFull, VariantSDH, VariantForced:
		return Variant(s), true
	}
	return VariantFull, false
}

// FullMode is which full-dialogue subtitle a language wants.
type FullMode string

const (
	FullEither FullMode = "either" // a normal one or an SDH one, whichever fits the video best (the default)
	FullNormal FullMode = "normal" // a normal one, never SDH
	FullSDH    FullMode = "sdh"    // an SDH one only
	FullNone   FullMode = "none"   // no full subtitle: only the forced track
)

// Profile is what one language wants.
type Profile struct {
	Full   FullMode `json:"full"`
	Forced bool     `json:"forced"` // also a forced track for the foreign-language parts
}

// DefaultProfile is what a language gets until someone changes it: a normal
// or SDH subtitle, nothing forced.
func DefaultProfile() Profile { return Profile{Full: FullEither} }

// Normalize fills in the default and makes the choice consistent: with no
// full subtitle wanted, the forced track is what is wanted.
func (p Profile) Normalize() Profile {
	switch p.Full {
	case FullEither, FullNormal, FullSDH, FullNone:
	default:
		p.Full = FullEither
	}
	if p.Full == FullNone {
		p.Forced = true
	}
	return p
}

// IsDefault reports whether p asks for nothing beyond the default.
func (p Profile) IsDefault() bool { return p.Normalize() == DefaultProfile() }

// Slot is one subtitle a language needs: its full dialogue or its forced track.
type Slot string

const (
	SlotFull   Slot = "full"
	SlotForced Slot = "forced"
)

// Key is how the slot is told apart in the records Mediarium keeps for a
// language: the language alone for the full subtitle (as before profiles
// existed) and "en.forced" for the forced track.
func (s Slot) Key(lang string) string {
	if s == SlotForced {
		return lang + ".forced"
	}
	return lang
}

// SplitSlotKey is the inverse of Key.
func SplitSlotKey(key string) (lang string, slot Slot) {
	if l, ok := strings.CutSuffix(key, ".forced"); ok {
		return l, SlotForced
	}
	return key, SlotFull
}

// Slots lists what the profile wants, full subtitle first.
func (p Profile) Slots() []Slot {
	p = p.Normalize()
	var out []Slot
	if p.Full != FullNone {
		out = append(out, SlotFull)
	}
	if p.Forced {
		out = append(out, SlotForced)
	}
	return out
}

// Have is which kinds of subtitle exist for one language.
type Have struct {
	Full   bool // a subtitle with no marker in its name or track
	SDH    bool
	Forced bool
}

// Satisfies reports whether what exists fills the slot under the profile.
func (p Profile) Satisfies(h Have, s Slot) bool {
	p = p.Normalize()
	if s == SlotForced {
		return h.Forced
	}
	switch p.Full {
	case FullNormal:
		return h.Full
	case FullSDH:
		return h.SDH
	case FullNone:
		return true
	}
	return h.Full || h.SDH
}

// Missing lists the slots the profile wants that h does not fill.
func (p Profile) Missing(h Have) []Slot {
	var out []Slot
	for _, s := range p.Slots() {
		if !p.Satisfies(h, s) {
			out = append(out, s)
		}
	}
	return out
}

// HaveMap says what exists, per language (keys lower case).
type HaveMap map[string]Have

// Add notes a subtitle of the given variant in a language.
func (m HaveMap) Add(lang string, v Variant) {
	lang = strings.ToLower(lang)
	if lang == "" {
		return
	}
	h := m[lang]
	switch v {
	case VariantSDH:
		h.SDH = true
	case VariantForced:
		h.Forced = true
	default:
		h.Full = true
	}
	m[lang] = h
}

// Merge adds everything in other.
func (m HaveMap) Merge(other HaveMap) {
	for l, o := range other {
		h := m[l]
		h.Full = h.Full || o.Full
		h.SDH = h.SDH || o.SDH
		h.Forced = h.Forced || o.Forced
		m[l] = h
	}
}

// For is what exists for a wanted language. A file that names only the base
// language ("pt") counts for a regional variant ("pt-BR"), as with
// LanguageSatisfied.
func (m HaveMap) For(wanted string) Have {
	w := strings.ToLower(wanted)
	h := m[w]
	if b := baseLanguage(w); b != w {
		o := m[b]
		h.Full = h.Full || o.Full
		h.SDH = h.SDH || o.SDH
		h.Forced = h.Forced || o.Forced
	}
	return h
}

// Profiles are the choices made per language (keys lower case).
type Profiles map[string]Profile

// For is the profile of a language: its own, else the default.
func (ps Profiles) For(lang string) Profile {
	if p, ok := ps[strings.ToLower(strings.TrimSpace(lang))]; ok {
		return p.Normalize()
	}
	return DefaultProfile()
}

// ParseProfiles reads the stored setting, a JSON object such as
// {"en":{"full":"normal","forced":true}}. Anything it can't read is left out,
// so a damaged value means the defaults.
func ParseProfiles(raw string) Profiles {
	out := Profiles{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	var in map[string]Profile
	if json.Unmarshal([]byte(raw), &in) != nil {
		return out
	}
	for lang, p := range in {
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang == "" || len(lang) > 12 {
			continue
		}
		if p = p.Normalize(); !p.IsDefault() {
			out[lang] = p
		}
	}
	return out
}

// Encode is the stored form: only languages with a choice other than the
// default, "" when there are none.
func (ps Profiles) Encode() string {
	keep := map[string]Profile{}
	for lang, p := range ps {
		if p = p.Normalize(); !p.IsDefault() {
			keep[strings.ToLower(lang)] = p
		}
	}
	if len(keep) == 0 {
		return ""
	}
	b, err := json.Marshal(keep) // map keys are sorted, so the value is stable
	if err != nil {
		return ""
	}
	return string(b)
}

// Languages lists the languages that have a choice, sorted.
func (ps Profiles) Languages() []string {
	out := make([]string, 0, len(ps))
	for l := range ps {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Variant tells what kind of subtitle a search result is. One marked both
// "foreign parts only" and "hearing impaired" is a full subtitle with sound
// descriptions that was tagged wrong, not a forced track (uploads are often
// ticked that way), so it counts as SDH.
func (r Result) Variant() Variant {
	switch {
	case r.HearingImpaired:
		return VariantSDH
	case r.ForeignPartsOnly:
		return VariantForced
	}
	return VariantFull
}

// Accepts reports whether a result can fill the slot under the profile.
func (p Profile) Accepts(s Slot, r Result) bool {
	p = p.Normalize()
	v := r.Variant()
	if s == SlotForced {
		return v == VariantForced
	}
	switch v {
	case VariantForced:
		return false
	case VariantSDH:
		return p.Full == FullEither || p.Full == FullSDH
	}
	return p.Full == FullEither || p.Full == FullNormal
}

// Filter keeps the results that can fill the slot.
func (p Profile) Filter(s Slot, results []Result) []Result {
	var out []Result
	for _, r := range results {
		if p.Accepts(s, r) {
			out = append(out, r)
		}
	}
	return out
}

// Apply sets what the search asks the provider for, for this slot. The
// default (a normal or SDH subtitle) asks for nothing special, as before
// profiles existed; results are filtered again after they come back, since
// not every source can filter.
func (p Profile) Apply(s Slot, q Query) Query {
	p = p.Normalize()
	q.Forced = s == SlotForced
	if s == SlotFull {
		q.Full = p.Full
	}
	return q
}

var negations = map[string]bool{"no": true, "non": true, "not": true, "without": true, "removed": true, "nonsdh": true}

// ClassifyName reads the words in a release or subtitle name for the two
// markers some sources only give that way. "hi" alone is not taken as
// "hearing impaired" (it is Hindi's code and a common syllable), and "forced"
// or "foreign parts" as the very first word is taken as part of a title
// ("Forced Entry", "Foreign Correspondent") rather than a marker.
func ClassifyName(name string) (hearingImpaired, foreignPartsOnly bool) {
	tokens := tokenize(name)
	for i, t := range tokens {
		if i > 0 && negations[tokens[i-1]] {
			continue // "not forced", "non-SDH", "removed SDH"
		}
		switch {
		case t == "forced" && i > 0:
			foreignPartsOnly = true
		case t == "foreign" && i > 0 && i+1 < len(tokens) && (tokens[i+1] == "parts" || tokens[i+1] == "part"):
			foreignPartsOnly = true
		case t == "foreignparts" && i > 0:
			foreignPartsOnly = true
		case t == "sdh", t == "cc":
			hearingImpaired = true
		case t == "hearing" && i+1 < len(tokens) && tokens[i+1] == "impaired":
			hearingImpaired = true
		case t == "closed" && i+1 < len(tokens) && tokens[i+1] == "captions":
			hearingImpaired = true
		}
	}
	return hearingImpaired, foreignPartsOnly
}
