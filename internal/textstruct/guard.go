package textstruct

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	// reBlockQuote matches lines starting with '>' (e.g. "> quote", ">> nested")
	reBlockQuote = regexp.MustCompile(`^\s*>`)

	// reListMarker matches unordered (- * • ・), ordered/lettered (1. a. iv.), and parenthesized ((1) (a) (i)) lists
	reListMarker = regexp.MustCompile(`^\s*([-*•・]|\d+[.)]|[a-zA-Z][.)]|(?i:[ivxlcdm]+)[.)]|\(\d+\)|\([a-zA-Z]\)|\((?i:[ivxlcdm]+)\))\s+`)

	// reEmailSig matches standard email signature separator ("-- ")
	reEmailSig = regexp.MustCompile(`^--\s*$`)

	// reArrayDecl matches array declarations like "Type[] name;" or "Type[10] name;"
	reArrayDecl = regexp.MustCompile(`\b[A-Za-z_]\w*(?:\[\s*\d*\s*\])+\s+[A-Za-z_]\w*`)

	// reGenericDecl matches generic declarations like "List<string> name;" or "Map<K, V> name;"
	reGenericDecl = regexp.MustCompile(`\b[A-Za-z_]\w*<[A-Za-z0-9_,\s<>\[\]*&]+>\s+[A-Za-z_]\w*`)
)

// isPreservedLine reports whether line is a structural element that must not be flattened.
func isPreservedLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true // Blank line
	}
	if reBlockQuote.MatchString(line) {
		return true
	}
	if reListMarker.MatchString(line) {
		return true
	}
	if isCodeFence(line) {
		return true
	}
	if isDivider(line) {
		return true
	}
	if reEmailSig.MatchString(line) {
		return true
	}
	if isURL(line) {
		return true
	}
	if isTableRow(trimmed) {
		return true
	}
	if isCodeLine(line) {
		return true
	}
	return false
}

func isCodeFence(s string) bool {
	trimmed := strings.TrimSpace(s)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") || strings.HasPrefix(trimmed, "<!--")
}

func isDivider(s string) bool {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < 3 {
		return false
	}
	first := trimmed[0]
	if first != '-' && first != '=' && first != '_' && first != '*' && first != '~' {
		return false
	}
	count := 0
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] == first {
			count++
		} else if trimmed[i] != ' ' && trimmed[i] != '\t' {
			return false
		}
	}
	return count >= 3
}

func isURL(s string) bool {
	trimmed := strings.TrimSpace(s)
	return strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")
}

// isTableRow reports whether trimmed looks like a pipe-delimited or column-aligned table row.
func isTableRow(trimmed string) bool {
	if strings.HasPrefix(trimmed, "|") || strings.HasSuffix(trimmed, "|") || strings.Contains(trimmed, " | ") {
		return true
	}
	return strings.Contains(trimmed, "\t") || strings.Contains(trimmed, "   ")
}

// isCodeLine checks if a line looks like source code.
func isCodeLine(line string) bool {
	// Leading tab or 4+ spaces
	isIndented := strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ")
	trimmed := strings.TrimSpace(line)

	// Single line comments
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
		return true
	}

	// Code braces / keywords
	if strings.HasSuffix(trimmed, "{") || strings.HasSuffix(trimmed, "}") ||
		strings.HasPrefix(trimmed, "}") {
		return true
	}

	// Trailing semicolon is only treated as code when indented or accompanied by
	// code syntax, so prose lines wrapped at a semicolon can still join.
	if strings.HasSuffix(trimmed, ";") && (isIndented || isCodeStatementWithSemicolon(trimmed)) {
		return true
	}

	// Go / C code keywords if indented
	if isIndented {
		keywords := []string{
			"func ", "return", "if ", "else", "for ", "switch ", "case ",
			"var ", "const ", "type ", "struct ", "package ", "import ",
			"nil", "err", ":=", "==", "!=", "&&", "||", "/*", "*/",
		}
		for _, kw := range keywords {
			if strings.Contains(line, kw) {
				return true
			}
		}
	}

	// Go function declaration on line 1 without indent: e.g. "func applySettings(form Form) error {"
	if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "package ") || strings.HasPrefix(trimmed, "import ") {
		return true
	}

	return false
}

func isCodeStatementWithSemicolon(trimmed string) bool {
	if strings.ContainsAny(trimmed, "={}") ||
		strings.Contains(trimmed, "<<") ||
		strings.Contains(trimmed, ">>") ||
		strings.Contains(trimmed, "++") ||
		strings.Contains(trimmed, "--") ||
		strings.Contains(trimmed, "->") ||
		strings.Contains(trimmed, "::") ||
		reArrayDecl.MatchString(trimmed) ||
		reGenericDecl.MatchString(trimmed) ||
		hasCodeCallSyntax(trimmed) {
		return true
	}
	for _, kw := range []string{
		"return;", "break;", "continue;",
		"return ", "break ", "continue ",
		"var ", "const ", "type ", "import ", "package ",
		"let ", "int ", "char ", "bool ", "void ", "float ", "double ",
		"struct ", "typedef ", "using ", "namespace ", "throw ",
		"public ", "private ", "protected ", "internal ", "static ", "readonly ",
	} {
		if strings.HasPrefix(trimmed, kw) {
			return true
		}
	}
	return false
}

func hasCodeCallSyntax(s string) bool {
	prevIdent := false
	for _, r := range s {
		if r == '(' && prevIdent {
			return true
		}
		prevIdent = unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
	}
	return false
}

// isShortSignOff reports if line is a short closing phrase (e.g. "Sincerely,", "Best regards,")
func isShortSignOff(trimmed string) bool {
	runes := []rune(trimmed)
	if len(runes) > 0 && len(runes) < 20 {
		last := runes[len(runes)-1]
		if last == ',' || last == ':' {
			return true
		}
	}
	return false
}
