package textstruct

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	// reParaSplit matches blank line paragraph boundaries (\n\s*\n)
	reParaSplit = regexp.MustCompile(`\r?\n\s*\r?\n`)
)

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
	rawParas := reParaSplit.Split(text, -1)
	var paras []string
	for _, p := range rawParas {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			paras = append(paras, trimmed)
		}
	}
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

			// Step 2: Split oversized paragraph into sentences
			subChunks := chunkParagraph(para, maxChunkChars)
			chunks = append(chunks, subChunks...)
		}
	}

	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
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
