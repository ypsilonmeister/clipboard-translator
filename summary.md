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
