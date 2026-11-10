package platform

import (
	"fmt"
	"strings"
	"time"
)

type Clipboard interface {
	Read() (string, error)
	Write(string) error
	// ReplaceIfUnchanged atomically compares the current clipboard text with
	// expected, and replaces it with translated only when they match.
	// Windows implementation holds OpenClipboard across compare and write.
	ReplaceIfUnchanged(expected, translated string) (bool, error)
	Close() error
}

// normalizeClipboardText converts CRLF and lone CR line endings to LF and
// trims trailing spaces, tabs and line breaks.
func normalizeClipboardText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimRight(s, " \t\n")
}

// sameClipboardText reports whether current and expected match after normalising
// CRLF/CR to LF and trimming trailing spaces, tabs and line breaks.
// Leading and inner whitespace must still match exactly.
func sameClipboardText(current, expected string) bool {
	if current == expected {
		return true
	}
	return normalizeClipboardText(current) == normalizeClipboardText(expected)
}

// clipboardDiff classifies the difference between the expected clipboard text
// (captured at hotkey press) and the current clipboard text without exposing
// private clipboard contents.
type clipboardDiff struct {
	ExpectedRunes  int
	CurrentRunes   int
	FirstDiff      int // index of first differing rune, or -1 if equal
	WhitespaceOnly bool
	OneIsPrefix    bool
	Elapsed        time.Duration
}

func (d clipboardDiff) String() string {
	return fmt.Sprintf("expected_len=%d, current_len=%d, first_diff=%d, ws_only=%t, prefix=%t, elapsed=%s",
		d.ExpectedRunes, d.CurrentRunes, d.FirstDiff, d.WhitespaceOnly, d.OneIsPrefix, d.Elapsed.Round(time.Millisecond))
}

// classifyClipboardDiff compares expected and current clipboard text rune-by-rune
// without logging any clipboard content.
func classifyClipboardDiff(expected, current string, elapsed time.Duration) clipboardDiff {
	expRunes := []rune(expected)
	curRunes := []rune(current)
	minLen := len(expRunes)
	if len(curRunes) < minLen {
		minLen = len(curRunes)
	}

	firstDiff := -1
	for i := 0; i < minLen; i++ {
		if expRunes[i] != curRunes[i] {
			firstDiff = i
			break
		}
	}
	if firstDiff == -1 && len(expRunes) != len(curRunes) {
		firstDiff = minLen
	}

	oneIsPrefix := (firstDiff == -1) || (firstDiff == minLen)
	wsOnly := sameClipboardText(current, expected)

	return clipboardDiff{
		ExpectedRunes:  len(expRunes),
		CurrentRunes:   len(curRunes),
		FirstDiff:      firstDiff,
		WhitespaceOnly: wsOnly,
		OneIsPrefix:    oneIsPrefix,
		Elapsed:        elapsed,
	}
}
