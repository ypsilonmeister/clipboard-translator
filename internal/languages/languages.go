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
		if script == "ja" && kanaExtra(r) {
			return true
		}
	}
	return false
}

// kanaExtra covers what unicode.Hiragana and unicode.Katakana leave out:
// half-width katakana and the long vowel mark.
func kanaExtra(r rune) bool {
	return (r >= 0xFF66 && r <= 0xFF9D) || r == 'ー'
}

// uses reports whether s contains the script of lang. For Japanese that includes
// Han, because kanji-only text is Japanese unless the pair says otherwise.
func uses(s, lang string) bool {
	l := languages[lang]
	return hasScript(s, l.script) || (lang == "ja" && hasScript(s, "han"))
}

// inScript reports whether r belongs to the script of lang (for Japanese that
// includes Han, as in uses).
func inScript(r rune, lang string) bool {
	if unicode.In(r, scriptTables[languages[lang].script]...) {
		return true
	}
	if lang == "ja" {
		return unicode.Is(unicode.Han, r) || kanaExtra(r)
	}
	return false
}

// unspaced lists the scripts whose words are not separated by spaces (or, for
// Hangul, that attach particles to words), so the amount of such text is
// counted in characters. Other scripts are counted in space-separated tokens.
var unspaced = map[string]bool{
	"han": true, "ja": true, "hangul": true, "thai": true, "khmer": true, "myanmar": true,
}

// Thresholds for text that mixes Latin with the other script of the pair.
// The text is read as Latin only when Latin is clearly its skeleton; where
// unsure, the result stays "other -> Latin", as before this rule.
//
//   - minProseWords: product names and identifiers do not make a sentence
//     ("Windows 11, macOS Sonoma, Red Hat Enterprise Linux" inside Japanese,
//     Russian or Arabic text), so at least this many Latin words must look like
//     running prose (see proseWord).
//   - latinWordsNum/latinWordsDen: Latin words per unit of the other script.
//     3/2 lets an English sentence of 11 words carry a 6-character name, and
//     still leaves "git commit してから git push origin main を実行" Japanese.
const (
	minProseWords = 5
	latinWordsNum = 3
	latinWordsDen = 2
)

// proseWord reports whether a Latin token looks like a word of running text:
// lowercase letters only, apart from punctuation around it and ' or - inside.
// Names, acronyms, versions, paths and identifiers are not.
func proseWord(tok string) bool {
	tok = strings.TrimFunc(tok, func(r rune) bool { return !unicode.IsLetter(r) })
	for _, r := range tok {
		if !unicode.IsLower(r) && r != 0x27 && r != 0x2019 && r != '-' {
			return false
		}
	}
	return tok != ""
}

// latinIsSkeleton reports whether s (known to contain the script of other)
// is Latin text quoting a little of the other script, rather than the reverse.
// Over the space-separated tokens of s: w counts those with Latin letters and
// none of the other script ("Composeを" has a particle attached, which is the
// other language's grammar, and a URL or code fragment is one token however
// long), prose counts the w that are proseWords, and u is the amount of the
// other script (characters, or tokens for space-separated scripts).
//
// Text that opens or ends in the other script keeps the old result whatever
// the counts say: a command is made of lowercase words too, so "Запусти npm
// run build and then npm run test" and "npm run build and then npm run test
// не работает" cannot be told from an English sentence with a foreign word at
// either end. Tokens that cannot be a word of either language (a flag, a URL,
// a path; see nonWordToken) are counted but do not move that boundary, so a
// trailing "--verbose" or link does not turn the command into English (#208).
func latinIsSkeleton(s, other string) bool {
	byChar := unspaced[languages[other].script]
	w, prose, u := 0, 0, 0
	first := true // no token with a letter of either script yet
	endsOther := false
	for _, tok := range strings.Fields(s) {
		hasLatin, n := false, 0
		for _, r := range tok {
			switch {
			case inScript(r, other):
				// A token that begins its Japanese with hiragana is a particle or
				// an inflection ("を", "する", "の比較"): Japanese grammar around
				// Latin words, however many ("git push origin main する").
				if n == 0 && other == "ja" && unicode.Is(unicode.Hiragana, r) {
					return false
				}
				n++
			case unicode.Is(unicode.Latin, r):
				hasLatin = true
			}
		}
		if (n > 0 || hasLatin) && !nonWordToken(tok) {
			if first && n > 0 {
				return false
			}
			first = false
			endsOther = n > 0
		}
		switch {
		case n > 0 && byChar:
			u += n
		case n > 0:
			u++
		case hasLatin:
			w++
			if proseWord(tok) {
				prose++
			}
		}
	}
	return !endsOther && prose >= minProseWords && w*latinWordsDen >= u*latinWordsNum
}

// nonWordToken reports whether tok cannot be a word of running text in any
// language: a command-line flag ("-v", "--verbose"), a URL, a path or an
// assignment ("src/main.go", "C:\work", "DEBUG=1"). Surrounding brackets and
// quotes are ignored, so "(https://example.com)" is still a URL. Tokens
// without a letter are not the question: they never count anywhere.
func nonWordToken(tok string) bool {
	tok = strings.Trim(tok, "([{<\"'“‘")
	return strings.HasPrefix(tok, "-") || IsURL(tok) || strings.ContainsAny(tok, `/\=`)
}

func IsURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")
}

// DirectionFor says which way s should be translated for the language pair.
// For a Latin language paired with a non-Latin one, the non-Latin language wins
// when the text mixes scripts (Japanese with an English word is still
// Japanese), unless the Latin side is clearly the skeleton: a sentence of
// space-separated Latin words around a few characters of the other script
// (English with a quoted Japanese name is still English; see latinIsSkeleton).
// URLs and text without a letter of either script are not translated.
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
			if latinIsSkeleton(s, other) {
				return to(latin, other)
			}
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
