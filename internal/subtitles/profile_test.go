package subtitles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestProfileSatisfiesAndMissing(t *testing.T) {
	tests := []struct {
		name string
		p    Profile
		have Have
		want []Slot
	}{
		{"default wants a normal subtitle", Profile{}, Have{}, []Slot{SlotFull}},
		{"default is met by a normal one", Profile{}, Have{Full: true}, nil},
		{"default is met by an SDH one", Profile{}, Have{SDH: true}, nil},
		{"default ignores a forced track", Profile{}, Have{Forced: true}, []Slot{SlotFull}},
		{"normal only: SDH does not count", Profile{Full: FullNormal}, Have{SDH: true}, []Slot{SlotFull}},
		{"normal only is met by a normal one", Profile{Full: FullNormal}, Have{Full: true}, nil},
		{"SDH only: a normal one does not count", Profile{Full: FullSDH}, Have{Full: true}, []Slot{SlotFull}},
		{"SDH only is met by SDH", Profile{Full: FullSDH}, Have{SDH: true}, nil},
		{"forced only wants only the forced track", Profile{Full: FullNone}, Have{Full: true}, []Slot{SlotForced}},
		{"forced only is met by a forced track", Profile{Full: FullNone}, Have{Forced: true}, nil},
		{"normal plus forced, nothing there", Profile{Full: FullNormal, Forced: true}, Have{}, []Slot{SlotFull, SlotForced}},
		{"normal plus forced, only the full one", Profile{Full: FullNormal, Forced: true}, Have{Full: true}, []Slot{SlotForced}},
		{"normal plus forced, only the forced one", Profile{Full: FullNormal, Forced: true}, Have{Forced: true}, []Slot{SlotFull}},
		{"either plus forced, SDH and forced there", Profile{Forced: true}, Have{SDH: true, Forced: true}, nil},
		{"an unknown mode is the default", Profile{Full: "whatever"}, Have{SDH: true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Missing(tt.have); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Missing = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProfileAcceptsResults(t *testing.T) {
	plain := Result{}
	sdh := Result{HearingImpaired: true}
	forced := Result{ForeignPartsOnly: true}
	both := Result{HearingImpaired: true, ForeignPartsOnly: true}
	tests := []struct {
		name   string
		p      Profile
		slot   Slot
		result Result
		want   bool
	}{
		{"default takes a plain one", Profile{}, SlotFull, plain, true},
		{"default takes an SDH one", Profile{}, SlotFull, sdh, true},
		{"default never takes a forced one as the full subtitle", Profile{}, SlotFull, forced, false},
		{"normal only refuses SDH", Profile{Full: FullNormal}, SlotFull, sdh, false},
		{"normal only takes plain", Profile{Full: FullNormal}, SlotFull, plain, true},
		{"SDH only takes SDH", Profile{Full: FullSDH}, SlotFull, sdh, true},
		{"SDH only refuses plain", Profile{Full: FullSDH}, SlotFull, plain, false},
		{"the forced slot takes forced", Profile{Forced: true}, SlotForced, forced, true},
		{"the forced slot refuses a plain one", Profile{Forced: true}, SlotForced, plain, false},
		{"the forced slot refuses an SDH one", Profile{Forced: true}, SlotForced, sdh, false},
		// Uploads ticked both boxes are full subtitles with sound descriptions.
		{"marked both counts as SDH, not forced", Profile{Forced: true}, SlotForced, both, false},
		{"marked both is taken as SDH", Profile{}, SlotFull, both, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Accepts(tt.slot, tt.result); got != tt.want {
				t.Fatalf("Accepts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSlotKeysRoundTrip(t *testing.T) {
	for _, tt := range []struct {
		lang string
		slot Slot
		key  string
	}{
		{"en", SlotFull, "en"},
		{"pt-BR", SlotFull, "pt-BR"},
		{"en", SlotForced, "en.forced"},
		{"pt-BR", SlotForced, "pt-BR.forced"},
	} {
		if got := tt.slot.Key(tt.lang); got != tt.key {
			t.Errorf("Key(%q, %s) = %q, want %q", tt.lang, tt.slot, got, tt.key)
		}
		if l, s := SplitSlotKey(tt.key); l != tt.lang || s != tt.slot {
			t.Errorf("SplitSlotKey(%q) = %q, %s", tt.key, l, s)
		}
	}
}

func TestProfilesParseAndEncode(t *testing.T) {
	// The default is not stored; a broken value means the defaults.
	if got := (Profiles{"en": DefaultProfile()}).Encode(); got != "" {
		t.Fatalf("a default profile is stored as %q", got)
	}
	for _, raw := range []string{"", "   ", "not json", "[]", `{"en": 5}`} {
		if got := ParseProfiles(raw); len(got) != 0 {
			t.Errorf("ParseProfiles(%q) = %v", raw, got)
		}
	}
	in := Profiles{"en": {Full: FullNormal, Forced: true}, "FR": {Full: FullNone}, "de": {Full: FullEither}}
	enc := in.Encode()
	out := ParseProfiles(enc)
	want := Profiles{"en": {Full: FullNormal, Forced: true}, "fr": {Full: FullNone, Forced: true}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("round trip gave %v from %s, want %v", out, enc, want)
	}
	if got := out.For("FR"); got.Full != FullNone || !got.Forced {
		t.Fatalf("For(FR) = %+v", got)
	}
	if got := out.For("ja"); got != DefaultProfile() {
		t.Fatalf("a language with no choice gets %+v", got)
	}
	if !reflect.DeepEqual(out.Languages(), []string{"en", "fr"}) {
		t.Fatalf("Languages = %v", out.Languages())
	}
	// An unknown mode is the default, and forced-only always wants the track.
	if p := ParseProfiles(`{"en":{"full":"loud"}}`); len(p) != 0 {
		t.Fatalf("unknown mode kept: %v", p)
	}
}

func TestClassifyName(t *testing.T) {
	tests := []struct {
		name       string
		hi, forced bool
	}{
		{"Movie.2020.1080p.WEB.SDH", true, false},
		{"Movie.2020.1080p.WEB-GROUP", false, false},
		{"Movie (2020) English forced", false, true},
		{"Movie.2020.Foreign.Parts.Only", false, true},
		{"Movie 2020 hearing impaired", true, false},
		{"Movie 2020 closed captions", true, false},
		{"Forced Entry 2019 1080p", false, false},          // a title that starts with the word
		{"Foreign Correspondent 1940 1080p", false, false}, // the same
		{"Movie 2020 not forced", false, false},
		{"Movie 2020 non-SDH", false, false},
		{"Movie 2020 Hindi HI", false, false}, // "hi" alone is not a marker
		{"", false, false},
	}
	for _, tt := range tests {
		hi, forced := ClassifyName(tt.name)
		if hi != tt.hi || forced != tt.forced {
			t.Errorf("ClassifyName(%q) = %v, %v; want %v, %v", tt.name, hi, forced, tt.hi, tt.forced)
		}
	}
}

func TestSidecarPathAndVariant(t *testing.T) {
	video := filepath.Join("lib", "Movie (2020)", "Movie (2020).mkv")
	for _, tt := range []struct {
		lang    string
		variant Variant
		suffix  string
	}{
		{"en", VariantFull, ".en.srt"},
		{"en", VariantSDH, ".en.sdh.srt"},
		{"pt-BR", VariantForced, ".pt-BR.forced.srt"},
	} {
		path := SidecarPath(video, tt.lang, tt.variant)
		if want := strings.TrimSuffix(video, ".mkv") + tt.suffix; path != want {
			t.Errorf("SidecarPath = %q, want %q", path, want)
		}
		if v, ok := VariantOfPath(video, tt.lang, path); !ok || v != tt.variant {
			t.Errorf("VariantOfPath(%q) = %q, %v", path, v, ok)
		}
	}
	if _, ok := VariantOfPath(video, "en", strings.TrimSuffix(video, ".mkv")+".fr.srt"); ok {
		t.Error("another language's file is not this subtitle")
	}
	if _, ok := VariantOfPath(video, "en", strings.TrimSuffix(video, ".mkv")+".en.default.srt"); ok {
		t.Error("an unknown flag is not one of the kinds Mediarium writes")
	}
}

func TestDiskHaveReadsLanguageAndKind(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Film (2020).mkv")
	for _, name := range []string{
		"Film (2020).mkv",
		"Film (2020).en.srt",
		"Film (2020).fr.sdh.srt",
		"Film (2020).de.forced.srt",
		"Film (2020).es.hi.srt",
		"Film (2020).it.foreign.ass",
		"Film (2020).pt-BR.default.srt", // "default" says nothing about the kind
		"Film (2020).forced.srt",        // no language at all
		"Film (2020).en.txt",            // not a subtitle
		"Other (2021).en.srt",           // another video's
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := HaveMap{
		"en":    {Full: true},
		"fr":    {SDH: true},
		"de":    {Forced: true},
		"es":    {SDH: true},
		"it":    {Forced: true},
		"pt-br": {Full: true},
	}
	if got := DiskHave(video); !reflect.DeepEqual(got, want) {
		t.Fatalf("DiskHave = %v\nwant %v", got, want)
	}
	if got := DiskHave(""); len(got) != 0 {
		t.Fatalf("no video: %v", got)
	}
}

func TestHaveMapForUsesTheBaseLanguage(t *testing.T) {
	m := HaveMap{"pt": {Full: true}, "en": {Forced: true}}
	if !m.For("pt-BR").Full || !m.For("PT").Full {
		t.Error("a plain pt file counts for pt-BR")
	}
	if (HaveMap{"pt-br": {Full: true}}).For("pt").Full {
		t.Error("a pt-BR file does not count for plain pt")
	}
	if m.For("en").Full || !m.For("en").Forced {
		t.Errorf("en: %+v", m.For("en"))
	}
}

// subTrackFlags is subTrack with the hearing-impaired flag and a name.
func subTrackFlags(lang, name string, forced, hearing bool) []byte {
	parts := [][]byte{ebml(idTrackType, u(trackSubtitle)), ebml(idCodecID, []byte("S_TEXT/UTF8")), ebml(idLanguage, []byte(lang))}
	if name != "" {
		parts = append(parts, ebml(idName, []byte(name)))
	}
	if forced {
		parts = append(parts, ebml(idFlagForced, u(1)))
	}
	if hearing {
		parts = append(parts, ebml(idFlagHearing, u(1)))
	}
	return ebml(idTrackEntry, parts...)
}

func TestEmbeddedHaveSeparatesForcedAndSDHTracks(t *testing.T) {
	tracks := ebml(idTracks,
		subTrackFlags("eng", "", false, false),
		subTrackFlags("fre", "", false, true),             // flagged hearing impaired
		subTrackFlags("ger", "Deutsch SDH", false, false), // said so in the name
		subTrackFlags("spa", "Forced", false, false),      // forced by its name
		subTrackFlags("ita", "", true, true),              // both flags: a forced track
	)
	file := append(ebml(idEBML, ebml(0x4282, []byte("matroska"))), ebml(idSegment, tracks)...)
	p := filepath.Join(t.TempDir(), "flags.mkv")
	if err := os.WriteFile(p, file, 0o644); err != nil {
		t.Fatal(err)
	}
	want := HaveMap{
		"en": {Full: true},
		"fr": {SDH: true},
		"de": {SDH: true},
		"es": {Forced: true},
		"it": {Forced: true},
	}
	if got := EmbeddedHave(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("EmbeddedHave = %v\nwant %v", got, want)
	}
	// The older answer still counts every track that isn't forced.
	langs := EmbeddedLanguages(p)
	var got []string
	for l := range langs {
		got = append(got, l)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"de", "en", "fr"}) {
		t.Fatalf("EmbeddedLanguages = %v", got)
	}
}

func TestFindAsksOpenSubtitlesToFilterOnlyWhenAProfileAsksFor(t *testing.T) {
	tests := []struct {
		name string
		p    Profile
		slot Slot
		want map[string]string
	}{
		{"default sends nothing extra", Profile{}, SlotFull, map[string]string{}},
		{"normal only", Profile{Full: FullNormal}, SlotFull, map[string]string{"hearing_impaired": "exclude", "foreign_parts_only": "exclude"}},
		{"SDH only", Profile{Full: FullSDH}, SlotFull, map[string]string{"hearing_impaired": "only"}},
		{"the forced track", Profile{Full: FullNormal, Forced: true}, SlotForced, map[string]string{"foreign_parts_only": "only"}},
		{"forced only", Profile{Full: FullNone}, SlotForced, map[string]string{"foreign_parts_only": "only"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var q map[string][]string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q = r.URL.Query()
				w.Write([]byte(`{"data":[]}`))
			}))
			defer srv.Close()
			query := tt.p.Apply(tt.slot, Query{TMDBID: 603, Language: "en"})
			if _, err := NewWithBaseURL("k", srv.URL).Find(context.Background(), query); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"hearing_impaired", "foreign_parts_only"} {
				got := ""
				if v := q[key]; len(v) > 0 {
					got = v[0]
				}
				if got != tt.want[key] {
					t.Errorf("%s = %q, want %q", key, got, tt.want[key])
				}
			}
		})
	}
}

func TestSearchReadsTheHearingImpairedAndForeignPartsFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
		 {"attributes":{"language":"en","release":"A","hearing_impaired":true,"foreign_parts_only":false,"files":[{"file_id":1}]}},
		 {"attributes":{"language":"en","release":"B","hearing_impaired":false,"foreign_parts_only":true,"files":[{"file_id":2}]}},
		 {"attributes":{"language":"en","release":"C","hearing_impaired":false,"foreign_parts_only":false,"files":[{"file_id":3}]}}
		]}`))
	}))
	defer srv.Close()
	got, err := NewWithBaseURL("k", srv.URL).Find(context.Background(), Query{TMDBID: 1})
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].Variant() != VariantSDH || got[1].Variant() != VariantForced || got[2].Variant() != VariantFull {
		t.Fatalf("variants: %q %q %q", got[0].Variant(), got[1].Variant(), got[2].Variant())
	}
	// The same list under three profiles picks different subtitles.
	pick := func(p Profile, s Slot) int {
		best := Pick(p.Filter(s, got), "x.mkv")
		if best == nil {
			return 0
		}
		return best.FileID
	}
	if pick(Profile{Full: FullNormal}, SlotFull) != 3 || pick(Profile{Full: FullSDH}, SlotFull) != 1 || pick(Profile{Forced: true}, SlotForced) != 2 {
		t.Fatal("the profiles did not pick the matching kind")
	}
	if pick(Profile{Full: FullNormal}, SlotForced) != 2 {
		t.Fatal("the forced slot takes the forced one whatever the full mode is")
	}
}

func TestSubDLAsksForTheHearingImpairedFlagAndReadsIt(t *testing.T) {
	var asked url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query()
		w.Write([]byte(`{"status":true,"subtitles":[
		 {"release_name":"Film.2020.1080p.WEB-GRP","name":"film.srt","url":"/subtitle/1-1.zip","hi":true,"comment":""},
		 {"release_name":"Film.2020.1080p.WEB-GRP","name":"film.forced.srt","url":"/subtitle/2-2.zip","hi":false,"comment":""},
		 {"release_name":"Film.2020.1080p.WEB-GRP","name":"film2.srt","url":"/subtitle/3-3.zip","hi":false,"comment":"Only the foreign parts, forced"},
		 {"release_name":"Film.2020.1080p.WEB-GRP","name":"film3.srt","url":"/subtitle/4-4.zip","hi":false,"comment":"Full subtitles"}
		]}`))
	}))
	defer srv.Close()
	got, err := NewSubDLWithBaseURL("k", srv.URL, srv.URL).Find(context.Background(), Query{TMDBID: 5, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if asked.Get("hi") != "1" || asked.Get("comment") != "1" {
		t.Fatalf("hi=%q comment=%q", asked.Get("hi"), asked.Get("comment"))
	}
	var variants []Variant
	for _, r := range got {
		variants = append(variants, r.Variant())
	}
	if want := []Variant{VariantSDH, VariantForced, VariantForced, VariantFull}; !reflect.DeepEqual(variants, want) {
		t.Fatalf("variants = %v, want %v", variants, want)
	}
}
