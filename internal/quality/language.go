package quality

import (
	"strings"

	"github.com/rdborg/mediarium/internal/parser"
)

// DefaultLanguage is the audio language wanted when none has been chosen.
const DefaultLanguage = parser.English

// LanguageFit says how well a release's audio language suits the language
// the person wants.
type LanguageFit struct {
	// Rank orders acceptable releases, higher is better: 3 is the wanted
	// language alone (or nothing tagged when English is wanted), 2 is the
	// wanted language together with another one, 1 is a multi-language
	// release that does not say it holds the wanted language. Rejected
	// releases have rank 0.
	Rank int
	// Accepted is false for a release that is clearly in another language
	// only (ITALIAN, GERMAN, FRENCH... with nothing that includes the wanted
	// language). Automation never takes such a release; a manual grab is
	// still allowed.
	Accepted bool
	// Label names the languages the release says it has, in words for the
	// release list: "Italian + English", "Multi", "English". Empty when the
	// release carries no language tag at all.
	Label string
}

// OriginalLanguage is the word in a list of wanted languages that stands for
// the language the film or show was made in; each title fills it in.
const OriginalLanguage = "Original"

// ParseLanguages reads the list of wanted audio languages: names separated by
// commas, best first. Blanks and repeats are dropped.
func ParseLanguages(list string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(list, ",") {
		l = strings.TrimSpace(l)
		if l == "" || seen[strings.ToLower(l)] {
			continue
		}
		seen[strings.ToLower(l)] = true
		out = append(out, l)
	}
	return out
}

// ResolveOriginal puts a title's own original language (a name such as
// "Japanese", or "" when it isn't known) where the list says "Original". An
// unknown original language leaves that entry out; a list left with nothing
// accepts every language.
func ResolveOriginal(list, original string) string {
	var out []string
	for _, l := range ParseLanguages(list) {
		if strings.EqualFold(l, OriginalLanguage) {
			if original == "" {
				continue
			}
			l = original
		}
		out = append(out, l)
	}
	return strings.Join(ParseLanguages(strings.Join(out, ",")), ",")
}

// languageOfISO maps TMDB's two-letter original_language to the names
// releases use.
var languageOfISO = map[string]string{
	"en": parser.English, "it": "Italian", "de": "German", "fr": "French", "es": "Spanish", "ru": "Russian",
	"pt": "Portuguese", "nl": "Dutch", "pl": "Polish", "sv": "Swedish", "da": "Danish", "no": "Norwegian", "nb": "Norwegian",
	"fi": "Finnish", "tr": "Turkish", "el": "Greek", "hu": "Hungarian", "cs": "Czech", "ro": "Romanian", "uk": "Ukrainian",
	"ar": "Arabic", "he": "Hebrew", "hi": "Hindi", "ta": "Tamil", "te": "Telugu", "ja": "Japanese", "ko": "Korean",
	"zh": "Chinese", "cn": "Chinese", "th": "Thai", "vi": "Vietnamese",
}

// LanguageName is the release-name language for a TMDB language code, or ""
// when there is no such name.
func LanguageName(iso string) string {
	return languageOfISO[strings.ToLower(strings.TrimSpace(iso))]
}

// FitLanguage judges a release for a person who wants preferred audio: one
// language, or several separated by commas with the best first. An empty
// preferred language accepts everything. A release with no language tag is
// taken to be in English, the language nearly everything untagged is in.
//
// With several languages a release is accepted when it suits any of them, and
// one that suits an earlier language ranks above one that only suits a later
// language.
func FitLanguage(r parser.Release, preferred string) LanguageFit {
	langs := ParseLanguages(preferred)
	if len(langs) <= 1 {
		one := ""
		if len(langs) == 1 && !strings.EqualFold(langs[0], OriginalLanguage) {
			one = langs[0]
		}
		return fitOneLanguage(r, one)
	}
	for i, l := range langs {
		if strings.EqualFold(l, OriginalLanguage) {
			continue
		}
		if fit := fitOneLanguage(r, l); fit.Accepted {
			// Earlier languages count for more than how well the tag fits.
			fit.Rank += (len(langs) - i) * 4
			return fit
		}
	}
	fit := fitOneLanguage(r, langs[0])
	fit.Rank, fit.Accepted = 0, false
	return fit
}

func fitOneLanguage(r parser.Release, preferred string) LanguageFit {
	fit := LanguageFit{Accepted: true, Rank: 3, Label: languageLabel(r)}
	if preferred == "" {
		return fit
	}
	has, other := false, false
	for _, l := range r.Languages {
		if strings.EqualFold(l, preferred) {
			has = true
		} else {
			other = true
		}
	}
	untagged := len(r.Languages) == 0
	assumedEnglish := strings.EqualFold(preferred, parser.English)

	switch {
	case r.Multi && has:
		fit.Rank = 2
	case r.Multi:
		// Several audio tracks, and the original is nearly always one of
		// them. Fine, but behind a release that says so.
		fit.Rank = 1
	case untagged && assumedEnglish:
		fit.Rank = 3
	case untagged:
		fit.Rank = 2 // taken to be English, not the wanted language
	case has && !other:
		fit.Rank = 3
	case has:
		fit.Rank = 2
	case !assumedEnglish && onlyEnglish(r.Languages):
		fit.Rank = 2 // tagged English, and English is what untagged releases are assumed to be
	default:
		fit.Rank, fit.Accepted = 0, false
	}
	return fit
}

func onlyEnglish(langs []string) bool {
	return len(langs) == 1 && strings.EqualFold(langs[0], parser.English)
}

// languageLabel words the language tags of a release.
func languageLabel(r parser.Release) string {
	if len(r.Languages) == 0 {
		if r.Multi {
			return "Multi"
		}
		return ""
	}
	label := strings.Join(r.Languages, " + ")
	if r.Multi {
		label += " (multi)"
	}
	return label
}

// LanguageRank is FitLanguage(r, p.Language).Rank.
func (p Profile) LanguageRank(r parser.Release) int {
	return FitLanguage(r, p.Language).Rank
}

// LanguageOK reports whether a release's audio language suits the language
// this profile was loaded with (Language). It is true when none is set.
func (p Profile) LanguageOK(r parser.Release) bool {
	return FitLanguage(r, p.Language).Accepted
}

// LanguageReason explains, in plain words, why a release was turned down for
// its language, or "" when it was not.
func (p Profile) LanguageReason(r parser.Release) string {
	fit := FitLanguage(r, p.Language)
	if fit.Accepted {
		return ""
	}
	return strings.Join(r.Languages, " and ") + " audio, not " + strings.Join(ParseLanguages(p.Language), " or ")
}
