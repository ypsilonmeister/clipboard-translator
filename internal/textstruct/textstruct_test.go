package textstruct

import (
	"reflect"
	"strings"
	"testing"
)

// Fixture 1: PDF Column Wrap (English Prose)
func TestFixture1_PDFColumnWrapEnglish(t *testing.T) {
	input := `The proposed architecture introduces a novel transformer-
based attention mechanism that significantly improves trans-
lation quality on resource-constrained edge devices.
Furthermore, the computational latency is reduced by 40%
without observable loss in accuracy.`

	expected := `The proposed architecture introduces a novel transformer-based attention mechanism that significantly improves trans-lation quality on resource-constrained edge devices. Furthermore, the computational latency is reduced by 40% without observable loss in accuracy.`

	got := CleanSpuriousNewlines(input, "en")
	if got != expected {
		t.Errorf("CleanSpuriousNewlines mismatch:\nGOT:\n%s\nWANT:\n%s", got, expected)
	}
}

// Fixture 2: PDF Column Wrap (Japanese Prose)
func TestFixture2_PDFColumnWrapJapanese(t *testing.T) {
	input := `本研究では、小型エッジデバイス上で高速かつ高精度に
動作する翻訳モデルの実現を目的とし、パラメータ削減と
量子化手法の適用を行った。
その結果、推論速度の大幅な向上が確認され、実用的な
応答性を達成することができた。`

	expected := `本研究では、小型エッジデバイス上で高速かつ高精度に動作する翻訳モデルの実現を目的とし、パラメータ削減と量子化手法の適用を行った。その結果、推論速度の大幅な向上が確認され、実用的な応答性を達成することができた。`

	got := CleanSpuriousNewlines(input, "ja")
	if got != expected {
		t.Errorf("CleanSpuriousNewlines mismatch:\nGOT:\n%s\nWANT:\n%s", got, expected)
	}
}

// Fixture 3: Quoted Email Reply with >
func TestFixture3_QuotedEmailReply(t *testing.T) {
	input := `> Thanks for the update on the release schedule.
> Are we still on track for Friday?

Yes, everything looks good on our end. We just
finished the final regression suite.

> Please let me know if you need anything from QA.

All tests passed cleanly.`

	expected := `> Thanks for the update on the release schedule.
> Are we still on track for Friday?

Yes, everything looks good on our end. We just finished the final regression suite.

> Please let me know if you need anything from QA.

All tests passed cleanly.`

	got := CleanSpuriousNewlines(input, "en")
	if got != expected {
		t.Errorf("CleanSpuriousNewlines mismatch:\nGOT:\n%s\nWANT:\n%s", got, expected)
	}
}

// Fixture 4: Bulleted / Numbered List
func TestFixture4_BulletedNumberedList(t *testing.T) {
	input := `Please complete the following checklist before merging:
* Run gofmt -l to ensure formatting passes
* Execute go test -race ./...
* Verify Windows GUI build without console popup
* Update documentation:
  1. README.md
  2. README.ja.md`

	expected := input // All breaks strictly preserved

	got := CleanSpuriousNewlines(input, "en")
	if got != expected {
		t.Errorf("CleanSpuriousNewlines mismatch:\nGOT:\n%s\nWANT:\n%s", got, expected)
	}
}

// Fixture 5: Code Block & Indentation
func TestFixture5_CodeBlockIndentation(t *testing.T) {
	input := `func applySettings(form Form) error {
	if form.MaxChars <= 0 {
		return errors.New("invalid limit")
	}
	// Note: keep revisions consistent across saves
	return nil
}`

	expected := input // All breaks strictly preserved

	got := CleanSpuriousNewlines(input, "en")
	if got != expected {
		t.Errorf("CleanSpuriousNewlines mismatch:\nGOT:\n%s\nWANT:\n%s", got, expected)
	}
}

// Fixture 6: Email Sign-off & Signature
func TestFixture6_EmailSignOffSignature(t *testing.T) {
	input := `We look forward to discussing the partnership proposal in detail
during our scheduled call next Tuesday.

Sincerely,
Taro Yamada
Senior Director of Engineering
Clipboard Translator Team
-- 
https://github.com/ypsilonmeister/clipboard-translator`

	expected := `We look forward to discussing the partnership proposal in detail during our scheduled call next Tuesday.

Sincerely,
Taro Yamada
Senior Director of Engineering
Clipboard Translator Team
-- 
https://github.com/ypsilonmeister/clipboard-translator`

	got := CleanSpuriousNewlines(input, "en")
	if got != expected {
		t.Errorf("CleanSpuriousNewlines mismatch:\nGOT:\n%s\nWANT:\n%s", got, expected)
	}
}

// Fixture 7: Japanese & English Mixed (Bilingual Document Wrap)
func TestFixture7_JapaneseEnglishMixed(t *testing.T) {
	input := `New version v1.2.0 is now available. This release includes several
important bug fixes and performance improvements.
本バージョンでは、メモリ使用効率の改善と起動速度の高速化を
行いました。詳細は GitHub の Release ノートをご確認ください。`

	expected := `New version v1.2.0 is now available. This release includes several important bug fixes and performance improvements.
本バージョンでは、メモリ使用効率の改善と起動速度の高速化を行いました。詳細は GitHub の Release ノートをご確認ください。`

	got := CleanSpuriousNewlines(input, "")
	if got != expected {
		t.Errorf("CleanSpuriousNewlines mismatch:\nGOT:\n%s\nWANT:\n%s", got, expected)
	}
}

// Negative Fixtures: Short lines where meaningful breaks MUST be preserved (Asymmetry of Failure)
func TestNegativeFixtures_ShortLinesNeverFlattened(t *testing.T) {
	tests := []struct {
		name  string
		input string
		lang  string
	}{
		{
			name: "Unmarked English List",
			input: `apples
oranges
pears
bananas`,
			lang: "en",
		},
		{
			name: "Japanese Noun List",
			input: `りんご
みかん
バナナ
ぶどう`,
			lang: "ja",
		},
		{
			name: "Street Address",
			input: `John Smith
123 Main Street
Apt 4B
New York, NY 10001`,
			lang: "en",
		},
		{
			name: "Short Chat / Dialogue Lines",
			input: `no way
home now`,
			lang: "en",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CleanSpuriousNewlines(tc.input, tc.lang)
			if got != tc.input {
				t.Errorf("negative fixture flattened:\nGOT:\n%s\nWANT:\n%s", got, tc.input)
			}
		})
	}
}

// Edge Cases for CleanSpuriousNewlines
func TestCleanSpuriousNewlines_EdgeCases(t *testing.T) {
	if got := CleanSpuriousNewlines("", ""); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	single := "Single line of text without any line breaks."
	if got := CleanSpuriousNewlines(single, ""); got != single {
		t.Errorf("expected single line preserved, got %q", got)
	}

	// CRLF preserved
	crlfInput := "Line one\r\nLine two is wrapped\r\nand continues here."
	crlfGot := CleanSpuriousNewlines(crlfInput, "en")
	if !strings.Contains(crlfGot, "\r\n") && strings.Contains(crlfGot, "\n") {
		t.Errorf("expected CRLF line endings preserved, got %q", crlfGot)
	}
}

// ChunkText Tests
func TestChunkText_UnderBudget(t *testing.T) {
	text := "Short text that easily fits in one chunk."
	chunks := ChunkText(text, 1200)
	if len(chunks) != 1 || chunks[0] != text {
		t.Fatalf("expected 1 chunk with full text, got %v", chunks)
	}
}

func TestChunkText_Paragraphs(t *testing.T) {
	para1 := "First paragraph with some text."
	para2 := "Second paragraph with other text."
	para3 := "Third paragraph with more text."
	input := para1 + "\n\n" + para2 + "\n\n" + para3

	// Budget that fits para1 and para2, but not para3
	budget := len([]rune(para1)) + len([]rune(para2)) + 10
	chunks := ChunkText(input, budget)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
	expectedChunk1 := para1 + "\n\n" + para2
	if chunks[0] != expectedChunk1 {
		t.Errorf("chunk[0] mismatch:\nGOT: %q\nWANT: %q", chunks[0], expectedChunk1)
	}
	if chunks[1] != para3 {
		t.Errorf("chunk[1] mismatch:\nGOT: %q\nWANT: %q", chunks[1], para3)
	}
}

func TestChunkText_OversizedParagraphSentences(t *testing.T) {
	s1 := "Sentence number one is about something interesting."
	s2 := "Sentence number two adds crucial context to sentence one."
	s3 := "Sentence number three concludes the whole thought."
	input := s1 + " " + s2 + " " + s3

	budget := len([]rune(s1)) + len([]rune(s2)) + 2
	chunks := ChunkText(input, budget)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
	if chunks[0] != s1+" "+s2 {
		t.Errorf("chunk[0] mismatch:\nGOT: %q\nWANT: %q", chunks[0], s1+" "+s2)
	}
	if chunks[1] != s3 {
		t.Errorf("chunk[1] mismatch:\nGOT: %q\nWANT: %q", chunks[1], s3)
	}
}

func TestChunkText_AbbreviationNotSplit(t *testing.T) {
	s1 := "Mr. Smith went to Washington with Dr. Jones to discuss policy."
	s2 := "After the meeting, they returned to New York."
	input := s1 + " " + s2

	// Set budget to fit s1 exactly, ensuring it splits at the true sentence boundary
	budget := len([]rune(s1)) + 5
	chunks := ChunkText(input, budget)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
	if chunks[0] != s1 {
		t.Errorf("chunk[0] split at abbreviation:\nGOT:  %q\nWANT: %q", chunks[0], s1)
	}
	if chunks[1] != s2 {
		t.Errorf("chunk[1] mismatch:\nGOT:  %q\nWANT: %q", chunks[1], s2)
	}
}

func TestChunkText_JapaneseSentences(t *testing.T) {
	s1 := "これは最初の文であり、背景を説明しています。"
	s2 := "これは二番目の文であり、詳細な実験結果を述べています。"
	s3 := "最後に三番目の文で結論をまとめます。"
	input := s1 + s2 + s3

	budget := len([]rune(s1)) + len([]rune(s2)) + 1
	chunks := ChunkText(input, budget)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
	if chunks[0] != s1+s2 {
		t.Errorf("chunk[0] mismatch:\nGOT: %q\nWANT: %q", chunks[0], s1+s2)
	}
	if chunks[1] != s3 {
		t.Errorf("chunk[1] mismatch:\nGOT: %q\nWANT: %q", chunks[1], s3)
	}
}

func TestChunkText_CodeBlockSplitAtLineBoundaries(t *testing.T) {
	code := `func example() {
	x := "hello world"
	y := "this is a long string that must not be split in half"
	return x + y
}`
	lines := strings.Split(code, "\n")
	budget := len([]rune(lines[0])) + len([]rune(lines[1])) + 2

	chunks := ChunkText(code, budget)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d: %v", len(chunks), chunks)
	}
	// Check that line boundaries are respected, no mid-line slice
	for _, chunk := range chunks {
		for _, line := range strings.Split(chunk, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.Contains(code, trimmed) {
				t.Errorf("code chunk corrupted or sliced mid-line: %q", line)
			}
		}
	}
}

func TestChunkText_GiantSentenceRuneBudget(t *testing.T) {
	// A single giant continuous string with no sentence delimiters
	input := strings.Repeat("A", 100)
	chunks := ChunkText(input, 30)
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks for 100 runes with budget 30, got %d: %v", len(chunks), chunks)
	}
	expected := []string{
		strings.Repeat("A", 30),
		strings.Repeat("A", 30),
		strings.Repeat("A", 30),
		strings.Repeat("A", 10),
	}
	if !reflect.DeepEqual(chunks, expected) {
		t.Errorf("chunks mismatch:\nGOT:  %v\nWANT: %v", chunks, expected)
	}
}

func TestSplitParagraphs(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"whitespace", "   \r\n\t  ", nil},
		{"single paragraph", "Hello world", []string{"Hello world"}},
		{"two paragraphs LF", "Para 1\n\nPara 2", []string{"Para 1", "Para 2"}},
		{"two paragraphs CRLF", "Para 1\r\n\r\nPara 2", []string{"Para 1", "Para 2"}},
		{"three paragraphs with extra spaces", "P1\n  \n\nP2\n\n\n  P3  ", []string{"P1", "P2", "P3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitParagraphs(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("SplitParagraphs(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestJoinSentences(t *testing.T) {
	cases := []struct {
		name      string
		sentences []string
		want      string
	}{
		{"empty", nil, ""},
		{"single", []string{"Hello world."}, "Hello world."},
		{"english sentences", []string{"Sentence one.", "Sentence two."}, "Sentence one. Sentence two."},
		{"japanese sentences", []string{"文1です。", "文2です。"}, "文1です。文2です。"},
		{"mixed sentences", []string{"Sentence one.", "文2です。"}, "Sentence one.文2です。"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := JoinSentences(tc.sentences)
			if got != tc.want {
				t.Errorf("JoinSentences(%v) = %q, want %q", tc.sentences, got, tc.want)
			}
		})
	}
}

func TestFormatBilingual(t *testing.T) {
	cases := []struct {
		name               string
		source, translated string
		want               string
	}{
		{
			name:       "single paragraph plain",
			source:     "Hello world",
			translated: "こんにちは世界",
			want:       "Hello world\nこんにちは世界",
		},
		{
			name:       "multiple matching paragraphs interleaved",
			source:     "First paragraph.\n\nSecond paragraph.",
			translated: "最初の段落。\n\n2番目の段落。",
			want:       "First paragraph.\n最初の段落。\n\nSecond paragraph.\n2番目の段落。",
		},
		{
			name:       "three matching paragraphs with CRLF",
			source:     "P1\r\n\r\nP2\r\n\r\nP3",
			translated: "T1\n\nT2\n\nT3",
			want:       "P1\nT1\r\n\r\nP2\nT2\r\n\r\nP3\nT3",
		},
		{
			name:       "mismatched paragraph count falls back to plain join",
			source:     "P1\n\nP2\n\nP3",
			translated: "T1\n\nT2",
			want:       "P1\n\nP2\n\nP3\nT1\n\nT2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatBilingual(tc.source, tc.translated)
			if got != tc.want {
				t.Errorf("FormatBilingual(%q, %q) = %q, want %q", tc.source, tc.translated, got, tc.want)
			}
		})
	}
}

func TestFormatBilingualPreservesOriginalWhitespace(t *testing.T) {
	source := "\r\n\r\n  First paragraph.  \r\n \r\n    Second paragraph.\t"
	want := "\r\n\r\n  First paragraph.  \n訳一\r\n \r\n    Second paragraph.\t\n訳二"
	if got := FormatBilingual(source, "訳一\n\n訳二"); got != want {
		t.Fatalf("raw source whitespace changed: got %q want %q", got, want)
	}
}

func trimAllChunks(chunks []string) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = strings.TrimSpace(c)
	}
	return out
}

func TestChunkLinesWithBoundaries_Recombination(t *testing.T) {
	cases := []struct {
		name           string
		input          string
		budget         int
		wantBoundaries []Boundary
		wantRejoined   string
	}{
		{
			name:           "space on left side of boundary",
			input:          strings.Repeat("word ", 240) + "tail\nnext",
			budget:         1200,
			wantBoundaries: []Boundary{BoundarySpace, BoundaryNewline},
			wantRejoined:   strings.Repeat("word ", 240) + "tail\nnext",
		},
		{
			name:           "space on right side of boundary",
			input:          strings.Repeat("a", 1200) + " tail\nnext",
			budget:         1200,
			wantBoundaries: []Boundary{BoundarySpace, BoundaryNewline},
			wantRejoined:   strings.Repeat("a", 1200) + " tail\nnext",
		},
		{
			name:           "hard split without whitespace",
			input:          strings.Repeat("x", 1300) + "\nnext line",
			budget:         1200,
			wantBoundaries: []Boundary{BoundaryNone, BoundaryNewline},
			wantRejoined:   strings.Repeat("x", 1300) + "\nnext line",
		},
		{
			name:           "mixed explicit newlines and intra-line splits",
			input:          "head line\n" + strings.Repeat("word ", 240) + "tail\n" + strings.Repeat("y", 1300) + "\nend",
			budget:         1200,
			wantBoundaries: []Boundary{BoundaryNewline, BoundarySpace, BoundaryNewline, BoundaryNone, BoundaryNewline},
			wantRejoined:   "head line\n" + strings.Repeat("word ", 240) + "tail\n" + strings.Repeat("y", 1300) + "\nend",
		},
		{
			name:           "CJK long line without whitespace",
			input:          strings.Repeat("あ", 1300) + "\n次の行",
			budget:         1200,
			wantBoundaries: []Boundary{BoundaryNone, BoundaryNewline},
			wantRejoined:   strings.Repeat("あ", 1300) + "\n次の行",
		},
		{
			name:           "CJK long line with space on left",
			input:          strings.Repeat("語 ", 600) + "尾\n次",
			budget:         1200,
			wantBoundaries: []Boundary{BoundarySpace, BoundaryNewline},
			wantRejoined:   strings.Repeat("語 ", 599) + "語尾\n次",
		},
		{
			name:           "CJK long line with space on right",
			input:          strings.Repeat("語", 1200) + " 尾\n次",
			budget:         1200,
			wantBoundaries: []Boundary{BoundarySpace, BoundaryNewline},
			wantRejoined:   strings.Repeat("語", 1200) + "尾\n次",
		},
		{
			name:           "CRLF line at exact rune budget keeps CR with line",
			input:          strings.Repeat("a", 1200) + "\r\nnext",
			budget:         1200,
			wantBoundaries: []Boundary{BoundaryNewline},
			wantRejoined:   strings.Repeat("a", 1200) + "\nnext",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks, boundaries := ChunkLinesWithBoundaries(tc.input, tc.budget)
			if !reflect.DeepEqual(boundaries, tc.wantBoundaries) {
				t.Fatalf("boundaries = %v, want %v (chunks=%d)", boundaries, tc.wantBoundaries, len(chunks))
			}
			// Trimmed chunks model llamaserver.Translate's response trimming.
			if got := JoinChunks(trimAllChunks(chunks), boundaries); got != tc.wantRejoined {
				t.Fatalf("JoinChunks(trimmed) mismatch:\nGOT:  %q\nWANT: %q", got, tc.wantRejoined)
			}
			// Untrimmed chunks must not gain duplicate spaces.
			if got := JoinChunks(chunks, boundaries); got != tc.input {
				t.Fatalf("JoinChunks(untrimmed) mismatch:\nGOT:  %q\nWANT: %q", got, tc.input)
			}
		})
	}
}

func TestChunkParagraphWithBoundaries_Recombination(t *testing.T) {
	cases := []struct {
		name           string
		input          string
		budget         int
		wantBoundaries []Boundary
	}{
		{
			name:           "single-line space on left",
			input:          strings.Repeat("word ", 240) + "tail",
			budget:         1200,
			wantBoundaries: []Boundary{BoundarySpace},
		},
		{
			name:           "single-line space on right",
			input:          strings.Repeat("a", 1200) + " tail",
			budget:         1200,
			wantBoundaries: []Boundary{BoundarySpace},
		},
		{
			name:           "single-line hard split without whitespace",
			input:          strings.Repeat("x", 1300),
			budget:         1200,
			wantBoundaries: []Boundary{BoundaryNone},
		},
		{
			name:           "single-line CJK hard split without whitespace",
			input:          strings.Repeat("あ", 1300),
			budget:         1200,
			wantBoundaries: []Boundary{BoundaryNone},
		},
		{
			name:           "single-line CJK sentence split",
			input:          "最初の文です。二番目の文です。",
			budget:         8,
			wantBoundaries: []Boundary{BoundarySentence},
		},
		{
			name:           "mixed sentence boundary and intra-sentence space split",
			input:          "First sentence. Word " + strings.Repeat("word ", 239) + "tail.",
			budget:         1200,
			wantBoundaries: []Boundary{BoundarySentence, BoundarySpace},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks, boundaries := ChunkParagraphWithBoundaries(tc.input, tc.budget)
			if !reflect.DeepEqual(boundaries, tc.wantBoundaries) {
				t.Fatalf("boundaries = %v, want %v (chunks=%d)", boundaries, tc.wantBoundaries, len(chunks))
			}
			if got := JoinChunks(trimAllChunks(chunks), boundaries); got != tc.input {
				t.Fatalf("JoinChunks(trimmed) mismatch:\nGOT:  %q\nWANT: %q", got, tc.input)
			}
			if got := JoinChunks(chunks, boundaries); got != tc.input {
				t.Fatalf("JoinChunks(untrimmed) mismatch:\nGOT:  %q\nWANT: %q", got, tc.input)
			}
		})
	}
}

func TestJoinChunks_SpaceBoundaryTargetScript(t *testing.T) {
	source := strings.Repeat("word ", 240) + "tail"
	chunks, boundaries := ChunkParagraphWithBoundaries(source, 1200)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	wantBoundaries := []Boundary{BoundarySpace}
	if !reflect.DeepEqual(boundaries, wantBoundaries) {
		t.Fatalf("boundaries = %v, want %v", boundaries, wantBoundaries)
	}

	cases := []struct {
		name   string
		pieces []string
		want   string
	}{
		{
			name:   "CJK target pieces gain no space",
			pieces: []string{"これは日本語", "です"},
			want:   "これは日本語です",
		},
		{
			name:   "Latin target pieces gain space",
			pieces: []string{"word", "tail"},
			want:   "word tail",
		},
		{
			name:   "Korean words retain space",
			pieces: []string{"안녕하세요", "세계"},
			want:   "안녕하세요 세계",
		},
		{
			name:   "Korean phrase retains final space",
			pieces: []string{"단어 단어", "끝"},
			want:   "단어 단어 끝",
		},
		{
			name:   "mixed CJK then Latin gains no space",
			pieces: []string{"これは日本語", "tail"},
			want:   "これは日本語tail",
		},
		{
			name:   "mixed Latin then CJK gains no space",
			pieces: []string{"word", "です"},
			want:   "wordです",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := JoinChunks(tc.pieces, boundaries); got != tc.want {
				t.Fatalf("JoinChunks(%v, %v) = %q, want %q", tc.pieces, boundaries, got, tc.want)
			}
		})
	}

	_, lineBoundaries := ChunkLinesWithBoundaries(source+"\nnext", 1200)
	wantLineBoundaries := []Boundary{BoundarySpace, BoundaryNewline}
	if !reflect.DeepEqual(lineBoundaries, wantLineBoundaries) {
		t.Fatalf("lineBoundaries = %v, want %v", lineBoundaries, wantLineBoundaries)
	}
	if got := JoinChunks([]string{"これは日本語", "です", "次の行"}, lineBoundaries); got != "これは日本語です\n次の行" {
		t.Fatalf("JoinChunks(multi-line CJK target) = %q, want %q", got, "これは日本語です\n次の行")
	}
}

func TestJoinChunks_KoreanSentenceBoundaryUnchanged(t *testing.T) {
	if got := JoinChunks([]string{"안녕하세요", "세계"}, []Boundary{BoundarySentence}); got != "안녕하세요세계" {
		t.Fatalf("sentence boundary behavior changed: %q", got)
	}
}

func TestJoinChunks_CJKSourceExplicitSpace(t *testing.T) {
	// N2 remains a trade-off: metadata cannot distinguish an explicit source
	// space from one that a CJK translation should omit.
	source := strings.Repeat("語 ", 600) + "尾"
	chunks, boundaries := ChunkParagraphWithBoundaries(source, 1200)
	want := strings.Repeat("語 ", 599) + "語尾"
	if got := JoinChunks(trimAllChunks(chunks), boundaries); got != want {
		t.Fatalf("CJK source trade-off changed: got %q, want %q", got, want)
	}
}

func TestJoinChunks_PieceCountMismatchAndEmptyChunks(t *testing.T) {
	cases := []struct {
		name       string
		chunks     []string
		boundaries []Boundary
		want       string
	}{
		{
			name:       "fewer boundaries than gaps joins trailing pieces directly",
			chunks:     []string{"alpha", "beta", "gamma"},
			boundaries: []Boundary{BoundarySpace},
			want:       "alpha betagamma",
		},
		{
			name:       "nil boundaries joins all pieces directly",
			chunks:     []string{"alpha", "beta"},
			boundaries: nil,
			want:       "alphabeta",
		},
		{
			name:       "more boundaries than gaps ignores trailing boundaries",
			chunks:     []string{"alpha", "beta"},
			boundaries: []Boundary{BoundarySpace, BoundaryNewline, BoundarySpace},
			want:       "alpha beta",
		},
		{
			name:       "empty left chunk at space boundary inserts no leading space",
			chunks:     []string{"", "tail"},
			boundaries: []Boundary{BoundarySpace},
			want:       "tail",
		},
		{
			name:       "empty right chunk at space boundary inserts no trailing space",
			chunks:     []string{"head", ""},
			boundaries: []Boundary{BoundarySpace},
			want:       "head",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := JoinChunks(tc.chunks, tc.boundaries); got != tc.want {
				t.Fatalf("JoinChunks(%v, %v) = %q, want %q", tc.chunks, tc.boundaries, got, tc.want)
			}
		})
	}
}

func TestCleanSpuriousNewlines_MidSentenceUppercaseAndPunctuation(t *testing.T) {
	cases := []struct {
		name  string
		input string
		lang  string
		want  string
	}{
		{
			name:  "English proper nouns and acronyms mid-sentence",
			input: "We met with\nJohn yesterday to review the\nAPI specification.",
			lang:  "en",
			want:  "We met with John yesterday to review the API specification.",
		},
		{
			name: "English wrapped paragraph with I, proper noun, acronym",
			input: "During the morning conference call I\n" +
				"discussed the proposed architecture with\n" +
				"Alice and Bob regarding the new HTTP\n" +
				"endpoint that needs further testing.",
			lang: "en",
			want: "During the morning conference call I discussed the proposed architecture with Alice and Bob regarding the new HTTP endpoint that needs further testing.",
		},
		{
			name: "German prose with capitalized nouns mid-sentence",
			input: "Die vorgeschlagene Architektur führt einen\n" +
				"Mechanismus ein, der die Übersetzungsqualität auf\n" +
				"Geräten mit begrenzten Ressourcen verbessert und die\n" +
				"Latenz bei jeder Anfrage deutlich reduziert.",
			lang: "de",
			want: "Die vorgeschlagene Architektur führt einen Mechanismus ein, der die Übersetzungsqualität auf Geräten mit begrenzten Ressourcen verbessert und die Latenz bei jeder Anfrage deutlich reduziert.",
		},
		{
			name:  "Opening parenthesis mid-sentence",
			input: "This is a well-known phenomenon\n(see Figure 2) in practice.",
			lang:  "en",
			want:  "This is a well-known phenomenon (see Figure 2) in practice.",
		},
		{
			name: "Opening bracket and double/single quotes mid-sentence",
			input: "The benchmark results are summarized\n" +
				"[see Appendix A] while the authors noted\n" +
				"\"consistent gains across benchmarks\" and\n" +
				"“notable improvements in throughput” plus\n" +
				"'verified edge behavior' and\n" +
				"‘reproducible latency numbers’ overall.",
			lang: "en",
			want: "The benchmark results are summarized [see Appendix A] while the authors noted \"consistent gains across benchmarks\" and “notable improvements in throughput” plus 'verified edge behavior' and ‘reproducible latency numbers’ overall.",
		},
		{
			name:  "Trailing semicolon mid-sentence",
			input: "The first experiment measured latency;\nThe second measured peak memory usage.",
			lang:  "en",
			want:  "The first experiment measured latency; The second measured peak memory usage.",
		},
		{
			name:  "Trailing colon mid-sentence",
			input: "Several important details follow:\nEach component is tested independently.",
			lang:  "en",
			want:  "Several important details follow: Each component is tested independently.",
		},
		{
			name:  "Trailing em-dash mid-sentence",
			input: "The model handles multiple languages—\nEnglish and German among them—\nwhile keeping latency consistently low.",
			lang:  "en",
			want:  "The model handles multiple languages— English and German among them— while keeping latency consistently low.",
		},
		{
			name:  "Trailing en-dash mid-sentence",
			input: "The evaluation covers 2020–\n2025 benchmark corpora across domains.",
			lang:  "en",
			want:  "The evaluation covers 2020– 2025 benchmark corpora across domains.",
		},
		{
			name: "Trailing closing paren, bracket, and quotes mid-sentence",
			input: "The baseline configuration (v1.0)\n" +
				"Remains supported, the reference [Ref 2]\n" +
				"Applies cleanly, and \"legacy mode\"\n" +
				"Alongside the newer ‘strict mode’\n" +
				"Continues to pass all checks.",
			lang: "en",
			want: "The baseline configuration (v1.0) Remains supported, the reference [Ref 2] Applies cleanly, and \"legacy mode\" Alongside the newer ‘strict mode’ Continues to pass all checks.",
		},
		{
			name:  "CRLF line endings preserved on non-joined breaks",
			input: "Overview\r\nThis paragraph was wrapped mid-sentence\r\nBerlin and Munich are listed here.",
			lang:  "en",
			want:  "Overview\r\nThis paragraph was wrapped mid-sentence Berlin and Munich are listed here.",
		},
		{
			name:  "Prose ending in semicolon with bracketed aside reflows",
			input: "The experiment used a stable configuration [version two];\nBerlin provided the reference dataset.",
			lang:  "en",
			want:  "The experiment used a stable configuration [version two]; Berlin provided the reference dataset.",
		},
		{
			name:  "Prose ending in semicolon with comparison sign reflows",
			input: "The benchmark results improved by >10%;\nBerlin provided the reference dataset.",
			lang:  "en",
			want:  "The benchmark results improved by >10%; Berlin provided the reference dataset.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanSpuriousNewlines(tc.input, tc.lang); got != tc.want {
				t.Errorf("CleanSpuriousNewlines mismatch:\nGOT:\n%q\nWANT:\n%q", got, tc.want)
			}
		})
	}
}

func TestCleanSpuriousNewlines_NegativeGuardsPreserved(t *testing.T) {
	cases := []struct {
		name  string
		input string
		lang  string
	}{
		{
			name: "Heading shorter than 0.75 column median before uppercase paragraph",
			input: "System Overview and Experimental Setup\n" +
				"The evaluation measures translation latency across multiple benchmarks.",
			lang: "en",
		},
		{
			name: "Pipe-delimited markdown table rows",
			input: "| Model | Latency | Accuracy |\n" +
				"| Baseline | 12.4 ms | 91.2% |\n" +
				"| Proposed | 8.1 ms | 94.5% |",
			lang: "en",
		},
		{
			name: "Space-aligned table rows ending in percent",
			input: "Model Alpha   Baseline   40%\n" +
				"Model Beta    Proposed   55%",
			lang: "en",
		},
		{
			name: "Tab-aligned table rows",
			input: "Model Alpha\tBaseline\t40%\n" +
				"Model Beta\tProposed\t55%",
			lang: "en",
		},
		{
			name: "Parenthesized numbered list items",
			input: "(1) First item describes the preprocessing stage in full detail\n" +
				"(2) Second item describes the postprocessing stage in full detail",
			lang: "en",
		},
		{
			name: "Indentation shift with two spaces",
			input: "First paragraph ends without a trailing period\n" +
				"  Indented second paragraph must remain separate.",
			lang: "en",
		},
		{
			name: "Indentation shift with tab",
			input: "First paragraph ends without a trailing period\n" +
				"\tTab-indented second paragraph must remain separate.",
			lang: "en",
		},
		{
			name: "Indentation shift with fullwidth space",
			input: "最初の段落は句点なしで終わる行です\n" +
				"\u3000全角スペースで始まる次の段落は維持される。",
			lang: "ja",
		},
		{
			name: "Code lines ending in semicolon remain preserved",
			input: "result = computeLatency(samples);\n" +
				"return result;",
			lang: "en",
		},
		{
			name: "Sentence ending inside closing quote or paren still ends sentence",
			input: "The author finally concluded \"Everything works.\"\n" +
				"Okay.",
			lang: "en",
		},
		{
			name: "Sentence ending inside closing parenthesis still ends sentence",
			input: "The author finally concluded (see Appendix A.)\n" +
				"Okay.",
			lang: "en",
		},
		{
			name:  "All-caps heading ending in connector word before long sentence",
			input: "INTRODUCTION TO\nThe following section explains all available translation profiles.",
			lang:  "en",
		},
		{
			name:  "Short attribution line ending in connector word",
			input: "Prepared by\nAlice Example, QA Department",
			lang:  "en",
		},
		{
			name: "Parenthesized Roman-numeral list items",
			input: "(i) First item describes the preprocessing stage in full detail\n" +
				"(ii) Second item describes the postprocessing stage in full detail",
			lang: "en",
		},
		{
			name: "Unindented C++ stream statements ending in semicolon",
			input: "logger << \"The translation is ready\";\n" +
				"logger << \"The next request is pending\";",
			lang: "en",
		},
		{
			name: "Unindented array declarations ending in semicolon",
			input: "TranslationResult[] translatedResults;\n" +
				"TranslationRequest[] pendingRequests;",
			lang: "en",
		},
		{
			name: "Unindented generic declarations ending in semicolon",
			input: "List<string> translatedParagraphs;\n" +
				"List<string> sourceParagraphs;",
			lang: "en",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanSpuriousNewlines(tc.input, tc.lang); got != tc.input {
				t.Errorf("negative guard failed to preserve breaks:\nGOT:\n%q\nWANT:\n%q", got, tc.input)
			}
		})
	}
}

func TestSplitSentences_EllipsesAbbreviationsAndLowercaseContinuation(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "Ellipsis mid-sentence lowercase",
			input: "Wait... really?",
			want:  []string{"Wait... really?"},
		},
		{
			name:  "Ellipsis mid-sentence uppercase and later boundary",
			input: "Wait... Really? Next sentence.",
			want:  []string{"Wait... Really?", "Next sentence."},
		},
		{
			name:  "Ellipsis in quotes followed by lowercase",
			input: "He said \"Wait...\" and left. Next sentence.",
			want:  []string{"He said \"Wait...\" and left.", "Next sentence."},
		},
		{
			name:  "Document and academic abbreviations",
			input: "See Smith et al. for details, Figs. 2, Eq. 5, Eqs. 6, Vol. 3, Vols. 4-5, No. 4, Nos. 5, Nr. 7, pp. 10, p. 12, Ch. 2, Chs. 3-4, Chap. 5, Sec. 4, Secs. 5-6, vs. baseline, v. 2, cf. Table 1, ca. 1995, approx. 10, Dept. 3, est. 2024, min. 1, max. 9, Ed. 2, Eds. Smith, Ref. 8, Refs. 9-10, St. Louis, Oak Ave. downtown. Next sentence.",
			want: []string{
				"See Smith et al. for details, Figs. 2, Eq. 5, Eqs. 6, Vol. 3, Vols. 4-5, No. 4, Nos. 5, Nr. 7, pp. 10, p. 12, Ch. 2, Chs. 3-4, Chap. 5, Sec. 4, Secs. 5-6, vs. baseline, v. 2, cf. Table 1, ca. 1995, approx. 10, Dept. 3, est. 2024, min. 1, max. 9, Ed. 2, Eds. Smith, Ref. 8, Refs. 9-10, St. Louis, Oak Ave. downtown.",
				"Next sentence.",
			},
		},
		{
			name:  "Period followed by whitespace and lowercase letter (unlisted tokens)",
			input: "See et seq. for further background, and passim. in the commentary. Next sentence.",
			want:  []string{"See et seq. for further background, and passim. in the commentary.", "Next sentence."},
		},
		{
			name:  "Period inside closing quote followed by lowercase continuation",
			input: "He said \"Stop.\" quietly to himself. Next sentence.",
			want:  []string{"He said \"Stop.\" quietly to himself.", "Next sentence."},
		},
		{
			name:  "Real sentence boundary with digit",
			input: "First sentence. 2024 brought new results.",
			want:  []string{"First sentence.", "2024 brought new results."},
		},
		{
			name:  "Real sentence boundary after numbered reference and alphanumeric item",
			input: "This is shown in Fig. 1. More details appear in Fig. 1a. The next section covers more.",
			want:  []string{"This is shown in Fig. 1.", "More details appear in Fig. 1a.", "The next section covers more."},
		},
		{
			name:  "Question and exclamation marks with lowercase attribution still split",
			input: "\"Really?\" she asked. \"Yes!\" he replied.",
			want:  []string{"\"Really?\"", "she asked.", "\"Yes!\"", "he replied."},
		},
		{
			name:  "Sentence-final min. before new sentence",
			input: "The wait lasted one min. Then we left.",
			want:  []string{"The wait lasted one min.", "Then we left."},
		},
		{
			name:  "Sentence-final ed. before new sentence",
			input: "We consulted the latest ed. Then we revised the draft.",
			want:  []string{"We consulted the latest ed.", "Then we revised the draft."},
		},
		{
			name:  "Sentence-final Ave. before new sentence",
			input: "The street was Oak Ave. Then we turned left.",
			want:  []string{"The street was Oak Ave.", "Then we turned left."},
		},
		{
			name:  "Sentence-initial No. before new sentence",
			input: "No. This is unsupported.",
			want:  []string{"No.", "This is unsupported."},
		},
		{
			name:  "Sentence-final etc. before new sentence",
			input: "We bought paper, pens, etc. Then we left.",
			want:  []string{"We bought paper, pens, etc.", "Then we left."},
		},
		{
			name:  "Preserve Fig. before capital-letter reference identifier S1",
			input: "See Fig. S1 for details.",
			want:  []string{"See Fig. S1 for details."},
		},
		{
			name:  "Preserve Eq. before capital-letter reference identifier A1",
			input: "See Eq. A1 for details.",
			want:  []string{"See Eq. A1 for details."},
		},
		{
			name:  "Preserve Figs. before capital-letter reference identifier A",
			input: "See Figs. A and B for details.",
			want:  []string{"See Figs. A and B for details."},
		},
		{
			name:  "Preserve e.g. before capitalized proper noun continuation",
			input: "We used alternative sources, e.g. The Times and The Guardian.",
			want:  []string{"We used alternative sources, e.g. The Times and The Guardian."},
		},
		{
			name:  "Preserve i.e. before capitalized proper noun continuation",
			input: "The destination is clear, i.e. The Hague.",
			want:  []string{"The destination is clear, i.e. The Hague."},
		},
		{
			name:  "Split sentence after U.S. before sentence starter Then",
			input: "The company operates in the U.S. Then it expanded.",
			want:  []string{"The company operates in the U.S.", "Then it expanded."},
		},
		{
			name:  "Split sentence after p.m. before sentence starter We",
			input: "The meeting ended at 5 p.m. We went home.",
			want:  []string{"The meeting ended at 5 p.m.", "We went home."},
		},
		{
			name:  "Split sentence after U.K. before sentence starter The",
			input: "This happened in the U.K. The results were clear.",
			want:  []string{"This happened in the U.K.", "The results were clear."},
		},
		{
			name:  "Split ordinary sentence starting with No. before pronoun I",
			input: "No. I disagree.",
			want:  []string{"No.", "I disagree."},
		},
		{
			name:  "Split ordinary sentence starting with No. before article A",
			input: "No. A different approach is needed.",
			want:  []string{"No.", "A different approach is needed."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitSentences(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitSentences(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestChunkParagraphWithBoundaries_EllipsesAndAbbreviations(t *testing.T) {
	s1 := "Wait... really, as shown in Fig. 1, Eq. 2, Vol. 3, No. 4, pp. 10-15, and Smith et al. for this case."
	s2 := "Another sentence follows cleanly here."
	input := s1 + " " + s2

	chunks, boundaries := ChunkParagraphWithBoundaries(input, len([]rune(s1))+5)
	if len(chunks) != 2 || chunks[0] != s1 || chunks[1] != s2 {
		t.Fatalf("ChunkParagraphWithBoundaries split unexpectedly: chunks=%#v", chunks)
	}
	wantBoundaries := []Boundary{BoundarySentence}
	if !reflect.DeepEqual(boundaries, wantBoundaries) {
		t.Fatalf("boundaries = %v, want %v", boundaries, wantBoundaries)
	}
	if got := JoinChunks(trimAllChunks(chunks), boundaries); got != input {
		t.Fatalf("JoinChunks mismatch: got %q, want %q", got, input)
	}
}

func TestChunkParagraphWithBoundaries_SentenceFinalMultipartAbbrev(t *testing.T) {
	input := "The company operates in the U.S. Then it expanded."
	chunks, boundaries := ChunkParagraphWithBoundaries(input, 40)
	wantChunks := []string{"The company operates in the U.S.", "Then it expanded."}
	wantBoundaries := []Boundary{BoundarySentence}
	if !reflect.DeepEqual(chunks, wantChunks) || !reflect.DeepEqual(boundaries, wantBoundaries) {
		t.Fatalf("ChunkParagraphWithBoundaries mismatch:\nchunks: %#v, want %#v\nboundaries: %#v, want %#v",
			chunks, wantChunks, boundaries, wantBoundaries)
	}
}
