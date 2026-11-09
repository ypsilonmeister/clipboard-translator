package main

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
)

const historySize = 10

type historyEntry struct {
	Source, Translated string
	Direction          languages.Direction
}

// history keeps the last few translations in memory only (never on disk:
// clipboard text can be sensitive). Newest first.
type history struct {
	items []historyEntry
}

// Add puts e first. An older identical translation of the same text is dropped.
func (h *history) Add(e historyEntry) {
	kept := make([]historyEntry, 0, len(h.items)+1)
	kept = append(kept, e)
	for _, old := range h.items {
		if old.Source == e.Source && old.Translated == e.Translated {
			continue
		}
		kept = append(kept, old)
	}
	if len(kept) > historySize {
		kept = kept[:historySize]
	}
	h.items = kept
}

func (h *history) Items() []historyEntry { return append([]historyEntry(nil), h.items...) }
func (h *history) Clear()                { h.items = nil }

// oneLine collapses every run of whitespace (newlines and tabs included) into
// one space and cuts the text to at most max runes, ending in "…" when cut.
func oneLine(s string, max int) string {
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	r := []rune(s)
	if max > 0 && len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// historyLabel is the menu text of an entry: "source → translation", shortened.
func historyLabel(e historyEntry) string {
	return fmt.Sprintf("%s → %s", oneLine(e.Source, 24), oneLine(e.Translated, 32))
}

type pasteAction int

const (
	pasteNone          pasteAction = iota // auto-paste off, or nothing was selected
	pasteNow                              // type Ctrl+V
	pasteWindowChanged                    // the focus moved, or is unknown: do not paste
)

// decidePaste says whether a finished translation may be pasted automatically.
// Only a fresh selection is pasted (over the selection it came from), and only
// into the very window it came from.
func decidePaste(enabled, fromSelection bool, startWindow, nowWindow uintptr) pasteAction {
	if !enabled || !fromSelection {
		return pasteNone
	}
	if startWindow == 0 || nowWindow != startWindow {
		return pasteWindowChanged
	}
	return pasteNow
}
