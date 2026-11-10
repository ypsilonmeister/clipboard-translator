package textstruct

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	// reParaSplit matches blank line paragraph boundaries (\n\s*\n)
	reParaSplit = regexp.MustCompile(`\r?\n\s*\r?\n`)

	abbreviations = map[string]bool{
		"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true,
		"sr": true, "jr": true, "vs": true, "etc": true, "al": true,
		"fig": true, "inc": true, "ltd": true, "corp": true, "co": true,
		"approx": true, "dept": true, "est": true, "govt": true,
		"jan": true, "feb": true, "mar": true, "apr": true, "jun": true,
		"jul": true, "aug": true, "sep": true, "oct": true, "nov": true, "dec": true,
	}
)

// SplitParagraphs splits text at blank line boundaries (\r?\n\s*\r?\n) and trims
// empty outer whitespace.
func SplitParagraphs(text string) []string {
	if text == "" {
		return nil
	}
	rawParas := reParaSplit.Split(text, -1)
	var paras []string
	for _, p := range rawParas {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			paras = append(paras, trimmed)
		}
	}
	if len(paras) == 0 && strings.TrimSpace(text) != "" {
		return []string{strings.TrimSpace(text)}
	}
	return paras
}

// JoinSentences joins sentence fragments into a paragraph, inserting a space
// between non-CJK adjacent characters and joining CJK characters directly.
func JoinSentences(sentences []string) string {
	var b strings.Builder
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if b.Len() > 0 {
			prevRunes := []rune(b.String())
			nextRunes := []rune(s)
			if len(prevRunes) > 0 && len(nextRunes) > 0 {
				last := prevRunes[len(prevRunes)-1]
				first := nextRunes[0]
				if !isCJK(last) && !isCJK(first) && !unicode.IsSpace(last) {
					b.WriteByte(' ')
				}
			}
		}
		b.WriteString(s)
	}
	return b.String()
}

// FormatBilingual formats source and translated text. If both texts contain
// multiple matching paragraphs, they are interleaved paragraph-by-paragraph.
// Otherwise, it falls back to joining source and translated with a newline.
func FormatBilingual(source, translated string) string {
	sourceParas := SplitParagraphs(source)
	transParas := SplitParagraphs(translated)
	if len(sourceParas) > 1 && len(sourceParas) == len(transParas) {
		pairs := make([]string, len(sourceParas))
		for i := range sourceParas {
			pairs[i] = strings.TrimRight(sourceParas[i], "\r\n") + "\n" + strings.TrimSpace(transParas[i])
		}
		return strings.Join(pairs, "\n\n")
	}
	return strings.TrimRight(source, "\r\n") + "\n" + translated
}

// ChunkText splits long text exceeding maxChunkChars into coherent semantic segments
// (paragraphs first, sentences second, runes fallback), respecting model context limits.
func ChunkText(text string, maxChunkChars int) []string {
	if text == "" {
		return []string{""}
	}
	if maxChunkChars <= 0 || len([]rune(text)) <= maxChunkChars {
		return []string{text}
	}

	// Step 1: Split at paragraph boundaries (blank lines)
	paras := SplitParagraphs(text)
	if len(paras) == 0 {
		return []string{text}
	}

	var chunks []string
	var currentChunk strings.Builder
	currentLen := 0

	for _, para := range paras {
		paraRunes := len([]rune(para))

		if paraRunes <= maxChunkChars {
			// If adding this paragraph exceeds maxChunkChars, flush currentChunk
			needed := paraRunes
			if currentLen > 0 {
				needed += 2 // "\n\n"
			}

			if currentLen+needed <= maxChunkChars {
				if currentLen > 0 {
					currentChunk.WriteString("\n\n")
				}
				currentChunk.WriteString(para)
				currentLen += needed
			} else {
				if currentChunk.Len() > 0 {
					chunks = append(chunks, currentChunk.String())
					currentChunk.Reset()
					currentLen = 0
				}
				currentChunk.WriteString(para)
				currentLen = paraRunes
			}
		} else {
			// Paragraph exceeds maxChunkChars: flush pending chunk first
			if currentChunk.Len() > 0 {
				chunks = append(chunks, currentChunk.String())
				currentChunk.Reset()
				currentLen = 0
			}

			// If paragraph is a code/structured block, split by lines rather than sentences
			if isCodeBlock(para) {
				subChunks := chunkCodeBlock(para, maxChunkChars)
				chunks = append(chunks, subChunks...)
			} else {
				// Step 2: Split oversized paragraph into sentences
				subChunks := chunkParagraph(para, maxChunkChars)
				chunks = append(chunks, subChunks...)
			}
		}
	}

	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks
}

func isCodeBlock(para string) bool {
	lines := strings.Split(para, "\n")
	codeLines := 0
	for _, l := range lines {
		if isCodeLine(l) || isCodeFence(l) {
			codeLines++
		}
	}
	return codeLines > len(lines)/2
}

// chunkCodeBlock splits an oversized code block at line boundaries.
func chunkCodeBlock(para string, maxChunkChars int) []string {
	lines := strings.Split(para, "\n")
	var chunks []string
	var cur strings.Builder
	curLen := 0

	for _, line := range lines {
		lRunes := len([]rune(line))
		if lRunes <= maxChunkChars {
			needed := lRunes
			if curLen > 0 {
				needed += 1 // "\n"
			}
			if curLen+needed <= maxChunkChars {
				if curLen > 0 {
					cur.WriteString("\n")
				}
				cur.WriteString(line)
				curLen += needed
			} else {
				if cur.Len() > 0 {
					chunks = append(chunks, cur.String())
					cur.Reset()
					curLen = 0
				}
				cur.WriteString(line)
				curLen = lRunes
			}
		} else {
			if cur.Len() > 0 {
				chunks = append(chunks, cur.String())
				cur.Reset()
				curLen = 0
			}
			chunks = append(chunks, splitRuneBudget(line, maxChunkChars)...)
		}
	}

	if cur.Len() > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}

// chunkParagraph splits an oversized paragraph at sentence boundaries.
func chunkParagraph(para string, maxChunkChars int) []string {
	sentences := splitSentences(para)
	if len(sentences) == 0 {
		return splitRuneBudget(para, maxChunkChars)
	}

	var chunks []string
	var cur strings.Builder
	curLen := 0

	for _, s := range sentences {
		sRunes := len([]rune(s))
		if sRunes <= maxChunkChars {
			needed := sRunes
			sep := ""
			if curLen > 0 {
				curStr := cur.String()
				curRunes := []rune(curStr)
				sRunesSlice := []rune(s)
				if len(curRunes) > 0 && len(sRunesSlice) > 0 {
					lastRune := curRunes[len(curRunes)-1]
					firstRune := sRunesSlice[0]
					if !isCJK(lastRune) && !isCJK(firstRune) {
						sep = " "
						needed += 1
					}
				}
			}
			if curLen+needed <= maxChunkChars {
				cur.WriteString(sep)
				cur.WriteString(s)
				curLen += needed
			} else {
				if cur.Len() > 0 {
					chunks = append(chunks, cur.String())
					cur.Reset()
					curLen = 0
				}
				cur.WriteString(s)
				curLen = sRunes
			}
		} else {
			// Sentence itself exceeds maxChunkChars: hard split by clause or runes
			if cur.Len() > 0 {
				chunks = append(chunks, cur.String())
				cur.Reset()
				curLen = 0
			}
			chunks = append(chunks, splitRuneBudget(s, maxChunkChars)...)
		}
	}

	if cur.Len() > 0 {
		chunks = append(chunks, cur.String())
	}

	return chunks
}

func splitSentences(para string) []string {
	var sentences []string
	runes := []rune(para)
	start := 0

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		isBoundary := false

		// CJK sentence boundary: 。！？
		if r == '。' || r == '！' || r == '？' {
			// include trailing closing quotes if any
			for i+1 < len(runes) && isClosingPunctuation(runes[i+1]) {
				i++
			}
			isBoundary = true
		} else if r == '.' || r == '!' || r == '?' {
			// Check if this dot is part of a common abbreviation like "Mr.", "Dr.", "e.g.", "i.e."
			if r == '.' && isAbbreviation(runes, i) {
				continue
			}

			// Latin sentence boundary: followed by whitespace and uppercase/number
			endIdx := i
			for endIdx+1 < len(runes) && isClosingPunctuation(runes[endIdx+1]) {
				endIdx++
			}
			if endIdx+1 < len(runes) && unicode.IsSpace(runes[endIdx+1]) {
				i = endIdx
				isBoundary = true
			}
		}

		if isBoundary {
			s := strings.TrimSpace(string(runes[start : i+1]))
			if s != "" {
				sentences = append(sentences, s)
			}
			start = i + 1
		}
	}

	if start < len(runes) {
		rem := strings.TrimSpace(string(runes[start:]))
		if rem != "" {
			sentences = append(sentences, rem)
		}
	}

	return sentences
}

func isAbbreviation(runes []rune, dotIdx int) bool {
	// Look backwards to extract the token before the period
	start := dotIdx - 1
	for start >= 0 && unicode.IsLetter(runes[start]) {
		start--
	}
	word := strings.ToLower(string(runes[start+1 : dotIdx]))
	if len(word) == 0 {
		return false
	}
	// Single-letter initials (e.g. "J." in "J. Smith")
	if len(word) == 1 {
		return true
	}
	if abbreviations[word] {
		return true
	}
	// Multi-part abbreviations with adjacent periods (e.g. second dot of "e.g.")
	if start >= 1 && runes[start] == '.' {
		return true
	}
	return false
}

// splitRuneBudget slices a string into chunks of at most maxChunkChars runes.
func splitRuneBudget(s string, maxChunkChars int) []string {
	runes := []rune(s)
	if len(runes) <= maxChunkChars {
		return []string{s}
	}

	var parts []string
	for len(runes) > 0 {
		end := maxChunkChars
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, string(runes[:end]))
		runes = runes[end:]
	}
	return parts
}
