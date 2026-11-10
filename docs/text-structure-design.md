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

The detector is a pure, deterministic function over UTF-8 text: `AnalyzeText(raw string) AnalyzedDocument`. It operates without external dependencies or ML models.

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
A soft line break between line $L_i$ and line $L_{i+1}$ is considered *spurious* only when multiple corroborating signals agree and neither line is a preserved structure.

#### A. Geometric Signal (Column Uniformity)
- In fixed-width wraps (PDFs, terminal output), successive lines share a nearly identical visual width.
- Let $W(L)$ be the display width (East Asian Width aware, e.g. full-width = 2, half-width = 1).
- If lines $L_i, L_{i-1}, L_{i-2}$ cluster around a consistent column margin (e.g. standard deviation of width < 5 runes, or $|W(L_i) - \text{median}| \le 3$), line $L_i$ was wrapped by page geometry rather than semantic intent.
- Exception: The terminal line of a wrapped paragraph is typically significantly shorter than the column margin, which serves as a natural paragraph end signal.

#### B. Punctuation & Boundary Signals
- **Latin / Western Scripts:**
  - $L_i$ does **not** end in sentence-ending punctuation (`.`, `!`, `?`, `:`, `;`).
  - $L_{i+1}$ begins with a lowercase letter (`[a-z]`), continuation punctuation, or lowercase accent.
  - Hyphenated wrap: $L_i$ ends with an alphanumeric followed by `-` (ASCII hyphen) and $L_{i+1}$ begins with `[a-z]`. The break and the hyphen should be joined as a single word (e.g., `trans-` + `lation` $\to$ `translation`), while preserving true compound words where dictionary or casing dictates.
- **Japanese / CJK Scripts:**
  - $L_i$ does **not** end in Japanese sentence-ending punctuation (`。`, `！`, `？`, `…`, `―`) or closing brackets (`」`, `』`, `）`, `】`, `”`).
  - $L_i$ ends with, or $L_{i+1}$ begins with, a particle, connector, or mid-sentence token (hiragana particle like は, が, を, に, で, と, も; kanji character; opening bracket).
  - Joining does **not** insert an ASCII space (unlike Western scripts where `\n` is replaced by a single space ` `).

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
4. **Context & Boundary Awareness:**
   - Bounded concurrency: Chunks are translated sequentially (or with a concurrency of 1) to respect the single local `llama-server` process without overloading VRAM/RAM.
   - Output reconstitution: Chunks are reassembled using their original delimiter tokens (`\n\n` for paragraphs, ` ` or empty string for intra-paragraph sentence splits).

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
  - *Option B (Recommended):* **Derived as $\min(\text{model\_context\_tokens} / 4, \text{max\_chars})$ with a conservative safety cap of 1,200 characters (~300–400 tokens) per chunk.**
- **Rationale:**
  PLaMo 3 Translate handles 53 languages. Token expansion ratios vary drastically between scripts (e.g. English text to Japanese or Thai). A 1,200-character budget guarantees that prompt overhead, source tokens, and generated target tokens fit comfortably inside the default `2048` context window with zero risk of truncation (`ErrTruncated`), while keeping paragraph context coherent.

### Q3: Does cleanup apply to the stored history and bilingual output, or only to the prompt sent to the model?
- **Options considered:**
  - *Option A:* Replace original text everywhere (in prompt, history, and bilingual output).
  - *Option B (Recommended):* **Cleanup applies strictly to the text sent to the translation model. The user's original raw clipboard text is preserved verbatim in memory history and in bilingual (`formatBilingual`) output.**
- **Rationale:**
  The clipboard translator's primary contract is **clipboard safety and non-destructive operation**. If a user copies text, translates it in bilingual mode, and pastes it, they expect *their original text* to be what they copied, not an algorithm's guess at how it should be reflowed. Furthermore, if the heuristic ever made a mistake, the user still has their untampered original in the history tray menu.

### Q4: Should hyphenated words across wrapped lines be un-hyphenated?
- **Options considered:**
  - *Option A:* Strip all trailing hyphens when joining wrapped lines (`con-` + `nect` $\to$ `connect`).
  - *Option B (Recommended):* **Un-hyphenate only when both halves match standard word-break patterns (`[a-zA-Z]-` followed by `\n[a-z]`), but leave punctuation hyphens, em-dashes, and markdown list hyphens intact.**
- **Rationale:**
  Hyphenated line breaks are the single biggest source of degraded translations in English PDFs (e.g., `infor-` + `mation` is translated as two garbage tokens instead of "information"). Stripping `-\n` when followed by a lowercase letter is unambiguous and safe.

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
- Line 1 $\to$ 2: `transformer-\nbased` $\implies$ **[JOIN: DE-HYPHENATE]** $\to$ `transformer-based` (or `transformerbased` if soft hyphen)
- Line 2 $\to$ 3: `trans-\nlation` $\implies$ **[JOIN: DE-HYPHENATE]** $\to$ `translation`
- Line 3 $\to$ 4: `devices.\nFurthermore,` $\implies$ **[JOIN]** with space (uniform line length, continuation of sentence)
- Line 4 $\to$ 5: `40%\nwithout` $\implies$ **[JOIN]** with space (mid-sentence continuation, lowercase next)
- **Resulting Text for Model:**
  `The proposed architecture introduces a novel transformer-based attention mechanism that significantly improves translation quality on resource-constrained edge devices. Furthermore, the computational latency is reduced by 40% without observable loss in accuracy.`

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
- Line 3 $\to$ 4: `行った。\nその結果、` $\implies$ **[JOIN: NO SPACE]** (uniform column wrap without blank line)
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
- Line 4 $\to$ 5: `行いました。\n詳細は` $\implies$ **[JOIN: NO SPACE]** (Japanese continuation wrap without blank line)

---

## 6. Implementation Phasing Plan

Following agreement on this design document, implementation will be partitioned into three independent PRs:

1. **Step 1 (Core Structure Package):**
   - Package `internal/textstruct` with pure functions `CleanSpuriousNewlines(text, lang)` and `ChunkText(text, maxChars)`.
   - Unit tests running directly against the pinned fixtures in Section 5. Zero app or UI dependencies.
2. **Step 2 (Newline Cleanup Integration):**
   - Add `cleanup_newlines` setting to `config.Profile` and Settings UI.
   - Wire `textstruct.CleanSpuriousNewlines` into `app.startTranslation` exclusively for the model prompt payload. Verify history and bilingual output retain untouched text.
3. **Step 3 (Chunked Long-Text Translation):**
   - Add chunking coordinator in `internal/app` or `internal/llamaserver` to sequence requests through `llama-server`.
   - Progress reporting in tray status during multi-chunk translation (e.g. `translating chunk 2 of 4...`).
