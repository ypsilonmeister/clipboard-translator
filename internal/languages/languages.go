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

// unspaced lists scripts without word spaces. Count base characters, not
// combining marks. Korean uses word spaces, so Hangul counts tokens instead.
var unspaced = map[string]bool{
	"han": true, "ja": true, "thai": true, "khmer": true, "myanmar": true,
}

// A hiragana prefix is grammar evidence only for these particles/inflections.
// Native vocabulary (わびさび, お疲れ様) and honorifics (さん) can be quoted in
// Latin prose. Keep the list conservative rather than treating all hiragana
// as grammar. A following kanji, as in の比較 or を実行, ends the prefix.
func japaneseGrammarPrefix(s string) bool {
	end := strings.IndexFunc(s, func(r rune) bool { return !unicode.Is(unicode.Hiragana, r) })
	if end >= 0 {
		s = s[:end]
	}
	switch s {
	case "は", "が", "を", "に", "で", "と", "の", "へ", "や", "も",
		"から", "まで", "より", "とは", "には", "では",
		"する", "した", "して", "してから", "します", "しない":
		return true
	}
	return false
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
// Over tokens split at whitespace or CJK comma/period: w counts Latin letters and
// none of the other script ("Composeを" has a particle attached, which is the
// other language's grammar, and a URL or code fragment is one token however
// long), prose counts the w that are proseWords, and u is the amount of the
// other script (base characters, or tokens for space-separated scripts).
//
// Text that opens or ends in the other script keeps the old result whatever
// the counts say: a command is made of lowercase words too, so "Запусти npm
// run build and then npm run test" and "npm run build and then npm run test
// не работает" cannot be told from an English sentence with a foreign word at
// either end. Tokens that cannot be a word of either language (a flag, a URL,
// a path; see nonWordToken) are counted but do not move that boundary, so a
// trailing "--verbose" or link does not turn the command into English (#208).
// Japanese names with honorifics also leave the boundary alone (#205).
func latinIsSkeleton(s, other string) bool {
	byChar := unspaced[languages[other].script]
	w, prose, u := 0, 0, 0
	first := true // no token with a letter of either script yet
	endsOther := false
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '、' || r == '。'
	}) {
		hasLatin, n := false, 0
		for i, r := range tok {
			switch {
			case inScript(r, other):
				if n == 0 && other == "ja" && japaneseGrammarPrefix(tok[i:]) {
					return false
				}
				if !unicode.IsMark(r) {
					n++
				}
			case unicode.Is(unicode.Latin, r):
				hasLatin = true
			}
		}
		honorificName := n > 0 && other == "ja" && japaneseHonorificName(tok)
		if (n > 0 || hasLatin) && !honorificName && !nonWordToken(tok) {
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

// A kanji or katakana name with an honorific can address someone in either
// language. Require the whole token (apart from surrounding punctuation) so
// attached particles or other text still contribute to the boundary.
func japaneseHonorificName(tok string) bool {
	tok = strings.TrimFunc(tok, unicode.IsPunct)
	for _, suffix := range [...]string{"さん", "さま", "様", "氏", "殿", "くん", "君", "ちゃん"} {
		name, found := strings.CutSuffix(tok, suffix)
		if found && name != "" {
			return strings.IndexFunc(name, func(r rune) bool {
				return !unicode.In(r, unicode.Han, unicode.Katakana) && !kanaExtra(r)
			}) < 0
		}
	}
	return false
}

// nonWordToken reports whether tok cannot be a word of running text in any
// language, so it must not mark where the text opens or ends. Brackets,
// quotes and sentence punctuation around the token are ignored first, so
// "(https://example.com)" and "（--verbose）" count. A non-word is:
//
//   - a flag: "-v", "--verbose", "--config=file", also with the dash an
//     editor lengthened ("—verbose");
//   - a URL, an email, a mention or a hashtag: "https://x.y",
//     "www.example.com", "support@example.com", "@user", "#golang";
//   - a domain or a file name: "example.com", "x.com", "main.go", "a.go",
//     "main.c", "en.wikipedia.org" (see dotted). "e.g.", "a.k.a.", "op.cit."
//     and "node.js" are not: dotted abbreviations and product names look
//     like domains, and a list of last labels is the only line;
//   - a path or an assignment: "src/main.go", "/usr/bin", "C:\build",
//     "DEBUG=1". A slash between plain words is prose ("and/or", "w/o",
//     "client-side/server-side"); "docs/readme" is then taken for a word too,
//     the cheaper error;
//   - an identifier mixing letters and digits: "v1.2.3", "oauth2", "sha256",
//     "2b", "70B", "4k", "10ms", "10s". A number with an ordinal, a decade,
//     am/pm, a multiplier or a dimension/resolution suffix ("2nd", "90s",
//     "5pm", "10x", "3D", "3d", "4K") stays a word (see numberSuffixes and
//     decade); "covid19" does not, which is the safe side: a word mistaken
//     for an identifier only hands the boundary to the previous real word,
//     while an identifier mistaken for a word hides a boundary in the other
//     script (#208).
func nonWordToken(tok string) bool {
	tok = strings.TrimLeft(tok, "([{<\"'“‘（［【「『《〈")
	tok = strings.TrimRight(tok, ")]}>\"'”’）］】」』》〉.,;:!?。、，：！？")
	if tok == "" {
		return false
	}
	if strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "–") || strings.HasPrefix(tok, "—") {
		return true // flag
	}
	if IsURL(tok) || strings.Contains(tok, "://") || strings.HasPrefix(strings.ToLower(tok), "www.") {
		return true
	}
	if strings.ContainsAny(tok, "@=\\") || strings.HasPrefix(tok, "#") {
		return true // email, mention, assignment, Windows path, hashtag
	}
	if strings.Contains(tok, "/") {
		plain := strings.IndexFunc(tok, func(r rune) bool { return r != '/' && r != '-' && !unicode.IsLetter(r) }) < 0
		if !plain || strings.HasPrefix(tok, "/") || strings.HasSuffix(tok, "/") {
			return true // path
		}
	}
	if dotted(tok) {
		return true
	}
	return identifier(tok)
}

// dottedSuffixes are the last labels that make a two-label dotted token a
// domain or a file name rather than an abbreviation or a product name: the
// generic and common country top-level domains, and the file extensions a
// developer is likely to paste. A curated list, because "example.com",
// "node.js" and "op.cit" have the same shape; "js" is left out on purpose so
// "node.js" stays a word, which makes "app.js" a word as well. The one-letter
// extensions "c" and "h" are here too; they only count after a label of two
// or more characters, so "e.g" and "a.c" stay abbreviations.
var dottedSuffixes = map[string]bool{
	// top-level domains
	"com": true, "org": true, "net": true, "edu": true, "gov": true, "mil": true, "int": true,
	"io": true, "co": true, "dev": true, "app": true, "ai": true, "info": true, "biz": true,
	"me": true, "us": true, "in": true, "tv": true, "cc": true, "ly": true, "to": true,
	"xyz": true, "online": true, "tech": true, "site": true, "store": true, "shop": true,
	"blog": true, "cloud": true, "page": true, "link": true, "live": true, "news": true,
	"jp": true, "uk": true, "de": true, "fr": true, "ru": true, "cn": true, "kr": true, "tw": true,
	"eu": true, "ca": true, "au": true, "br": true, "es": true, "nl": true, "se": true, "ch": true,
	"it": true, "pl": true, "be": true, "at": true, "dk": true, "fi": true, "no": true, "nz": true,
	"ie": true, "pt": true, "cz": true, "hk": true, "sg": true, "mx": true, "ar": true, "za": true,
	// file extensions
	"c": true, "h": true, "cpp": true, "cxx": true, "hpp": true, "hh": true,
	"go": true, "py": true, "rs": true, "ts": true, "tsx": true, "jsx": true, "mjs": true, "cjs": true,
	"java": true, "kt": true, "kts": true, "swift": true, "m": true, "mm": true, "cs": true, "fs": true,
	"rb": true, "php": true, "pm": true, "lua": true, "r": true, "jl": true, "scala": true,
	"dart": true, "ex": true, "exs": true, "erl": true, "hs": true, "ml": true, "clj": true,
	"sql": true, "sh": true, "bash": true, "zsh": true, "fish": true, "bat": true, "cmd": true, "ps1": true,
	"wasm": true, "so": true, "dll": true, "exe": true, "dylib": true, "lib": true, "o": true, "obj": true,
	"md": true, "txt": true, "rst": true, "json": true, "yaml": true, "yml": true, "toml": true,
	"ini": true, "cfg": true, "conf": true, "env": true, "log": true, "lock": true, "sum": true, "mod": true,
	"html": true, "htm": true, "css": true, "scss": true, "sass": true, "less": true, "vue": true, "svelte": true,
	"csv": true, "tsv": true, "xml": true, "proto": true, "graphql": true, "wsdl": true,
	"zip": true, "tar": true, "gz": true, "tgz": true, "bz2": true, "xz": true, "zst": true, "rar": true, "7z": true,
	"png": true, "jpg": true, "jpeg": true, "gif": true, "svg": true, "webp": true, "ico": true, "bmp": true,
	"mp3": true, "mp4": true, "wav": true, "ogg": true, "webm": true, "mkv": true, "mov": true,
	"pdf": true, "doc": true, "docx": true, "xls": true, "xlsx": true, "ppt": true, "pptx": true,
	"gguf": true, "bin": true, "bak": true, "tmp": true, "iso": true, "img": true, "dmg": true, "pkg": true,
	"deb": true, "rpm": true, "apk": true, "ipa": true, "jar": true, "war": true, "whl": true, "egg": true,
	"ttf": true, "otf": true, "woff": true, "woff2": true,
}

// dotted reports whether tok is a domain or a file name. The labels around
// the dots must be letters or digits. Non-final labels may also contain '-'
// and '_' when the last label is a recognised suffix ("docker-compose.yml",
// "test_utils.py"); each label must still contain a letter or digit. Then
// either there are three or more labels and the last has two or more
// characters ("en.wikipedia.org",
// "a.b.cd"), or there are two and the last is in dottedSuffixes
// ("example.com", "x.com", "main.go", "a.go"). A one-letter suffix only
// counts after a label of two or more characters ("main.c", not "e.g").
// Dotted abbreviations have one-letter labels throughout ("a.k.a.",
// "U.S.A.") or a last label that is no suffix ("Ph.D.", "op.cit.").
func dotted(tok string) bool {
	labels := strings.Split(tok, ".")
	if len(labels) < 2 {
		return false
	}
	last := labels[len(labels)-1]
	knownSuffix := dottedSuffixes[strings.ToLower(last)]
	for i, l := range labels {
		if i < len(labels)-1 && knownSuffix {
			l = strings.ReplaceAll(l, "-", "")
			l = strings.ReplaceAll(l, "_", "")
		}
		if l == "" || strings.IndexFunc(l, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) >= 0 {
			return false
		}
	}
	if len(labels) >= 3 {
		return len(last) >= 2
	}
	return knownSuffix && (len(last) >= 2 || len(labels[0]) >= 2)
}

// numberSuffixes are the letters that may follow a number in prose: ordinals
// ("2nd"), a time of day ("5pm"), a multiplier ("10x") and a dimension or a
// resolution ("3D", "3d", "4K"). Units and sizes are not here ("10ms", "1gb",
// "70B", "1G", "4k", "2b"): prose puts a space before a unit, and glued
// forms are model sizes, memory sizes and resolutions pasted from a command
// or a spec sheet. "4k" is listed by an earlier review as an identifier and
// "4K" as a word, so case decides for K. Decades ("90s") are in decade.
var numberSuffixes = map[string]bool{"st": true, "nd": true, "rd": true, "th": true, "am": true, "pm": true, "x": true, "d": true, "D": true, "K": true}

// decade reports whether tok names a decade: two digits from 20 to 90, or
// four digits, ending in 0, then "s" ("90s", "1990s", "2010s"). "10s", "5s",
// "30sec" and "100s" are not. The "s" suffix cannot separate "30s" the
// decade from "30s" the duration; the rule optimises for the decade reading
// only where a decade is what people write ("the 90s") and leaves every
// other number with "s" an identifier, which is the safe side (see
// nonWordToken).
func decade(tok string) bool {
	if !strings.HasSuffix(tok, "s") {
		return false
	}
	num := tok[:len(tok)-1]
	if strings.IndexFunc(num, func(r rune) bool { return r < '0' || r > '9' }) >= 0 || !strings.HasSuffix(num, "0") {
		return false
	}
	return len(num) == 4 || (len(num) == 2 && num[0] >= '2')
}

// identifier reports whether tok mixes letters and digits, except a number
// followed only by a numberSuffixes entry ("2nd", "5pm", "10x", "3D") or a
// decade ("90s").
func identifier(tok string) bool {
	letters, digits := 0, 0
	for _, r := range tok {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsDigit(r):
			digits++
		}
	}
	if letters == 0 || digits == 0 {
		return false
	}
	rest := strings.TrimLeftFunc(tok, unicode.IsDigit)
	if len(rest) == len(tok) {
		return true // letters first: "covid19", "oauth2", "v1.2.3"
	}
	return !numberSuffixes[rest] && !decade(tok)
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
