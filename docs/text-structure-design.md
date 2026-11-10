# Text Structure Detection & Processing — Design Document (Issue #145)

> **Status: Design proposal for review.**  
> Central philosophy: *Do not interrupt the work you are doing just to translate* ([`docs/DESIGN_PHILOSOPHY.md`](DESIGN_PHILOSOPHY.md)).  
> Scope: Design only. No implementation code is included in this branch.

---

## 1. Executive Summary & Goals

This document specifies the shared text-structure analysis foundation required by two interrelated capabilities:
1. **Newline cleanup before translation:** Removing spurious mid-sentence line breaks introduced when copying from PDFs, terminals, or narrow column text, while strictly preserving meaningful structure (lists, addresses, code, block quotes, signatures, headings).
2. **Long-text chunking:** Safely splitting text exceeding model token limits (`ErrTruncated`) into coherent semantic chunks (paragraphs first, sentences second), translating chunk by chunk, and joining the results cleanly.

### Fundamental Invariant: Asymmetry of Failure (失敗の非対称性)
The core design principle governing all structural judgments is **extreme conservatism**:
- **False Negative (迷ったら残す / Spurious break left intact):** A spurious line break is accidentally preserved. The model translates a slightly awkwardly broken sentence, but the original layout and all formatting remain fully intact. *Usability impact: minimal to mild.*
- **False Positive (守るべき改行を消す / Meaningful break destroyed):** A code snippet, list, bullet point, address line, or quotation has its newlines stripped and is flattened into an unreadable run-on paragraph. *Usability impact: severe and destructive.*

> [!IMPORTANT]
> **Worst-case failure is destroying meaningful formatting.**  
> If there is any ambiguity or conflicting signals, **leave the newline untouched**. When in doubt, do not modify.

---

## 2. Structural Signals & Detection Rules

The package `internal/textstruct` exposes two deterministic, pure functions operating over UTF-8 text without external dependencies or ML models:

```go
// CleanSpuriousNewlines reflows soft line breaks caused by fixed-width column wraps (e.g. PDFs, terminal output)
// while strictly preserving all structural boundaries (blank lines, lists, code, quotes, signatures).
func CleanSpuriousNewlines(text string, lang string) string

// ChunkText splits long text exceeding maxChunkChars into coherent semantic segments
// (paragraphs first, sentences second), respecting model context limits.
func ChunkText(text string, maxChunkChars int) []string
```

Both functions are built upon a shared internal line analysis pipeline (`analyzeLines`), which extracts per-line visual display width, indentation, boundary tokens, and structural guards.

### 2.1 Preserved Structures (Hard Guards — Never Flatten)
If a line matches any of the following patterns, its bounding breaks are **always preserved**:
1. **Blank lines (Paragraph separators):** `\n\s*\n`. These represent paragraph boundaries and chunk candidates.
2. **Block quotes / Replied email headers:** Lines starting with optional whitespace and `>` (e.g. `> quoted`, `>> nested`).
3. **List markers:**
   - Unordered: `^[\s]*([-*•・]|\d+[.)])\s+`
   - Ordered/lettered: `^[\s]*([a-zA-Z]|[ivxLCDM]+)[.)]\s+`
4. **Code / Indented blocks:**
   - Fenced code blocks (` ``` `, `~~~`).
   - Lines indented by 4+ spaces or 1+ tabs where adjacent lines indicate structured data (e.g. key-value pairs, brackets, operators).
5. **Short lines / Structural delimiters:**
   - Horizontal rules or dividers (`---`, `===`, `___`, `***`).
   - Email signature separators (`^--\s*$`).
   - Trailing short lines (< 20 runes) preceding blank lines or EOF, which commonly indicate signatures, titles, or sign-offs (e.g. "Best regards,\nAlice").

### 2.2 Signals for Spurious Wrap Detection (PDF Hard-Breaks)
In multi-line column text (such as two-column academic papers or reports), soft line breaks occur purely because a line reached the visual margin. Crucially, **sentences frequently end mid-paragraph within a column wrap without any blank line**. Therefore, sentence-ending punctuation alone cannot serve as a boundary veto.

Detection applies the following explicit hierarchy:

#### A. Primary Paragraph Boundary Signals (Hard Separators — Never Join)
1. **Blank lines:** Explicit blank lines `\n\s*\n`.
2. **Short terminal line with sentence-ending punctuation:**
   - If line $L_i$ ends in sentence-ending punctuation (`.`, `!`, `?`, `。`, `！`, `？`, `…`) AND its visual width $W(L_i)$ is distinctly shorter than the column margin (e.g. $W(L_i) < 0.75 \times \text{median}(W)$ or $> 15$ runes shorter than the established column width), $L_i$ is the final line of a paragraph. The line break $L_i \to L_{i+1}$ is preserved.
3. **Indentation change or structural guard on $L_{i+1}$:**
   - Line $L_{i+1}$ is indented by 2+ spaces (paragraph indent), or begins with a list marker, quote, code block, or signature delimiter.
4. **Script transition boundary:**
   - A line in one script ending in sentence-ending punctuation (e.g. Latin `.`) followed by a line in a different script family (e.g. Japanese Kanji/Kana) represents a structural section change and is preserved.

#### B. Geometric Signal (Uniform Column Wrapping — Primary Join Driver)
- Let $W(L)$ be the visual display width (East Asian Width aware: full-width = 2, half-width = 1).
- If lines in the block cluster around a consistent column margin (e.g. standard deviation of width < 5 runes, or $|W(L_i) - \text{median}| \le 3$), line $L_i$ was wrapped by physical column geometry rather than semantic intent.
- **Sentence-crossing continuation:** Even if $L_i$ ends in sentence-ending punctuation (`.` or `。`), if $L_i$ is a full-width line (not a short terminal line) and $L_{i+1}$ continues in the same column block without blank line or indentation, it is treated as a geometric wrap across sentences within the paragraph and is **joined** (with a space for Latin scripts, without space for CJK scripts).

#### C. Punctuation as Auxiliary / Directional Signals (補助シグナル)
- When line width is indeterminate (e.g. short 2-line snippets where median column width has no statistical significance):
  - **Mid-sentence continuation:** If $L_i$ does *not* end in sentence-ending punctuation, and $L_{i+1}$ begins with a lowercase letter (`[a-z]`) or Japanese particle/connector (は, が, を, に, で, と, も), the lines are joined.
  - **Ambiguity resolution:** If $L_i$ ends in sentence-ending punctuation and column width cannot confirm uniform wrapping, the Asymmetry of Failure (*迷ったら残す / When in doubt, preserve*) takes precedence: keep the line break.

#### D. Hyphenated Line Breaks (ASCII Hyphen Handling)
- When line $L_i$ ends in an alphanumeric followed by an ASCII hyphen (`[a-zA-Z]-`) and line $L_{i+1}$ begins with `[a-zA-Z]`:
  - The line break is removed and **the hyphen is always preserved** (`[a-zA-Z]-[a-zA-Z]`).
  - *Why always preserve?* Without a full dictionary, an algorithm cannot distinguish split words (`trans-\nlation`) from legitimate compound words (`transformer-\nbased`, `resource-\nconstrained`, `multi-\nlevel`). Preserving the hyphen is safe and non-destructive: `transformer-based` remains intact, and `trans-lation` is effortlessly and accurately understood by modern translation LLMs. Conversely, stripping hyphens would catastrophically corrupt compound words into invalid tokens (`transformerbased`). (If an explicit Unicode soft hyphen `\u00ad` is present, it is an unambiguous syllable break and may be cleanly stripped).

---

## 3. Long-Text Chunking Strategy

When text length exceeds the safety budget (determined by profile `max_chars` or model context constraints):

```
+-------------------------------------------------------------+
|                         Source Text                         |
+-------------------------------------------------------------+
                              |
                              v
             Step 1: Blank-line Paragraph Split
                              |
       +----------------------+----------------------+
       |                                             |
[Paragraph <= ChunkBudget]               [Paragraph > ChunkBudget]
       |                                             |
  Keep intact                                        v
                                        Step 2: Sentence Split
                                        (. ! ? 。 ！？ followed by boundary)
                                                     |
                                       +-------------+-------------+
                                       |                           |
                          [Sentence <= ChunkBudget]   [Oversized Sentence]
                                       |                           |
                                  Keep intact                      v
                                                      Step 3: Hard-split
                                                      at word/rune boundary
```

1. **Paragraph-level chunks (Primary):** Split on blank-line boundaries (`\n\s*\n`). Consecutive small paragraphs are grouped together up to the target chunk size.
2. **Sentence-level fallback (Secondary):** If an individual paragraph exceeds the chunk size, split it at sentence boundaries:
   - Latin: `[.!?]\s+` followed by an uppercase letter.
   - Japanese: `[。！？]` followed by non-quote character.
3. **Hard-limit split (Tertiary fallback):** If a single sentence exceeds the token budget (e.g. minified text or massive comma-separated listings), split at major clause delimiters (`;`, `,`, `、`), or cleanly at rune boundaries.
4. **Bounded Concurrency & Single-Process Lifetime:**
   - Chunks are translated sequentially (concurrency of 1) over loopback HTTP to `llama-server`. This guarantees zero VRAM/RAM spike and respects the single-process supervisor model.

### 3.1 Failure & Cancellation Semantics (失敗・キャンセル時の振る舞い)
Multi-chunk translation must strictly uphold the app's fundamental contract of **Clipboard Safety** (never corrupt or overwrite clipboard contents unexpectedly):

- **All-or-Nothing Guarantee:** If translation of any chunk encounters an error (network error, model crash, timeout) or is superseded/cancelled by the user (pressing the hotkey again or triggering abort):
  1. Terminate all remaining pending chunk requests immediately.
  2. Discard all partially accumulated chunk translations.
  3. **Leave the system clipboard untouched:** The user's clipboard remains exactly as it was before the hotkey was pressed.
  4. **In-Memory History:** Do not record incomplete or fragmented translations in the history tray menu. The failed request is discarded (or recorded with its original text and an error indicator).
  5. **User Feedback:** The tray icon tooltip or balloon notification clearly indicates the failure (e.g. `Translation failed at chunk 2 of 4`).

### 3.2 Bilingual Output Recombination (バイリンガル出力の結合形式)
When `output: "bilingual"` is configured in the profile:
- Multi-chunk texts split at paragraph boundaries are recombined **paragraph-by-paragraph** (interleaved structure):
  ```text
  [Original Paragraph 1]
  [Translated Paragraph 1]

  [Original Paragraph 2]
  [Translated Paragraph 2]
  ```
- *Rationale:* Interleaved per-paragraph format preserves the natural 1-to-1 reading alignment across long texts. Grouping all source paragraphs followed by all translated paragraphs is rejected because it forces the user to constantly scroll back and forth to compare text.
- If an oversized paragraph was split into sentences (Step 2), the translated sentences are reassembled back into the translated paragraph before being paired with the original source paragraph.

---

## 4. Open Questions & Recommendations

Following the pattern in [`docs/package-split-design.md`](package-split-design.md), each question includes an explicit recommended choice with the rationale so it can be approved without ambiguity.

### Q1: Where does newline cleanup apply: per-profile option, or automatic on strong PDF signature?
- **Options considered:**
  - *Option A:* Fully automatic detection whenever geometric and punctuation signals agree.
  - *Option B (Recommended):* **Per-profile opt-in setting (`cleanup_newlines: "auto" | "off"`, default `"off"`), with `"auto"` enabled when user explicitly checks "Clean up spurious line breaks" in Settings.**
- **Rationale:**
  Even the most conservative heuristic cannot read user intent: a user translating a terminal log or poetry might intentionally want the exact layout fed to the model. In accordance with Principle 2 (*Keep the common path minimal*) and Principle 5 (*Reliability is part of usability*), the default behavior must remain 100% transparent and predictable. Users who frequently translate PDFs or research papers can enable it per profile (e.g. creating a dedicated hotkey for "PDF / Paper Translation").

### Q2: How should the chunk budget be determined?
- **Options considered:**
  - *Option A:* Dynamically queried from `llama-server` context tokens via API.
  - *Option B (Recommended):* **Fixed default chunk target of 1,200 source characters (~300–600 tokens), clamped to `profile.max_chars` if configured lower.**
- **Rationale:**
  The relevant existing configuration knobs are:
  - Model context size: `context_tokens` (default `4096` in `config.json`).
  - Output generation limit: `max_tokens` (default `2048` in `config.json`).
  - Profile character limit: `max_chars` (default `4000` in `config.json`).

  Context budget constraint: $\text{prompt\_template} + \text{source\_tokens} + \text{target\_tokens} \le \text{context\_tokens} (4096)$.  
  With `max_tokens` capped at 2048, generated target tokens can consume up to 2048 tokens. This leaves $\sim 2000$ tokens for source text and prompt overhead.  
  Because PLaMo 3 Translate handles 53 languages, token expansion ratios vary (English $\to$ Japanese or Thai can require 1.5–2.0x tokens per character). A 1,200-character source chunk corresponds to $\sim 300\text{--}600$ input tokens. Even with a 2x expansion ($\sim 600\text{--}1200$ target tokens), total context stays comfortably below 1,900 tokens. This leaves an enormous safety margin of $> 2000$ tokens below the 4096 context ceiling, eliminating any possibility of `ErrTruncated` while preserving complete paragraphs.

### Q3: Does cleanup apply to the stored history and bilingual output, or only to the prompt sent to the model?
- **Options considered:**
  - *Option A:* Replace original text everywhere (in prompt, history, and bilingual output).
  - *Option B (Recommended):* **Cleanup applies strictly to the text sent to the translation model. The user's original raw clipboard text is preserved verbatim in memory history and in bilingual (`formatBilingual`) output.**
- **Rationale:**
  The clipboard translator's primary contract is **clipboard safety and non-destructive operation**. If a user copies text, translates it in bilingual mode, and pastes it, they expect *their original text* to be what they copied, not an algorithm's guess at how it should be reflowed. Furthermore, if the heuristic ever made a mistake, the user still has their untampered original in the history tray menu.

### Q4: Should hyphenated words across wrapped lines be un-hyphenated?
- **Options considered:**
  - *Option A:* Strip trailing hyphens when followed by lowercase (`transformer-\nbased` $\to$ `transformerbased`).
  - *Option B (Recommended):* **Always preserve the ASCII hyphen when joining wrapped lines (`[a-zA-Z]-\n[a-zA-Z]` $\to$ `[a-zA-Z]-[a-zA-Z]`). (If Unicode soft hyphen `\u00ad` is present, it is stripped).**
- **Rationale:**
  Directly governed by the Asymmetry of Failure (失敗の非対称性) principle. Without a dictionary, an algorithm cannot distinguish legitimate compound words (`transformer-based`, `resource-constrained`, `multi-level`) from wrapped single words (`trans-lation`). Stripping hyphens corrupts compound words into invalid nonsense (`transformerbased`). By contrast, preserving the hyphen is completely benign: `transformer-based` remains pristine, and `trans-lation` is effortlessly understood and translated accurately by modern LLMs.

### Q5: How should mid-translation failure or user cancellation be handled during multi-chunk translation?
- **Options considered:**
  - *Option A:* Write partial translations translated so far to the clipboard.
  - *Option B (Recommended):* **All-or-nothing: Discard partial translations, leave the user's clipboard completely untouched, and notify the failure via tray status/logs.**
  - **Rationale:**
    Writing partial results to the clipboard violates Clipboard Safety (Principle 5) and interrupts the user with broken half-translations. An all-or-nothing approach guarantees predictable, clean operation.

### Q6: How should chunked translations be recombined in bilingual mode?
- **Options considered:**
  - *Option A:* Concatenate all source paragraphs followed by all translated paragraphs.
  - *Option B (Recommended):* **Interleaved paragraph-by-paragraph structure (`Original P1\nTrans P1\n\nOriginal P2\nTrans P2`).**
  - **Rationale:**
    Interleaving maintains 1-to-1 reading correspondence for long documents, preventing the user from needing to scroll back and forth to match sentences.

---

## 5. Sample Corpus & Test Fixtures (テスト用コーパス)

The following real-world fixtures represent the acceptance criteria. For each fixture, the verbatim input and the exact expected structural judgment are pinned below.

```
Legend:
  [JOIN]  -> Spurious break removed (replaced by space ' ' in Latin, or joined directly '' in CJK, or de-hyphenated)
  [KEEP]  -> Meaningful break preserved (kept as \n or \r\n)
  [SPLIT] -> Paragraph/chunk boundary
```

---

### Fixture 1: PDF Column Wrap (English Prose)
*Origin: Two-column academic conference paper.*

#### Input Text
```text
The proposed architecture introduces a novel transformer-
based attention mechanism that significantly improves trans-
lation quality on resource-constrained edge devices.
Furthermore, the computational latency is reduced by 40%
without observable loss in accuracy.
```

#### Expected Judgment
- Line 1 $\to$ 2: `transformer-\nbased` $\implies$ **[JOIN: PRESERVE HYPHEN]** $\to$ `transformer-based`
- Line 2 $\to$ 3: `trans-\nlation` $\implies$ **[JOIN: PRESERVE HYPHEN]** $\to$ `trans-lation`
- Line 3 $\to$ 4: `devices.\nFurthermore,` $\implies$ **[JOIN]** with space (uniform column wrap, non-terminal full-width line ending in sentence-ending punctuation continues into next line in same column block)
- Line 4 $\to$ 5: `40%\nwithout` $\implies$ **[JOIN]** with space (mid-sentence continuation, lowercase next)
- **Resulting Text for Model:**
  `The proposed architecture introduces a novel transformer-based attention mechanism that significantly improves trans-lation quality on resource-constrained edge devices. Furthermore, the computational latency is reduced by 40% without observable loss in accuracy.`

---

### Fixture 2: PDF Column Wrap (Japanese Prose)
*Origin: Multi-column Japanese technical report / PDF.*

#### Input Text
```text
本研究では、小型エッジデバイス上で高速かつ高精度に
動作する翻訳モデルの実現を目的とし、パラメータ削減と
量子化手法の適用を行った。
その結果、推論速度の大幅な向上が確認され、実用的な
応答性を達成することができた。
```

#### Expected Judgment
- Line 1 $\to$ 2: `高精度に\n動作する` $\implies$ **[JOIN: NO SPACE]** (no ending punctuation, hiragana continuation)
- Line 2 $\to$ 3: `削減と\n量子化手法` $\implies$ **[JOIN: NO SPACE]**
- Line 3 $\to$ 4: `行った。\nその結果、` $\implies$ **[JOIN: NO SPACE]** (uniform column wrap without short terminal line or blank line)
- Line 4 $\to$ 5: `実用的な\n応答性を` $\implies$ **[JOIN: NO SPACE]**
- **Resulting Text for Model:**
  `本研究では、小型エッジデバイス上で高速かつ高精度に動作する翻訳モデルの実現を目的とし、パラメータ削減と量子化手法の適用を行った。その結果、推論速度の大幅な向上が確認され、実用的な応答性を達成することができた。`

---

### Fixture 3: Quoted Email Reply with `>`
*Origin: Email thread with quote markers and interspersed replies.*

#### Input Text
```text
> Thanks for the update on the release schedule.
> Are we still on track for Friday?

Yes, everything looks good on our end. We just
finished the final regression suite.

> Please let me know if you need anything from QA.

All tests passed cleanly.
```

#### Expected Judgment
- Line 1 $\to$ 2: `> Thanks... \n> Are...` $\implies$ **[KEEP]** (Both lines start with `>`)
- Line 2 $\to$ 3: `> Are... \n\nYes...` $\implies$ **[KEEP / SPLIT]** (Blank line paragraph boundary)
- Line 3 $\to$ 4: `We just\nfinished` $\implies$ **[JOIN]** with space (unquoted prose mid-sentence wrap)
- Line 4 $\to$ 5: `suite.\n\n> Please...` $\implies$ **[KEEP / SPLIT]** (Blank line boundary)
- Line 5 $\to$ 6: `> Please...\n\nAll tests...` $\implies$ **[KEEP / SPLIT]** (Blank line boundary)

---

### Fixture 4: Bulleted / Numbered List
*Origin: Task list or specifications.*

#### Input Text
```text
Please complete the following checklist before merging:
* Run gofmt -l to ensure formatting passes
* Execute go test -race ./...
* Verify Windows GUI build without console popup
* Update documentation:
  1. README.md
  2. README.ja.md
```

#### Expected Judgment
- Line 1 $\to$ 2: `merging:\n* Run` $\implies$ **[KEEP]** (List marker `* ` follows colon)
- Line 2 $\to$ 3: `passes\n* Execute` $\implies$ **[KEEP]** (Bullet items must stay on separate lines)
- Line 3 $\to$ 4: `./...\n* Verify` $\implies$ **[KEEP]** (Bullet items)
- Line 4 $\to$ 5: `popup\n* Update` $\implies$ **[KEEP]** (Bullet items)
- Line 5 $\to$ 6: `documentation:\n  1. README.md` $\implies$ **[KEEP]** (Indented ordered list marker follows)
- Line 6 $\to$ 7: `README.md\n  2. README.ja.md` $\implies$ **[KEEP]** (Ordered list items)
- **Zero breaks removed.** List structure strictly preserved.

---

### Fixture 5: Code Block & Indentation
*Origin: Stack trace or inline code copied with comments.*

#### Input Text
```text
func applySettings(form Form) error {
	if form.MaxChars <= 0 {
		return errors.New("invalid limit")
	}
	// Note: keep revisions consistent across saves
	return nil
}
```

#### Expected Judgment
- Lines 1 through 7: All lines contain brackets `{`, `}`, leading tabs `\t`, or language keywords.
- **[KEEP] on all lines.** Code must never have line breaks flattened.

---

### Fixture 6: Email Sign-off & Signature
*Origin: End of business correspondence.*

#### Input Text
```text
We look forward to discussing the partnership proposal in detail
during our scheduled call next Tuesday.

Sincerely,
Taro Yamada
Senior Director of Engineering
Clipboard Translator Team
-- 
https://github.com/ypsilonmeister/clipboard-translator
```

#### Expected Judgment
- Line 1 $\to$ 2: `detail\nduring` $\implies$ **[JOIN]** with space (wrapped body sentence)
- Line 2 $\to$ 3: `Tuesday.\n\nSincerely,` $\implies$ **[KEEP / SPLIT]** (Blank line)
- Line 3 $\to$ 4: `Sincerely,\nTaro Yamada` $\implies$ **[KEEP]** (Short closing phrase `< 20` runes ending in comma)
- Line 4 $\to$ 5: `Taro Yamada\nSenior Director...` $\implies$ **[KEEP]** (Short name line `< 25` runes)
- Line 5 $\to$ 6: `Senior Director...\nClipboard...` $\implies$ **[KEEP]** (Title and team lines)
- Line 6 $\to$ 7: `Team\n-- ` $\implies$ **[KEEP]** (Signature delimiter `-- `)
- Line 7 $\to$ 8: `-- \nhttps://...` $\implies$ **[KEEP]** (URL signature)

---

### Fixture 7: Japanese & English Mixed (Bilingual Document Wrap)
*Origin: Meeting minutes or software announcement with mixed scripts.*

#### Input Text
```text
New version v1.2.0 is now available. This release includes several
important bug fixes and performance improvements.
本バージョンでは、メモリ使用効率の改善と起動速度の高速化を
行いました。詳細は GitHub の Release ノートをご確認ください。
```

#### Expected Judgment
- Line 1 $\to$ 2: `several\nimportant` $\implies$ **[JOIN]** with space (English wrap, mid-sentence continuation)
- Line 2 $\to$ 3: `improvements.\n本バージョンでは` $\implies$ **[KEEP]** (Full stop `.` followed by script transition from Latin to Japanese)
- Line 3 $\to$ 4: `高速化を\n行いました。` $\implies$ **[JOIN: NO SPACE]** (Japanese mid-sentence wrap across particle を)
- Line 4 $\to$ 5: `行いました。\n詳細は` $\implies$ **[JOIN: NO SPACE]** (Japanese continuation wrap without short terminal line or blank line)

---

## 6. Implementation Phasing Plan

Following agreement on this design document, implementation will be partitioned into three independent PRs:

1. **Step 1 (Core Structure Package):**
   - Package `internal/textstruct` with pure functions:
     - `CleanSpuriousNewlines(text string, lang string) string`
     - `ChunkText(text string, maxChunkChars int) []string`
   - Shared internal line analysis pipeline (`analyzeLines`).
   - Unit tests running directly against the pinned fixtures in Section 5. Zero app or UI dependencies.
2. **Step 2 (Newline Cleanup Integration):**
   - Add `cleanup_newlines` setting to `config.Profile` and Settings UI (`auto` | `off`, default `off`).
   - Wire `textstruct.CleanSpuriousNewlines` into `app.startTranslation` exclusively for the model prompt payload. Verify history and bilingual output retain untouched text.
3. **Step 3 (Chunked Long-Text Translation):**
   - Add chunking coordinator in `internal/app` or `internal/llamaserver` sequencing requests through `llama-server`.
   - Implement all-or-nothing failure/cancellation semantics (Section 3.1) and paragraph-interleaved bilingual recombination (Section 3.2).
   - Progress reporting in tray status during multi-chunk translation (e.g. `translating chunk 2 of 4...`).
