package main

import (
	"fmt"
	"strings"
	"testing"
)

func entry(src, dst string) historyEntry {
	return historyEntry{Source: src, Translated: dst, Direction: translationDirection{"en", "ja"}}
}

func TestHistoryOrderLimitAndDedupe(t *testing.T) {
	var h history
	for i := 0; i < historySize+5; i++ {
		h.Add(entry(fmt.Sprint("src", i), fmt.Sprint("dst", i)))
	}
	items := h.Items()
	if len(items) != historySize {
		t.Fatalf("history must keep at most %d entries, has %d", historySize, len(items))
	}
	if items[0].Source != fmt.Sprint("src", historySize+4) {
		t.Fatalf("newest must come first, got %q", items[0].Source)
	}
	if last := items[len(items)-1].Source; last != "src5" {
		t.Fatalf("the oldest entries must fall off, last is %q", last)
	}

	// Translating the same text again moves it to the front instead of duplicating it.
	h.Add(entry("src7", "dst7"))
	items = h.Items()
	if items[0].Source != "src7" || len(items) != historySize {
		t.Fatalf("a repeat must move to the front: first=%q len=%d", items[0].Source, len(items))
	}
	n := 0
	for _, e := range items {
		if e.Source == "src7" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("src7 appears %d times", n)
	}

	// Items returns a copy: changing it must not change the history.
	items[0].Source = "tampered"
	if h.Items()[0].Source != "src7" {
		t.Fatal("Items must return a copy")
	}
	h.Clear()
	if len(h.Items()) != 0 {
		t.Fatal("Clear must empty the history")
	}
}

func TestOneLine(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello\n  world\t!", 50, "hello world !"},
		{"  padded  ", 50, "padded"},
		{"line1\r\nline2\r\n\r\nline3", 50, "line1 line2 line3"},
		{"こんにちは世界、これは長い文章です", 8, "こんにちは世界…"}, // counts runes, not bytes
		{"short", 5, "short"},
		{"exactly6", 8, "exactly6"},
		{"", 5, ""},
	}
	for _, c := range cases {
		got := oneLine(c.in, c.max)
		if got != c.want {
			t.Errorf("oneLine(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
		if n := len([]rune(got)); c.max > 0 && n > c.max {
			t.Errorf("oneLine(%q, %d) is %d runes, over the limit", c.in, c.max, n)
		}
	}
}

func TestHistoryLabel(t *testing.T) {
	got := historyLabel(entry("Hello,\nworld", "こんにちは\n世界"))
	if got != "Hello, world → こんにちは 世界" {
		t.Fatalf("label = %q", got)
	}
	if strings.ContainsAny(got, "\r\n\t") {
		t.Fatal("a menu label must be a single line")
	}
	long := historyLabel(entry(strings.Repeat("a", 100), strings.Repeat("あ", 100)))
	if n := len([]rune(long)); n > 24+len(" → ")+32 {
		t.Fatalf("label too long: %d runes", n)
	}
}

func TestDecidePaste(t *testing.T) {
	cases := []struct {
		name           string
		enabled, fresh bool
		start, now     uintptr
		want           pasteAction
	}{
		{"off", false, true, 5, 5, pasteNone},
		{"on, same window, fresh selection", true, true, 5, 5, pasteNow},
		{"on, but only the old clipboard was translated", true, false, 5, 5, pasteNone},
		{"on, focus moved to another window", true, true, 5, 6, pasteWindowChanged},
		{"on, focus lost altogether", true, true, 5, 0, pasteWindowChanged},
		{"on, window unknown at the start", true, true, 0, 0, pasteWindowChanged},
	}
	for _, c := range cases {
		if got := decidePaste(c.enabled, c.fresh, c.start, c.now); got != c.want {
			t.Errorf("%s: decidePaste = %v, want %v", c.name, got, c.want)
		}
	}
}
