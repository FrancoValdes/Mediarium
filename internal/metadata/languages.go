package metadata

// Language is a language TMDB can give titles, plots, genres and posters in.
// Name is written in the language itself, the way a speaker looks for it.
type Language struct {
	Tag  string `json:"tag"`  // a TMDB language tag, like "de-DE"
	Name string `json:"name"` // "Deutsch"
}

// InfoLanguages are the languages offered for what is read on screen. TMDB
// falls back to English for anything it has no translation of, so a title
// without a Hungarian description just shows the English one.
var InfoLanguages = []Language{
	{DefaultLanguage, "English"},
	{"es-ES", "Español (España)"},
	{"es-MX", "Español (México)"},
	{"de-DE", "Deutsch"},
	{"fr-FR", "Français"},
	{"fr-CA", "Français (Canada)"},
	{"pt-BR", "Português (Brasil)"},
	{"pt-PT", "Português (Portugal)"},
	{"it-IT", "Italiano"},
	{"nl-NL", "Nederlands"},
	{"pl-PL", "Polski"},
	{"uk-UA", "Українська"},
	{"ru-RU", "Русский"},
	{"cs-CZ", "Čeština"},
	{"sk-SK", "Slovenčina"},
	{"hu-HU", "Magyar"},
	{"ro-RO", "Română"},
	{"bg-BG", "Български"},
	{"el-GR", "Ελληνικά"},
	{"tr-TR", "Türkçe"},
	{"sv-SE", "Svenska"},
	{"da-DK", "Dansk"},
	{"nb-NO", "Norsk"},
	{"fi-FI", "Suomi"},
	{"he-IL", "עברית"},
	{"ar-SA", "العربية"},
	{"hi-IN", "हिन्दी"},
	{"id-ID", "Bahasa Indonesia"},
	{"vi-VN", "Tiếng Việt"},
	{"th-TH", "ไทย"},
	{"ja-JP", "日本語"},
	{"ko-KR", "한국어"},
	{"zh-CN", "中文 (简体)"},
	{"zh-TW", "中文 (繁體)"},
}

// IsInfoLanguage reports whether tag is one of InfoLanguages.
func IsInfoLanguage(tag string) bool {
	for _, l := range InfoLanguages {
		if l.Tag == tag {
			return true
		}
	}
	return false
}
