package textstruct

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// lineInfo contains structural metadata for a single line of text.
type lineInfo struct {
	raw            string
	trimmed        string
	width          int
	indent         string
	isPreserved    bool
	endsSentence   bool
	isHyphenated   bool
	isShortSignOff bool
}

// CleanSpuriousNewlines reflows soft line breaks caused by fixed-width column wraps (e.g. PDFs, terminal output)
// while strictly preserving all structural boundaries (blank lines, lists, code, quotes, signatures).
func CleanSpuriousNewlines(text string, lang string) string {
	if text == "" {
		return ""
	}

	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}

	lines := splitLines(text)
	if len(lines) <= 1 {
		return text
	}

	infos := analyzeLines(lines)
	colMedian := computeColumnMedian(infos)

	var b strings.Builder
	b.WriteString(infos[0].raw)

	for i := 0; i < len(infos)-1; i++ {
		cur := infos[i]
		next := infos[i+1]

		join, sep := shouldJoin(cur, next, colMedian, lang)
		if join {
			b.WriteString(sep)
			b.WriteString(strings.TrimLeft(next.raw, " \t"))
		} else {
			b.WriteString(nl)
			b.WriteString(next.raw)
		}
	}

	return b.String()
}

func splitLines(text string) []string {
	var lines []string
	rawLines := strings.Split(text, "\n")
	for _, l := range rawLines {
		lines = append(lines, strings.TrimRight(l, "\r"))
	}
	return lines
}

func analyzeLines(lines []string) []lineInfo {
	infos := make([]lineInfo, len(lines))
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		indent := extractIndent(l)
		preserved := isPreservedLine(l)
		shortSignOff := isShortSignOff(trimmed)

		infos[i] = lineInfo{
			raw:            l,
			trimmed:        trimmed,
			width:          stringWidth(trimmed),
			indent:         indent,
			isPreserved:    preserved,
			endsSentence:   endsInSentencePunctuation(trimmed),
			isHyphenated:   isTrailingHyphen(trimmed),
			isShortSignOff: shortSignOff,
		}
	}
	return infos
}

func extractIndent(s string) string {
	for i, r := range s {
		if r != ' ' && r != '\t' && r != '\u3000' {
			return s[:i]
		}
	}
	return s
}

func isTrailingHyphen(s string) bool {
	runes := []rune(s)
	if len(runes) < 2 {
		return false
	}
	last := runes[len(runes)-1]
	prev := runes[len(runes)-2]
	return last == '-' && (unicode.IsLetter(prev) || unicode.IsDigit(prev))
}

func endsInSentencePunctuation(s string) bool {
	runes := []rune(s)
	if len(runes) == 0 {
		return false
	}

	// Strip closing brackets/quotes if any (e.g. ." or 。」)
	lastIdx := len(runes) - 1
	for lastIdx > 0 && isClosingPunctuation(runes[lastIdx]) {
		lastIdx--
	}

	r := runes[lastIdx]
	return r == '.' || r == '!' || r == '?' ||
		r == '。' || r == '！' || r == '？' || r == '…' || r == '―'
}

func isClosingPunctuation(r rune) bool {
	switch r {
	case '"', '\'', ')', ']', '}', '」', '』', '）', '】', '”', '’':
		return true
	default:
		return false
	}
}

// computeColumnMedian calculates the median width of substantial prose lines.
// Lines shorter than 25 visual display width are excluded so that short lines
// (lists, addresses, signatures, chat messages) never establish a column margin.
func computeColumnMedian(infos []lineInfo) int {
	var widths []int
	for _, info := range infos {
		if !info.isPreserved && info.width >= 25 {
			widths = append(widths, info.width)
		}
	}
	if len(widths) < 2 {
		return 0
	}
	sort.Ints(widths)
	return widths[len(widths)/2]
}

// hasGeometricCorroboration verifies that cur is part of a column wrap.
// Lines shorter than 25 visual width are explicit semantic breaks (items, addresses,
// chat lines, poetry) that did not break because of page margin limits.
func hasGeometricCorroboration(cur, next lineInfo, colMedian int) bool {
	if cur.width < 25 {
		return false
	}
	if colMedian >= 25 {
		if cur.width >= colMedian-8 {
			return true
		}
	}
	// Standalone wrapped line (e.g. 2-line wrapped email paragraph)
	if cur.width >= 35 {
		return true
	}
	return false
}

func shouldJoin(cur, next lineInfo, colMedian int, lang string) (bool, string) {
	// 1. Hard structural vetoes: preserved lines (blank lines, quotes, lists, code, etc.)
	if cur.isPreserved || next.isPreserved || cur.isShortSignOff || next.isShortSignOff {
		return false, ""
	}

	// 2. Paragraph indentation veto (e.g. next line has 2+ spaces or fullwidth space indentation)
	if next.indent != cur.indent && (len(next.indent) >= 2 || strings.HasPrefix(next.raw, "\u3000")) {
		return false, ""
	}

	curRunes := []rune(cur.trimmed)
	nextRunes := []rune(next.trimmed)
	if len(curRunes) == 0 || len(nextRunes) == 0 {
		return false, ""
	}

	curLast := curRunes[len(curRunes)-1]
	nextFirst := nextRunes[0]

	// 3. Script transition veto: Latin sentence end followed by CJK (or vice versa)
	if cur.endsSentence {
		if isLatin(curLast) || curLast == '.' || curLast == '!' || curLast == '?' {
			if isCJK(nextFirst) {
				return false, "" // e.g. "improvements.\n本バージョンでは"
			}
		}
		if isCJK(curLast) {
			if isLatin(nextFirst) {
				return false, ""
			}
		}
	}

	// 4. Short non-sentence line followed by Uppercase (e.g. "Taro Yamada\nSenior Director...")
	if !cur.endsSentence && len(curRunes) < 25 && unicode.IsUpper(nextFirst) {
		return false, ""
	}

	// 5. Unclustered lines with uppercase continuation (e.g. "Senior Director...\nClipboard...")
	if unicode.IsUpper(nextFirst) && colMedian > 0 {
		diffCur := math.Abs(float64(cur.width - colMedian))
		diffNext := math.Abs(float64(next.width - colMedian))
		if diffCur > 10 && diffNext > 10 {
			return false, ""
		}
	}

	// Geometric Corroboration Requirement:
	// A soft break is ONLY considered spurious when geometric evidence indicates
	// the line reached a column margin. Short lines (lists, addresses, chat logs)
	// are never joined.
	if !hasGeometricCorroboration(cur, next, colMedian) {
		// Exception: in a multi-line column block (colMedian >= 25), if cur ends in sentence
		// punctuation and next line is a full-width column continuation (next.width >= colMedian - 8):
		if colMedian >= 25 && cur.endsSentence && next.width >= colMedian-8 {
			sep := determineSeparator(lang, curLast, nextFirst)
			return true, sep
		}
		return false, ""
	}

	// Positive Rule 1: Hyphenated wrap [a-zA-Z]-\n[a-zA-Z] -> preserve hyphen, remove newline
	if cur.isHyphenated && unicode.IsLetter(nextFirst) {
		return true, ""
	}

	// Positive Rule 2: Mid-sentence continuation in Latin / Western script
	if !cur.endsSentence {
		if unicode.IsLower(nextFirst) || unicode.IsDigit(nextFirst) || curLast == ',' || curLast == '%' {
			sep := determineSeparator(lang, curLast, nextFirst)
			return true, sep
		}
	}

	// Positive Rule 3: Mid-sentence continuation in Japanese / CJK script
	if !cur.endsSentence && (isCJK(curLast) || isCJK(nextFirst)) {
		sep := determineSeparator(lang, curLast, nextFirst)
		return true, sep
	}

	// Positive Rule 4: Uniform column wrap continuation across sentence end
	if cur.endsSentence {
		if next.width >= 15 && !cur.isShortSignOff && !next.isShortSignOff {
			sep := determineSeparator(lang, curLast, nextFirst)
			return true, sep
		}
	}

	// Default: when in doubt, preserve the newline (Asymmetry of Failure)
	return false, ""
}

// determineSeparator chooses the joining string (" " vs "") based on language hint and script.
func determineSeparator(lang string, curLast, nextFirst rune) string {
	if lang != "" {
		l := strings.ToLower(lang)
		if l == "ja" || strings.HasPrefix(l, "zh") || l == "ko" {
			if isLatin(curLast) && isLatin(nextFirst) {
				return " "
			}
			return ""
		}
		if l == "en" || l == "de" || l == "fr" || l == "es" || l == "it" || l == "pt" || l == "ru" {
			return " "
		}
	}

	if isCJK(curLast) && isCJK(nextFirst) {
		return ""
	}
	if isCJK(curLast) || isCJK(nextFirst) {
		return ""
	}
	return " "
}
