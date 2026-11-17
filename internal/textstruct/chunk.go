package textstruct

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// reParaSplit matches blank line paragraph boundaries (\n\s*\n)
	reParaSplit = regexp.MustCompile(`\r?\n\s*\r?\n`)

	abbreviations = map[string]bool{
		"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true,
		"sr": true, "jr": true, "vs": true, "v": true, "etc": true, "al": true,
		"fig": true, "figs": true, "eq": true, "eqs": true,
		"vol": true, "vols": true, "no": true, "nos": true, "nr": true,
		"pp": true, "p": true, "ch": true, "chs": true, "chap": true, "chaps": true,
		"sec": true, "secs": true, "cf": true, "ca": true,
		"ed": true, "eds": true, "ref": true, "refs": true,
		"op": true, "cit": true, "ibid": true,
		"inc": true, "ltd": true, "corp": true, "co": true,
		"approx": true, "dept": true, "depts": true, "est": true, "govt": true,
		"min": true, "max": true, "st": true, "ave": true, "blvd": true, "rd": true,
		"jan": true, "feb": true, "mar": true, "apr": true, "jun": true,
		"jul": true, "aug": true, "sep": true, "oct": true, "nov": true, "dec": true,
	}

	// strictTitleAbbreviations always attach to a following capitalized name or reference
	// and do not end sentences in normal prose.
	strictTitleAbbreviations = map[string]bool{
		"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true,
		"vs": true, "cf": true,
	}

	// contextualTitleAbbreviations attach to a following capitalized proper noun
	// (e.g. "St. Louis", "Eds. Smith", "Smith v. Jones", "John Jr.") unless the
	// next word is a common sentence starter (e.g. "Main St. Then we turned left.").
	contextualTitleAbbreviations = map[string]bool{
		"sr": true, "jr": true, "st": true, "v": true, "eds": true,
	}

	// docRefAbbreviations are abbreviations for figures, equations, sections, etc.,
	// that can attach to a following capital-letter or alphanumeric reference identifier
	// (e.g. "Fig. S1", "Eq. A1", "Figs. A and B", "Vol. II").
	docRefAbbreviations = map[string]bool{
		"fig": true, "figs": true, "eq": true, "eqs": true,
		"vol": true, "vols": true, "no": true, "nos": true, "nr": true,
		"pp": true, "p": true, "ch": true, "chs": true, "chap": true,
		"chaps": true, "sec": true, "secs": true, "ref": true, "refs": true,
	}

	// exampleIntroducerAbbreviations are multipart abbreviations that introduce
	// examples or clarifications and do not terminate a sentence, even when followed
	// by capitalized proper nouns or capitalized sentence-starter words (e.g. "e.g. The Times", "i.e. The Hague").
	exampleIntroducerAbbreviations = map[string]bool{
		"e.g": true,
		"i.e": true,
	}

	reRomanNumeral = regexp.MustCompile(`^(?i:M{0,3}(?:CM|CD|D?C{0,3})(?:XC|XL|L?X{0,3})(?:IX|IV|V?I{0,3}))$`)

	sentenceStarterWords = map[string]bool{
		"the": true, "then": true, "this": true, "that": true, "these": true, "those": true,
		"there": true, "they": true, "their": true, "it": true, "its": true, "we": true,
		"our": true, "he": true, "his": true, "she": true, "her": true, "you": true,
		"your": true, "i": true, "in": true, "on": true, "at": true, "for": true,
		"from": true, "to": true, "with": true, "without": true, "by": true, "after": true,
		"before": true, "during": true, "while": true, "when": true, "where": true,
		"why": true, "how": true, "what": true, "which": true, "who": true, "if": true,
		"unless": true, "until": true, "although": true, "though": true, "because": true,
		"since": true, "as": true, "so": true, "but": true, "and": true, "or": true,
		"nor": true, "yet": true, "however": true, "therefore": true, "furthermore": true,
		"moreover": true, "nevertheless": true, "nonetheless": true, "meanwhile": true,
		"instead": true, "otherwise": true, "consequently": true, "subsequently": true,
		"accordingly": true, "thus": true, "hence": true, "next": true, "first": true,
		"second": true, "third": true, "finally": true, "last": true, "more": true,
		"most": true, "some": true, "many": true, "several": true, "each": true,
		"every": true, "all": true, "both": true, "neither": true, "either": true,
		"another": true, "other": true, "such": true, "here": true, "now": true,
		"soon": true, "later": true, "today": true, "yesterday": true, "tomorrow": true,
		"yes": true, "no": true, "please": true, "see": true, "note": true,
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
	// Retain byte ranges, including indentation, trailing spaces and original
	// separators. Only the model-facing paragraph splitter should trim text.
	var sourceParas [][2]int
	start := 0
	for _, boundary := range reParaSplit.FindAllStringIndex(source, -1) {
		if strings.TrimSpace(source[start:boundary[0]]) != "" {
			sourceParas = append(sourceParas, [2]int{start, boundary[0]})
		}
		start = boundary[1]
	}
	if strings.TrimSpace(source[start:]) != "" {
		sourceParas = append(sourceParas, [2]int{start, len(source)})
	}
	transParas := SplitParagraphs(translated)
	if len(sourceParas) > 1 && len(sourceParas) == len(transParas) {
		var out strings.Builder
		cursor := 0
		for i, span := range sourceParas {
			out.WriteString(source[cursor:span[1]])
			out.WriteByte('\n')
			out.WriteString(transParas[i])
			cursor = span[1]
		}
		out.WriteString(source[cursor:])
		return out.String()
	}
	return strings.TrimRight(source, "\r\n") + "\n" + translated
}

// Boundary describes how two adjacent sub-chunks of a paragraph were separated
// before chunking, so recombination can restore separators lost to response
// trimming without inventing spaces inside hard-split tokens.
type Boundary uint8

const (
	// BoundaryNone is a hard rune-budget split with no whitespace on either side.
	BoundaryNone Boundary = iota
	// BoundarySpace is an intra-line split where whitespace existed at the source boundary.
	BoundarySpace
	// BoundaryNewline is an explicit line break ("\n") between lines.
	BoundaryNewline
	// BoundarySentence is a sentence boundary within a single-line paragraph.
	BoundarySentence
)

func splitBoundary(left, right string) Boundary {
	if hasTrailingSpace(left) || hasLeadingSpace(right) {
		return BoundarySpace
	}
	return BoundaryNone
}

func hasTrailingSpace(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(r)
}

func hasLeadingSpace(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r)
}

func shouldInsertSpace(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(left)
	first, _ := utf8.DecodeRuneInString(right)
	if unicode.IsSpace(last) || unicode.IsSpace(first) {
		return false
	}
	return !isCJK(last) && !isCJK(first)
}

// JoinChunks reassembles translated sub-chunks of a paragraph according to the
// boundary metadata recorded when the paragraph was split. Each boundaries[i-1]
// describes the boundary before chunks[i]. For BoundarySpace and
// BoundarySentence, a space is inserted only when both adjacent pieces are
// non-empty, neither edge already carries whitespace, and neither adjacent
// rune is CJK. Any extra chunks beyond len(boundaries)+1 are concatenated
// directly without a separator.
func JoinChunks(chunks []string, boundaries []Boundary) string {
	var out strings.Builder
	for i, chunk := range chunks {
		if i > 0 && i-1 < len(boundaries) {
			switch boundaries[i-1] {
			case BoundaryNewline:
				out.WriteByte('\n')
			case BoundarySpace, BoundarySentence:
				if shouldInsertSpace(out.String(), chunk) {
					out.WriteByte(' ')
				}
			case BoundaryNone:
			}
		}
		out.WriteString(chunk)
	}
	return out.String()
}

// ChunkLines keeps meaningful newlines when a paragraph spans multiple lines.
// Sentence/rune fallback is still used for prose without line boundaries.
func ChunkLines(text string, budget int) []string {
	chunks, _ := ChunkLinesWithBoundaries(text, budget)
	return chunks
}

// ChunkLinesWithBoundaries splits a multi-line paragraph into chunks up to
// budget runes and records the boundary before each chunk after the first.
func ChunkLinesWithBoundaries(text string, budget int) ([]string, []Boundary) {
	if budget <= 0 || len([]rune(text)) <= budget {
		return []string{text}, nil
	}
	return chunkCodeBlockWithBoundaries(text, budget)
}

// ChunkParagraphWithBoundaries splits a single oversized paragraph into chunks
// up to maxChunkChars runes and records the boundary before each chunk after
// the first.
func ChunkParagraphWithBoundaries(para string, maxChunkChars int) ([]string, []Boundary) {
	if maxChunkChars <= 0 || len([]rune(para)) <= maxChunkChars {
		return []string{para}, nil
	}
	if isCodeBlock(para) {
		return chunkCodeBlockWithBoundaries(para, maxChunkChars)
	}
	return chunkParagraphWithBoundaries(para, maxChunkChars)
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

func appendChunkWithBoundary(chunks *[]string, boundaries *[]Boundary, chunk string, b Boundary) {
	if len(*chunks) > 0 {
		*boundaries = append(*boundaries, b)
	}
	*chunks = append(*chunks, chunk)
}

// chunkCodeBlock splits an oversized code block at line boundaries.
func chunkCodeBlock(para string, maxChunkChars int) []string {
	chunks, _ := chunkCodeBlockWithBoundaries(para, maxChunkChars)
	return chunks
}

// chunkCodeBlockWithBoundaries splits an oversized multi-line block at line
// boundaries (and rune budget within oversized lines), recording the boundary
// before each chunk after the first.
func chunkCodeBlockWithBoundaries(para string, maxChunkChars int) ([]string, []Boundary) {
	lines := strings.Split(para, "\n")
	var chunks []string
	var boundaries []Boundary
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
					appendChunkWithBoundary(&chunks, &boundaries, cur.String(), BoundaryNewline)
					cur.Reset()
					curLen = 0
				}
				cur.WriteString(line)
				curLen = lRunes
			}
		} else {
			if cur.Len() > 0 {
				appendChunkWithBoundary(&chunks, &boundaries, cur.String(), BoundaryNewline)
				cur.Reset()
				curLen = 0
			}
			// Keep a trailing '\r' from CRLF attached to the line's final chunk so
			// it does not become a standalone whitespace-only chunk.
			content := strings.TrimSuffix(line, "\r")
			cr := line[len(content):]
			var parts []string
			if cr != "" && len([]rune(content)) <= maxChunkChars {
				parts = []string{line}
			} else {
				parts = splitRuneBudget(content, maxChunkChars)
				if cr != "" && len(parts) > 0 {
					parts[len(parts)-1] += cr
				}
			}
			for i, part := range parts {
				b := BoundaryNewline
				if i > 0 {
					b = splitBoundary(parts[i-1], part)
				}
				appendChunkWithBoundary(&chunks, &boundaries, part, b)
			}
		}
	}

	if cur.Len() > 0 {
		appendChunkWithBoundary(&chunks, &boundaries, cur.String(), BoundaryNewline)
	}
	return chunks, boundaries
}

// chunkParagraph splits an oversized paragraph at sentence boundaries.
func chunkParagraph(para string, maxChunkChars int) []string {
	chunks, _ := chunkParagraphWithBoundaries(para, maxChunkChars)
	return chunks
}

// chunkParagraphWithBoundaries splits an oversized paragraph at sentence
// boundaries (and rune budget within oversized sentences), recording the
// boundary before each chunk after the first.
func chunkParagraphWithBoundaries(para string, maxChunkChars int) ([]string, []Boundary) {
	sentences := splitSentences(para)
	if len(sentences) == 0 {
		parts := splitRuneBudget(para, maxChunkChars)
		var boundaries []Boundary
		for i := 1; i < len(parts); i++ {
			boundaries = append(boundaries, splitBoundary(parts[i-1], parts[i]))
		}
		return parts, boundaries
	}

	var chunks []string
	var boundaries []Boundary
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
					appendChunkWithBoundary(&chunks, &boundaries, cur.String(), BoundarySentence)
					cur.Reset()
					curLen = 0
				}
				cur.WriteString(s)
				curLen = sRunes
			}
		} else {
			// Sentence itself exceeds maxChunkChars: hard split by clause or runes
			if cur.Len() > 0 {
				appendChunkWithBoundary(&chunks, &boundaries, cur.String(), BoundarySentence)
				cur.Reset()
				curLen = 0
			}
			parts := splitRuneBudget(s, maxChunkChars)
			for i, part := range parts {
				b := BoundarySentence
				if i > 0 {
					b = splitBoundary(parts[i-1], part)
				}
				appendChunkWithBoundary(&chunks, &boundaries, part, b)
			}
		}
	}

	if cur.Len() > 0 {
		appendChunkWithBoundary(&chunks, &boundaries, cur.String(), BoundarySentence)
	}

	return chunks, boundaries
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
				if r == '.' {
					nextIdx := endIdx + 1
					for nextIdx < len(runes) && unicode.IsSpace(runes[nextIdx]) {
						nextIdx++
					}
					if nextIdx < len(runes) && unicode.IsLower(runes[nextIdx]) {
						i = endIdx
						continue
					}
				}
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
	// Consecutive periods (e.g. ellipsis "...") are not sentence boundaries by themselves.
	if (dotIdx >= 1 && runes[dotIdx-1] == '.') || (dotIdx+1 < len(runes) && runes[dotIdx+1] == '.') {
		return true
	}
	// Look backwards to extract the token before the period
	start := dotIdx - 1
	for start >= 0 && unicode.IsLetter(runes[start]) {
		start--
	}
	// Alphanumeric suffixes such as "1a." or "2b." are item identifiers, not initials.
	if start >= 0 && unicode.IsDigit(runes[start]) {
		return false
	}
	word := strings.ToLower(string(runes[start+1 : dotIdx]))
	if len(word) == 0 {
		return false
	}

	isMultipart := start >= 1 && runes[start] == '.'
	var fullMultipart string
	if isMultipart {
		abbrevStart := start
		for abbrevStart > 0 && runes[abbrevStart] == '.' {
			p := abbrevStart - 1
			for p >= 0 && unicode.IsLetter(runes[p]) {
				p--
			}
			abbrevStart = p + 1
			if p >= 0 && runes[p] == '.' {
				abbrevStart = p
			}
		}
		fullMultipart = strings.ToLower(string(runes[abbrevStart:dotIdx]))
	}

	endIdx := dotIdx
	for endIdx+1 < len(runes) && isClosingPunctuation(runes[endIdx+1]) {
		endIdx++
	}
	nextIdx := endIdx + 1
	for nextIdx < len(runes) && unicode.IsSpace(runes[nextIdx]) {
		nextIdx++
	}

	if nextIdx >= len(runes) || nextIdx == endIdx+1 || unicode.IsLower(runes[nextIdx]) || unicode.IsDigit(runes[nextIdx]) {
		return len(word) == 1 || abbreviations[word] || isMultipart
	}

	if unicode.IsUpper(runes[nextIdx]) {
		endNext := nextIdx
		for endNext < len(runes) && (unicode.IsLetter(runes[endNext]) || unicode.IsDigit(runes[endNext])) {
			endNext++
		}
		nextToken := string(runes[nextIdx:endNext])
		nextWord := strings.ToLower(nextToken)
		if strictTitleAbbreviations[word] {
			return true
		}
		if isMultipart {
			if exampleIntroducerAbbreviations[fullMultipart] {
				return true
			}
			return !sentenceStarterWords[nextWord]
		}
		if docRefAbbreviations[word] && isReferenceIdentifier(word, nextToken) {
			return true
		}
		if contextualTitleAbbreviations[word] || (len(word) == 1 && unicode.IsUpper(runes[dotIdx-1])) {
			return !sentenceStarterWords[nextWord]
		}
		return false
	}

	return len(word) == 1 || abbreviations[word] || isMultipart
}

func isReferenceIdentifier(word, token string) bool {
	if len(token) == 0 {
		return false
	}
	runes := []rune(token)

	// "no" and "nos" denote numbers, not lettered sections or figures.
	// In prose, "No. I ..." and "No. A ..." are ordinary responses where "No." is followed
	// by the pronoun "I" or article "A". A single capital letter is never a valid number identifier.
	if word == "no" || word == "nos" {
		hasDigit := false
		for _, r := range runes {
			if unicode.IsDigit(r) {
				hasDigit = true
				break
			}
		}
		if hasDigit {
			return true
		}
		if len(runes) > 1 && reRomanNumeral.MatchString(token) {
			return true
		}
		return false
	}

	// Single uppercase letter: e.g. "A" in "Figs. A and B"
	if len(runes) == 1 && unicode.IsUpper(runes[0]) {
		return true
	}
	// Contains digits: e.g. "S1", "A1", "B2"
	for _, r := range runes {
		if unicode.IsDigit(r) {
			return true
		}
	}
	// Roman numerals: e.g. "I", "II", "III", "IV", "V", "VI", etc.
	if reRomanNumeral.MatchString(token) {
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
