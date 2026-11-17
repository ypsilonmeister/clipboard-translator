# textstruct / languages family

Branch: `fix/textstruct-family`, based on current `origin/main`
`22c10303bbbf1ccfb160879ebf66eb49831d7809` (2026-11-17).
Issues #218, #205 and #222 were read in full before implementation.

## #218 — Korean spaces at chunk boundaries

`BoundarySpace` now uses `isCJKNoSpace`, which excludes Hangul from the
wide-script spacing veto. Width calculations and `BoundarySentence` retain
the existing `isCJK` behavior. Sentence boundaries are deliberately unchanged:
the reported regression was introduced only at Space boundaries, and changing
sentence planning/recombination would widen this fix.

| Repro | Before | After / decision |
| --- | --- | --- |
| `JoinChunks({"안녕하세요","세계"}, [Space])` | `안녕하세요세계` | `안녕하세요 세계` |
| `JoinChunks({"단어 단어","끝"}, [Space])` | `단어 단어끝` | `단어 단어 끝` |
| Korean pieces at `[Sentence]` | No inserted space | Unchanged; separate pre-existing limitation |
| N2: `strings.Repeat("語 ", 600) + "尾"`, budget 1200, trimmed responses | `strings.Repeat("語 ", 599) + "語尾"` | Deliberately unchanged. Excluding Hangul cannot distinguish explicit Japanese source spaces from spaces a Japanese translation should omit. Changing that would break the CJK-target guard added for #210/#215. |

Both requested examples failed before the fix and pass with it. Regression tests
also pin the sentence-boundary and N2 decisions. Full-suite command used for
each commit: `go test -count=1 ./... -skip '^TestProcessIsPerMonitorDPIAware$'`.
The excluded test requires desktop DPI state unavailable over SSH.

Validation before commit #218: full suite PASS (Windows/amd64, Go 1.27.3); git diff --check PASS.

## #205 — mixed-script direction

Changes are limited to the existing Latin-skeleton rule:

- Split tokens at whitespace, `、` and `。`. ASCII punctuation, URL/path and
  identifier handling remain intact. The exact opening-name row remains a
  guard; punctuation splitting alone cannot override the first-word veto.
- Replace the blanket hiragana-led veto with a conservative particle/inflection
  prefix list. This retains `を`, `の比較`, `する`, etc., while allowing quoted
  native words and spaced honorifics to be weighed by the existing ratios.
- Count Hangul by whitespace-delimited words. For unspaced scripts, count base
  characters without combining marks. This removes diacritic inflation in
  Thai/Khmer/Myanmar, but does not pretend to segment their words: full semantic
  parity with Han still needs language-specific segmentation and is deferred.
- Keep the first/last-word veto and the five-lowercase-word threshold. Removing
  the first/last veto contradicts existing command/name guard expectations;
  lowering the threshold widens ambiguous short-input classification. No guard
  case was removed or relaxed. No English command/prose word list was added.

Every issue row was replayed; `ja→en` below means the still-conservative result,
not a claim that it matches the human author's language.

| Issue row | Before → after source | Outcome / reason |
| --- | --- | --- |
| `The concept of わびさび is central to Japanese aesthetics and design philosophy` | ja → en | Fixed; quoted vocabulary is not a particle prefix. Added to existing table. |
| `people say お疲れ様 to each other every day after work` | ja → en | Fixed; leading お is not particle evidence. Added to existing table. |
| `田中さん、please review this document by Friday` | ja → ja | Left: opens in Japanese. Tokenization now separates please, but the existing first-word guard is retained. Original test unchanged. |
| `Please forward the attached file to 田中さん` | ja → ja | Left: last-word guard; already pinned by existing test. |
| `I hope you are well, 田中さん.` | ja → ja | Left: last-word guard and only four lowercase prose words. |
| `I love 寿司` | ja → ja | Left: too short to resolve safely; existing guard unchanged. |
| An English mail with a Japanese signature | ja → ja | Left: last-word guard protects commands too. Concrete replay: `Hello everyone, please review the attached document before our meeting.\n田中太郎`. |
| `以下を翻訳: The quick brown fox ...` | ja → ja | Left: debatable source; existing test uses `以下を翻訳: The quick brown fox jumps over the lazy dog`. First-word guard retained. |
| `Привет, this is a test of the new system` | ru → ru | Left: debatable; existing guard cannot distinguish a greeting from an imperative before a command. |
| `npm run build и потом npm run test` | en → en | Deliberately left as requested: lowercase command words look like prose without a word list. |
| `npm run build 之后再 npm run test` | en → en | Same deliberate command/prose ambiguity; no word list. |

Additional regression rows in the same existing table cover `ありがとう`, spaced
`田中 さん`, comma/period tokenization, Korean word counting, Thai/Khmer/Myanmar
marks, and attached/spaced Japanese grammar with abundant Latin prose. All ten
new positive regression cases fail on unchanged main's language code and pass
with this fix. `Docker Composeを使う`, product lists and every #187/#203/#208 guard
remain unchanged and pass.

### Direction bake-off regression gate

`internal/languages/testdata/direction_corpus.jsonl` preserves all 101 supplied
inputs, expectations, provenance and hard flags, adding `baseline_source` from
`verdicts-a.jsonl` after checking input/pair alignment. The test checks every
previously correct row individually, plus corpus size and baseline scores.
Semantic expectations (including covid19) are unchanged.

| Gate | Recorded baseline | Replayed before #205 | After #205 |
| --- | --- | --- | --- |
| en/ru overall | 88/97 | 88/97 | 88/97 |
| Hard en/ru | 18/24 | 18/24 | 18/24 |
| Cross-pair | 4/4 | 4/4 | 4/4 |

No previously correct corpus case regressed. The nine recorded misses remain:
`covid19` (existing accepted identifier trade-off), `30s`/`60s` (duration/decade
ambiguity), `app.js` (node.js product-name trade-off), and `docker-compose.yml`,
`test_utils.py`, `my-script.py`, `run-tests.sh`, `foo-bar.com` (current dotted-name
label grammar). These are outside the requested #205 script-counting changes.

Validation: post-commit #218 full suite PASS; #205 `go test -count=1 -v
./internal/languages` PASS, including all existing guards and the corpus;
`git diff --check` PASS. Full suite is also run immediately after each commit.


## #222 — PR #219 batch triage

Read the PR body, every issue comment (paginated `gh api`), inline review
comments and reviews. GitHub returns exactly three review comments:
[round 1](https://github.com/ypsilonmeister/clipboard-translator/pull/219#issuecomment-6935769162),
[round 2](https://github.com/ypsilonmeister/clipboard-translator/pull/219#issuecomment-6936344636),
[round 3](https://github.com/ypsilonmeister/clipboard-translator/pull/219#issuecomment-6937460196).
There is no posted round-4 review or inline review. The four implementation
states are `3cb3cc5`, `360132a`, `163f076`, and unreviewed `2df246b` (all in main).
The last fix was inspected in full, including its new declaration regexes,
example-introducer exception and No./Nos. reference handling.

The fixture [review219.json](internal/textstruct/testdata/review219.json) contains
all deduplicated runnable repros, positive/negative controls, merge-time probes,
and expanded punctuation/indentation/boundary constructions from those
comments, plus eight probes of the unreviewed final fix. Repeated inputs in
later reviews are represented once. Incomplete explanatory fragments such as
`Fig. 1a. The next section...` are not invented as complete repros; the exact
`See Fig. 1a. More details follow.` and public chunk-impact example are replayed.

The batch was run first against the main-based tree with #218 and #205 applied,
then again after this commit's production fix. All original concrete findings
from rounds 1–3 are already fixed. Two additional regressions from `2df246b`
were reproduced and fixed here: unanchored declaration regexes recognized
`value[0]` / `A<B>` in the middle of prose, so semicolon-wrapped prose was
incorrectly preserved as code. Require declaration types at the beginning of
the trimmed line, allowing modifiers and qualified names. Existing declaration
suffixes remain accepted (multiple variables, comments, C++ arrays after names).
No sentence splitting or wrap-joining heuristics were restructured.

| Classification after triage | Rows |
| --- | ---: |
| Already fixed / already correct preservation or positive control | 90 |
| Initially still present and safe to fix → fixed by this commit | 2 |
| Still present and deliberately left | 4 |
| Still present and safe to fix, but not fixed | 0 |
| Total | 96 |

Before: the 94 rows with unchanged expected behavior pass; the two new repros
fail exactly because their newline survives. After: all 96 replay assertions
pass, including the four tests pinning deliberate limitations. Six additional
declaration-suffix guards also pass. These counts distinguish passing tests for
accepted limitations from fixing those limitations.

### Per-repro outcomes

`\n` / `\t` denote actual control characters. `sentences` outputs are exact
slices in the JSON fixture. For `cleanup`, **join** means replacing every
newline with one space, preserving all other characters; **keep** means exact
input preservation. Long boundary inputs below use the same Go constructions
as the review, and are stored expanded in the fixture.

| ID / first review or final-fix audit | Repro input | Before → after / classification | Result / reason |
| --- | --- | --- | --- |
| `r1-min` / 1 | `The wait lasted one min. Then we left.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r1-ed` / 1 | `We consulted the latest ed. Then we revised the draft.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r1-ave` / 1 | `The street was Oak Ave. Then we turned left.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r1-no-this` / 1 | `No. This is unsupported.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r1-etc` / 1 | `We bought paper, pens, etc. Then we left.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r1-heading` / 1 | `INTRODUCTION TO\nThe following section explains all available translation profiles.` | Already fixed → unchanged | keep |
| `r1-attribution` / 1 | `Prepared by\nAlice Example, QA Department` | Already fixed → unchanged | keep |
| `r1-roman` / 1 | `(i) First item describes the preprocessing stage in full detail\n(ii) Second item describes the postprocessing stage in full detail` | Already fixed → unchanged | keep |
| `r1-stream` / 1 | `logger << "The translation is ready";\nlogger << "The next request is pending";` | Already fixed → unchanged | keep |
| `positive-proper` / 1 | `We met with\nJohn yesterday to review the\nAPI specification.` | Already fixed → unchanged | join |
| `positive-pronoun` / 1 | `During the planning meeting yesterday\nI discussed the release schedule with Alice.` | Already fixed → unchanged | join |
| `positive-acronym` / 1 | `The implementation now fully supports\nHTTP requests across multiple endpoints.` | Already fixed → unchanged | join |
| `positive-german` / 1 | `Der Bericht beschreibt zahlreiche technische\nVerfahren und nennt wichtige praktische\nBeispiele für die verwendeten neuen\nMethoden zur effizienten Verarbeitung.` | Already fixed → unchanged | join |
| `open-paren` / 1 | `This is a well-known phenomenon\n(see Figure 2) in practice.` | Already fixed → unchanged | join |
| `close-paren` / 1 | `The experiment used a stable configuration (version two)\nAnd Berlin provided the reference dataset.` | Already fixed → unchanged | join |
| `open-bracket` / 1 | `This is a well-known phenomenon\n[see Figure 2] in practice.` | Already fixed → unchanged | join |
| `close-bracket` / 1 | `The experiment used a stable configuration [version two]\nAnd Berlin provided the reference dataset.` | Already fixed → unchanged | join |
| `open-double` / 1 | `This is a well-known phenomenon\n"see Figure 2" in practice.` | Already fixed → unchanged | join |
| `close-double` / 1 | `The experiment used a stable configuration "version two"\nAnd Berlin provided the reference dataset.` | Already fixed → unchanged | join |
| `open-single` / 1 | `This is a well-known phenomenon\n'see Figure 2' in practice.` | Already fixed → unchanged | join |
| `close-single` / 1 | `The experiment used a stable configuration 'version two'\nAnd Berlin provided the reference dataset.` | Already fixed → unchanged | join |
| `open-curly-double` / 1 | `This is a well-known phenomenon\n“see Figure 2” in practice.` | Already fixed → unchanged | join |
| `close-curly-double` / 1 | `The experiment used a stable configuration “version two”\nAnd Berlin provided the reference dataset.` | Already fixed → unchanged | join |
| `open-curly-single` / 1 | `This is a well-known phenomenon\n‘see Figure 2’ in practice.` | Already fixed → unchanged | join |
| `close-curly-single` / 1 | `The experiment used a stable configuration ‘version two’\nAnd Berlin provided the reference dataset.` | Already fixed → unchanged | join |
| `trailing-semicolon` / 1 | `The experiment used a stable configuration;\nBerlin provided the reference dataset.` | Already fixed → unchanged | join |
| `trailing-colon` / 1 | `The experiment used a stable configuration:\nBerlin provided the reference dataset.` | Already fixed → unchanged | join |
| `trailing-em` / 1 | `The experiment used a stable configuration—\nBerlin provided the reference dataset.` | Already fixed → unchanged | join |
| `trailing-en` / 1 | `The experiment used a stable configuration–\nBerlin provided the reference dataset.` | Already fixed → unchanged | join |
| `guard-heading` / 1 | `Overview\nThe implementation handles several important translation workflows.` | Already fixed → unchanged | keep |
| `guard-signature` / 1 | `Taro Yamada\nSenior Director of Engineering` | Already fixed → unchanged | keep |
| `guard-long-heading` / 1 | `System Overview and Experimental Setup\nThe evaluation measures translation latency across multiple benchmarks.` | Already fixed → unchanged | keep |
| `guard-pipe` / 1 | `\| Model \| Latency \| Accuracy \|\n\| Alpha \| 20 ms \| 90% \|` | Already fixed → unchanged | keep |
| `guard-spaces` / 1 | `Model Alpha   Baseline   40%\nModel Beta    Proposed   55%` | Already fixed → unchanged | keep |
| `guard-tabs` / 1 | `Model Alpha\tBaseline\t40%\nModel Beta\tProposed\t55%` | Already fixed → unchanged | keep |
| `guard-bullet` / 1 | `The experiment used a stable configuration\n- Berlin provided the reference dataset.` | Already fixed → unchanged | keep |
| `guard-number` / 1 | `The experiment used a stable configuration\n1. Berlin provided the reference dataset.` | Already fixed → unchanged | keep |
| `guard-letter` / 1 | `The experiment used a stable configuration\n(a) Berlin provided the reference dataset.` | Already fixed → unchanged | keep |
| `indent-spaces` / 1 | `The experiment used a stable configuration\n  Berlin provided the reference dataset.` | Already fixed → unchanged | keep |
| `indent-tab` / 1 | `The experiment used a stable configuration\n\tBerlin provided the reference dataset.` | Already fixed → unchanged | keep |
| `indent-wide` / 1 | `The experiment used a stable configuration\n\u3000Berlin provided the reference dataset.` | Already fixed → unchanged | keep |
| `sentence-ellipsis-lower` / 1 | `Wait... really?` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `sentence-ellipsis-upper` / 1 | `Wait... Really? Next sentence.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `sentence-ellipsis-quoted` / 1 | `He said "Wait..." and left. Next sentence.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `sentence-double-dot` / 1 | `Wait.. really?` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `sentence-wide-ellipsis` / 1 | `Wait… really?` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `sentence-et-al` / 1 | `see Smith et al. for details` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `sentence-references` / 1 | `See Fig. 2, Eq. 3, Vol. 4, No. 5, pp. 6-7. Next sentence.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `sentence-passim` / 1 | `See passim. in the commentary. Next sentence.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `sentence-quoted-stop` / 1 | `He said "Stop." quietly to himself. Next sentence.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `sentence-ordinary` / 1 | `First sentence. Second sentence.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `sentence-digit` / 1 | `First sentence. 2024 brought new results.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `sentence-alphanumeric` / 1 | `See Fig. 1a. More details follow.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r2-fig` / 2 | `See Fig. S1 for details.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `r2-eq` / 2 | `See Eq. A1 for details.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `r2-figs` / 2 | `See Figs. A and B for details.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `r2-eg` / 2 | `We used alternative sources, e.g. The Times and The Guardian.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `r2-ie` / 2 | `The destination is clear, i.e. The Hague.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `r2-bracket` / 2 | `The experiment used a stable configuration [version two];\nBerlin provided the reference dataset.` | Already fixed → unchanged | join |
| `r2-comparison` / 2 | `The benchmark results improved by >10%;\nBerlin provided the reference dataset.` | Already fixed → unchanged | join |
| `r3-us` / 3 | `The company operates in the U.S. Then it expanded.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r3-pm` / 3 | `The meeting ended at 5 p.m. We went home.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r3-uk` / 3 | `This happened in the U.K. The results were clear.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r3-no-i` / 3 | `No. I disagree.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r3-no-a` / 3 | `No. A different approach is needed.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `r3-array` / 3 | `TranslationResult[] translatedResults;\nTranslationRequest[] pendingRequests;` | Already fixed → unchanged | keep |
| `r3-generic` / 3 | `List<string> translatedParagraphs;\nList<string> sourceParagraphs;` | Already fixed → unchanged | keep |
| `r3-chunk` / 3 | `The company operates in the U.S. Then it expanded.` | Already fixed → unchanged | 2 chunks, [Sentence], budget 40 |
| `surround-titles` / 3 | `Dr. Smith met Prof. Jones.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `surround-initials` / 3 | `J. Smith met A. Brown.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `surround-st` / 3 | `St. Louis is a city.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `surround-eds` / 3 | `See Eds. Smith and Jones for details.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `surround-army` / 3 | `The U.S. Army supplied the data.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `surround-eg-http` / 3 | `We used e.g. HTTP requests for testing.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `surround-vol` / 3 | `See Vol. II for details.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `surround-sec` / 3 | `See Sec. IV for details.` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `surround-figs` / 3 | `See Figs. A and B. Then continue.` | Already fixed → unchanged | 2 sentence(s); exact slices in fixture |
| `vocabulary-fig` / 1 | `See Fig. 2` | Already fixed → unchanged | 1 sentence(s); exact slices in fixture |
| `choice-width40` / 1 | `This paragraph has useful text\nAlice provided several helpful examples.` | Still present → deliberately left | Deliberately left: conservative geometric veto, despite 75% width. |
| `choice-width32` / 1 | `This paragraph has useful text\nAlice provided helpful examples.` | Already fixed → unchanged | join |
| `choice-extra-spaces` / 1 | `The results show a   stable improvement\nin all tested configurations.` | Still present → deliberately left | Deliberately left: three spaces are table evidence; preserve structure. |
| `choice-one-space` / 1 | `The results show a stable improvement\nin all tested configurations.` | Already fixed → unchanged | join |
| `boundary-words` / 1 | `strings.Repeat("word ", 240) + "tail\nnext"` | Already fixed → unchanged | Exact trimmed/untrimmed roundtrip; boundaries [1, 2] (None=0, Space=1, Newline=2) |
| `boundary-right-space` / 1 | `strings.Repeat("a", 1200) + " tail\nnext"` | Already fixed → unchanged | Exact trimmed/untrimmed roundtrip; boundaries [1, 2] (None=0, Space=1, Newline=2) |
| `boundary-hard-latin` / 1 | `strings.Repeat("x", 1300) + "\nnext line"` | Already fixed → unchanged | Exact trimmed/untrimmed roundtrip; boundaries [0, 2] (None=0, Space=1, Newline=2) |
| `boundary-hard-cjk` / 1 | `strings.Repeat("あ", 1300) + "\n次の行"` | Already fixed → unchanged | Exact trimmed/untrimmed roundtrip; boundaries [0, 2] (None=0, Space=1, Newline=2) |
| `join-latin` / 1 | `JoinChunks(["word", "tail"], [Space])` | Already fixed → unchanged | word tail |
| `join-cjk` / 1 | `JoinChunks(["これは日本語", "です"], [Space])` | Already fixed → unchanged | これは日本語です |
| `audit-array-prose` / 4 | `The measured value[0] remains stable;\nBerlin provided the reference dataset.` | Still present / safe → fixed by this commit | Fix: unanchored array regex mistook an inline expression for a declaration. |
| `audit-generic-prose` / 4 | `The notation A<B> describes a relation;\nBerlin provided the reference dataset.` | Still present / safe → fixed by this commit | Fix: unanchored generic regex mistook inline notation for a declaration. |
| `audit-qualified-array` / 4 | `Translation.Result[] translatedResults;\nTranslation.Request[] pendingRequests;` | Already fixed → unchanged | keep |
| `audit-qualified-generic` / 4 | `System.Collections.Generic.List<string> translatedParagraphs;\nSystem.Collections.Generic.List<string> sourceParagraphs;` | Already fixed → unchanged | keep |
| `audit-multiple-array` / 4 | `TranslationResult[] first, second;\nTranslationRequest[] pending, next;` | Already fixed → unchanged | keep |
| `audit-multiple-generic` / 4 | `List<string> first, second;\nList<string> pending, next;` | Already fixed → unchanged | keep |
| `choice-us-proper` / 4 | `The company operates in the U.S. Alice joined later.` | Still present → deliberately left | Deliberately left: Alice is not a known sentence starter and can be a proper-name continuation; requires semantic disambiguation. |
| `choice-no-roman` / 4 | `See No. V for details.` | Still present → deliberately left | Deliberately left: single-letter No. identifiers remain ambiguous with No. I / No. A responses; no contextual redesign. |


All deliberate tail decisions:

- `choice-width40`: keep the existing geometric veto even at 75% of median;
  widening it risks headings. The 30/32-width control still joins.
- `choice-extra-spaces`: three internal spaces remain table evidence; loosening
  that guard risks structural flattening. The one-space control still joins.
- `choice-us-proper`: `U.S. Alice` can be a real boundary, but the sentence-starter
  list cannot identify arbitrary names; extending it blindly would break proper
  noun continuations such as `U.S. Army`. Left for semantic disambiguation.
- `choice-no-roman`: `No. V` is still split; accepting a single capital letter
  would restore the reviewed `No. I` / `No. A` bugs. Context-sensitive reference
  disambiguation is deferred. Multi-letter Roman references remain covered.
- Declaration-shaped prose beginning with a type-like expression remains
  ambiguous. This batch only narrows the demonstrated false positives where
  notation appears inside ordinary prose; it does not parse a programming
  language or redesign code detection.
- No translation-model accuracy or live desktop interactions were exercised.
  These tests verify deterministic text/direction behavior only.

### Validation ledger

- #218: full suite PASS before commit; full suite PASS after `3550974`.
- #205: original language table and corpus PASS; full suite PASS after `f9e89ec`.
- #222: baseline replay reproduced exactly two new failures; after the fix,
  `go test -count=1 ./internal/textstruct ./internal/languages` PASS and all 96
  replay rows PASS. Full suite PASS on the final code before commit.
  `go vet ./...` PASS. Formatting and whitespace checks PASS.
- Full-suite invocation throughout: `go test -count=1 ./... -skip
  '^TestProcessIsPerMonitorDPIAware$'`. No other tests excluded. Windows/amd64,
  Go 1.27.3. DPI/desktop-dependent behavior is outside this SSH validation.
- Final DirectionFor corpus still scores 88/97 overall, 18/24 hard, 4/4
  cross-pair, with no loss of a previously correct case.
