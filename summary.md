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
