package textstruct

import (
	"regexp"
	"strings"
	"unicode"
)

// ValidTidyRule is shared with profile validation. Tidy accepts validated rules.
func ValidTidyRule(rule string) bool {
	switch rule {
	case "newlines", "width", "punct", "spaces", "quotes", "bullets":
		return true
	}
	return false
}

// Tidy applies only the requested rules, in list order. It needs no language
// detection: CleanSpuriousNewlines already chooses separators from the script.
func Tidy(text string, rules []string, punctStyle string) string {
	for _, rule := range rules {
		switch rule {
		case "newlines":
			text = tidyBlocks(text, func(s string) string { return CleanSpuriousNewlines(s, "") })
		case "width":
			text = CleanWidth(text)
		case "punct":
			text = CleanPunctuation(text, punctStyle)
		case "spaces":
			text = CleanSpaces(text)
		case "quotes":
			text = CleanQuotes(text)
		case "bullets":
			text = CleanBullets(text)
		}
	}
	return text
}

// Width conversion is deliberately narrower than NFKC: punctuation, symbols,
// compatibility characters and full-width spaces keep their original meaning.
func CleanWidth(text string) string {
	return tidyLines(text, func(s string) string {
		runes := []rune(s)
		var b strings.Builder
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			switch {
			case r >= '０' && r <= '９', r >= 'Ａ' && r <= 'Ｚ', r >= 'ａ' && r <= 'ｚ':
				r -= 0xFEE0
			case r >= 'ｦ' && r <= 'ﾝ':
				r = fullKatakana[r-'ｦ']
				if i+1 < len(runes) && (runes[i+1] == 'ﾞ' || runes[i+1] == 'ﾟ') {
					if composed, ok := voicedKatakana(r, runes[i+1]); ok {
						r = composed
						i++
					}
				}
			case r == 'ﾞ':
				r = '゛'
			case r == 'ﾟ':
				r = '゜'
			}
			b.WriteRune(r)
		}
		return b.String()
	})
}

var fullKatakana = []rune("ヲァィゥェォャュョッーアイウエオカキクケコサシスセソタチツテトナニヌネノハヒフヘホマミムメモヤユヨラリルレロワン")

func voicedKatakana(r, mark rune) (rune, bool) {
	plain, voiced := "カキクケコサシスセソタチツテトハヒフヘホウワヲ", "ガギグゲゴザジズゼゾダヂヅデドバビブベボヴヷヺ"
	if mark == 'ﾟ' {
		plain, voiced = "ハヒフヘホ", "パピプペポ"
	}
	for i, p := range []rune(plain) {
		if p == r {
			return []rune(voiced)[i], true
		}
	}
	return r, false
}

// CleanPunctuation changes only Japanese/full-width comma and full stop pairs.
// ASCII punctuation (decimals, addresses, URLs) is never rewritten.
func CleanPunctuation(text, style string) string {
	return tidyLines(text, func(s string) string {
		runes := []rune(s)
		for i, r := range runes {
			// Full-width decimal/group separators and dotted names are data.
			if i > 0 && i+1 < len(runes) {
				before, after := runes[i-1], runes[i+1]
				if (r == '，' || r == '．') && unicode.IsDigit(before) && unicode.IsDigit(after) {
					continue
				}
				if r == '．' && unicode.Is(unicode.Latin, before) && unicode.Is(unicode.Latin, after) {
					continue
				}
			}
			if style == "fullwidth" {
				switch r {
				case '、', '､':
					runes[i] = '，'
				case '。', '｡':
					runes[i] = '．'
				}
			} else {
				switch r {
				case '，', '､':
					runes[i] = '、'
				case '．', '｡':
					runes[i] = '。'
				}
			}
		}
		return string(runes)
	})
}

var spaceRun = regexp.MustCompile(` {2,}`)

// CleanSpaces preserves indentation, tabs (column alignment) and list contents.
// Only ordinary prose's internal ASCII spaces collapse; line ends are trimmed.
func CleanSpaces(text string) string {
	return tidyLines(text, func(s string) string {
		if reListMarker.MatchString(s) || reBlockQuote.MatchString(s) || isDivider(s) {
			return s
		}
		if strings.TrimSpace(s) == "" {
			return ""
		}
		indent := extractIndent(s)
		return indent + spaceRun.ReplaceAllString(strings.TrimRightFunc(s[len(indent):], unicode.IsSpace), " ")
	})
}

// CleanQuotes normalizes curly single/double quotes to ASCII, including curly
// apostrophes. Primes, backticks and Japanese quotation marks are untouched.
func CleanQuotes(text string) string {
	return tidyLines(text, func(s string) string {
		return strings.NewReplacer("‘", "'", "’", "'", "“", "\"", "”", "\"").Replace(s)
	})
}

var tidyBullet = regexp.MustCompile(`^([ \t]*)([-*•・])([ \t]+)(\S.*)$`)

// CleanBullets uses '- ' markers, retaining indentation and marker spacing.
// Require whitespace after the marker; preserve ordered lists, dividers and
// task checkboxes, whose marker can carry application-specific meaning.
func CleanBullets(text string) string {
	return tidyLines(text, func(s string) string {
		m := tidyBullet.FindStringSubmatch(s)
		if m == nil || isDivider(s) || strings.HasPrefix(m[4], "[") {
			return s
		}
		return m[1] + "-" + m[3] + m[4]
	})
}

// Keep the conservative guards local to tidy rather than changing translation
// chunking heuristics. Fenced/indented code, inline code, assignments, tables,
// URLs and email addresses are left byte-for-byte intact by every tidy rule.
var tidyAssignment = regexp.MustCompile(`\b[\w.]+\s*:?=`)

func tidyProtected(line string) bool {
	trimmed := strings.TrimSpace(line)
	return isCodeLine(line) || strings.Contains(line, "`") ||
		tidyAssignment.MatchString(line) || hasCodeCallSyntax(trimmed) || strings.Contains(line, "://") ||
		strings.ContainsAny(line, "@＠") || strings.Contains(strings.TrimRight(line, " \t\u3000"), "\t") ||
		strings.Contains(line, "|") ||
		((strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) && trimmed != "" && !reListMarker.MatchString(line)) ||
		strings.HasPrefix(trimmed, "<!--")
}

// tidyBlocks transforms contiguous unprotected text, preserving line endings
// and fenced blocks, including blank lines inside an unclosed fence.
func tidyBlocks(text string, transform func(string) string) string {
	var out, block strings.Builder
	flush := func() {
		out.WriteString(transform(block.String()))
		block.Reset()
	}
	fence := ""
	for _, raw := range strings.SplitAfter(text, "\n") {
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		trimmed := strings.TrimSpace(line)
		marker := ""
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			n := 0
			for n < len(trimmed) && trimmed[n] == trimmed[0] {
				n++
			}
			marker = trimmed[:n]
		}
		if fence != "" || marker != "" || tidyProtected(line) {
			flush()
			out.WriteString(raw)
			if fence == "" {
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, string(fence[0])) == "" {
				fence = ""
			}
			continue
		}
		block.WriteString(raw)
	}
	flush()
	return out.String()
}

func tidyLines(text string, transform func(string) string) string {
	return tidyBlocks(text, func(block string) string {
		parts := strings.SplitAfter(block, "\n")
		for i, raw := range parts {
			line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
			parts[i] = transform(line) + raw[len(line):]
		}
		return strings.Join(parts, "")
	})
}
