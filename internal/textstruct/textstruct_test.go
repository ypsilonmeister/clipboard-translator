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
