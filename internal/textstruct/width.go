package textstruct

import "unicode"

// runeWidth returns the visual display column width of a rune.
// Half-width (ASCII, Latin, etc.) = 1, Full-width (CJK Han, Kana, etc.) = 2,
// tab = 4, controls = 0.
func runeWidth(r rune) int {
	if r == '\t' {
		return 4
	}
	if unicode.IsControl(r) {
		return 0
	}
	if isCJK(r) {
		return 2
	}
	return 1
}

// stringWidth calculates the total visual display width of a string.
func stringWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// isCJK reports whether a rune belongs to East Asian / CJK wide scripts.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) ||
		(r >= 0x3000 && r <= 0x303F) || // CJK Symbols and Punctuation
		(r >= 0xFF01 && r <= 0xFF60) || // Fullwidth Forms
		(r >= 0xFFE0 && r <= 0xFFE6) // Fullwidth Signs
}

// isLatin reports whether a rune belongs to Latin script.
func isLatin(r rune) bool {
	return unicode.Is(unicode.Latin, r)
}
