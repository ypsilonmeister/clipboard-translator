package languages

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Direction is what to ask the model for. Source is empty for a
// target-only profile: the model then detects the source language itself.
type Direction struct{ Source, Target string }

// A language pair decides which way a selection is translated. The model
// (PLaMo 3 Translate) takes lowercase codes and, per its model card, covers the
// 53 languages below. The direction is read from the script of the text, so two
// languages that share a script cannot be told apart this way.

type language struct {
	name   string
	script string // key of scriptTables
}

var languages = map[string]language{
	"ja": {"Japanese", "ja"}, "en": {"English", "latin"},
	"zh-hans": {"Chinese (Simplified)", "han"}, "zh-hant": {"Chinese (Traditional)", "han"},
	"ko": {"Korean", "hangul"}, "ar": {"Arabic", "arabic"}, "az": {"Azerbaijani", "latin"},
	"be": {"Belarusian", "cyrillic"}, "bg": {"Bulgarian", "cyrillic"}, "bn": {"Bengali", "bengali"},
	"ca": {"Catalan", "latin"}, "ceb": {"Cebuano", "latin"}, "de": {"German", "latin"},
	"es": {"Spanish", "latin"}, "et": {"Estonian", "latin"}, "eu": {"Basque", "latin"},
	"fa": {"Persian", "arabic"}, "fr": {"French", "latin"}, "gu": {"Gujarati", "gujarati"},
	"he": {"Hebrew", "hebrew"}, "hi": {"Hindi", "devanagari"}, "hr": {"Croatian", "latin"},
	"hu": {"Hungarian", "latin"}, "hy": {"Armenian", "armenian"}, "id": {"Indonesian", "latin"},
	"it": {"Italian", "latin"}, "jv": {"Javanese", "latin"}, "ka": {"Georgian", "georgian"},
	"kk": {"Kazakh", "cyrillic"}, "km": {"Khmer", "khmer"}, "lt": {"Lithuanian", "latin"},
	"lv": {"Latvian", "latin"}, "ml": {"Malayalam", "malayalam"}, "mr": {"Marathi", "devanagari"},
	"ms": {"Malay", "latin"}, "my": {"Burmese", "myanmar"}, "ne": {"Nepali", "devanagari"},
	"nl": {"Dutch", "latin"}, "pa": {"Punjabi", "gurmukhi"}, "pl": {"Polish", "latin"},
	"ru": {"Russian", "cyrillic"}, "si": {"Sinhala", "sinhala"}, "sk": {"Slovak", "latin"},
	"sl": {"Slovenian", "latin"}, "sr": {"Serbian", "cyrillic"}, "sw": {"Swahili", "latin"},
	"ta": {"Tamil", "tamil"}, "te": {"Telugu", "telugu"}, "th": {"Thai", "thai"},
	"tl": {"Tagalog", "latin"}, "ur": {"Urdu", "arabic"}, "uz": {"Uzbek", "latin"},
	"vi": {"Vietnamese", "latin"},
}

// "ja" is kana only: Han is shared with Chinese and handled separately.
var scriptTables = map[string][]*unicode.RangeTable{
	"latin": {unicode.Latin}, "ja": {unicode.Hiragana, unicode.Katakana}, "han": {unicode.Han},
	"hangul": {unicode.Hangul}, "arabic": {unicode.Arabic}, "cyrillic": {unicode.Cyrillic},
	"bengali": {unicode.Bengali}, "gujarati": {unicode.Gujarati}, "hebrew": {unicode.Hebrew},
	"devanagari": {unicode.Devanagari}, "armenian": {unicode.Armenian}, "georgian": {unicode.Georgian},
	"khmer": {unicode.Khmer}, "malayalam": {unicode.Malayalam}, "myanmar": {unicode.Myanmar},
	"gurmukhi": {unicode.Gurmukhi}, "sinhala": {unicode.Sinhala}, "tamil": {unicode.Tamil},
	"telugu": {unicode.Telugu}, "thai": {unicode.Thai},
}

// Default is the pair used when a config names none.
var Default = []string{"ja", "en"}

func Name(code string) string {
	if l, ok := languages[code]; ok {
		return l.name
	}
	return code
}

// PairLabel is how a pair is shown in profile labels.
func PairLabel(pair []string) string {
	return Name(pair[0]) + " ↔ " + Name(pair[1])
}

// Validate checks a "languages" setting and returns it normalized
// (trimmed, lowercase). It rejects what the app cannot translate reliably.
func Validate(in []string) ([]string, error) {
	if len(in) != 2 {
		return nil, fmt.Errorf("languages must list exactly two language codes, e.g. [\"ja\", \"en\"]")
	}
	out := make([]string, 2)
	for i, c := range in {
		c = strings.ToLower(strings.TrimSpace(c))
		if _, ok := languages[c]; !ok {
			hint := ""
			if c == "zh" || c == "zh-cn" || c == "zh-tw" {
				hint = " (use zh-hans or zh-hant)"
			}
			return nil, fmt.Errorf("languages: %q is not a supported language code%s; supported: %s", in[i], hint, strings.Join(Codes(), ", "))
		}
		out[i] = c
	}
	if out[0] == out[1] {
		return nil, fmt.Errorf("languages: both entries are %q; pick two different languages", out[0])
	}
	if languages[out[0]].script == languages[out[1]].script {
		return nil, fmt.Errorf("languages: %s and %s use the same script, so the app cannot tell from the text which way to translate. Pick a language with a different script for now", Name(out[0]), Name(out[1]))
	}
	return out, nil
}

// Known reports whether code is one of the supported language codes.
func Known(code string) bool {
	_, ok := languages[code]
	return ok
}

// Language is one entry of the table, for pages that list them.
type Language struct{ Code, Name string }

// All returns every supported language, in no particular order.
func All() []Language {
	out := make([]Language, 0, len(languages))
	for code, l := range languages {
		out = append(out, Language{Code: code, Name: l.name})
	}
	return out
}

func Codes() []string {
	codes := make([]string, 0, len(languages))
	for c := range languages {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return codes
}

func hasScript(s, script string) bool {
	tables := scriptTables[script]
	for _, r := range s {
		if unicode.In(r, tables...) {
			return true
		}
		// Half-width katakana is not in unicode.Katakana.
		if script == "ja" && r >= 0xFF66 && r <= 0xFF9D {
			return true
		}
	}
	return false
}

// uses reports whether s contains the script of lang. For Japanese that includes
// Han, because kanji-only text is Japanese unless the pair says otherwise.
func uses(s, lang string) bool {
	l := languages[lang]
	return hasScript(s, l.script) || (lang == "ja" && hasScript(s, "han"))
}

func IsURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")
}

// DirectionFor says which way s should be translated for the language pair.
// The language with its own non-Latin script wins when the text mixes scripts
// (Japanese with an English word is still Japanese). URLs and text without a
// letter of either script are not translated.
func DirectionFor(pair []string, s string) (Direction, bool) {
	if len(pair) != 2 {
		pair = Default
	}
	s = strings.TrimSpace(s)
	if s == "" || IsURL(s) {
		return Direction{}, false
	}
	a, b := pair[0], pair[1]
	to := func(src, dst string) (Direction, bool) {
		return Direction{Source: src, Target: dst}, true
	}
	la, lb := languages[a].script == "latin", languages[b].script == "latin"
	switch {
	case la && lb:
		return Direction{}, false // Validate refuses this pair
	case la || lb:
		latin, other := a, b
		if lb {
			latin, other = b, a
		}
		if uses(s, other) {
			return to(other, latin)
		}
		if hasScript(s, "latin") {
			return to(latin, other)
		}
		return Direction{}, false
	}
	ownA, ownB := hasScript(s, languages[a].script), hasScript(s, languages[b].script)
	switch {
	case ownA && ownB:
		if b == "ja" { // kana only occurs in Japanese, even beside Han
			return to(b, a)
		}
		return to(a, b)
	case ownA:
		return to(a, b)
	case ownB:
		return to(b, a)
	}
	// Han with no kana or Hangul: Japanese if the pair has Japanese.
	if hasScript(s, "han") {
		if a == "ja" {
			return to(a, b)
		}
		if b == "ja" {
			return to(b, a)
		}
	}
	return Direction{}, false
}

func SamePair(a, b []string) bool {
	return len(a) == 2 && len(b) == 2 && (a[0] == b[0] && a[1] == b[1] || a[0] == b[1] && a[1] == b[0])
}
