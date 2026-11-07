package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPasteRechecksFocusAfterWaiting(t *testing.T) {
	for _, tc := range []struct {
		name                string
		target, after       uintptr
		released, wantPaste bool
	}{
		{"same target", 10, 10, true, true},
		{"changed while waiting", 10, 20, true, false},
		{"unknown target", 0, 0, true, false},
		{"modifiers held", 10, 10, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			focus, sent := tc.target, false
			err := pasteWithFocus(tc.target, func() uintptr { return focus }, func(time.Duration) bool { focus = tc.after; return tc.released }, func() error { sent = true; return nil })
			if sent != tc.wantPaste || (err == nil) != tc.wantPaste {
				t.Fatalf("sent=%v err=%v", sent, err)
			}
		})
	}
	want := errors.New("injection failed")
	err := pasteWithFocus(10, func() uintptr { return 10 }, func(time.Duration) bool { return true }, func() error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("lost paste error: %v", err)
	}
}

// The target is the window the selection came from, not whatever is in front
// when the paste starts: a focus change before the modifier wait is caught too.
func TestPasteTargetIsCarriedNotResampled(t *testing.T) {
	sent := false
	err := pasteWithFocus(10, func() uintptr { return 20 }, func(time.Duration) bool { return true }, func() error { sent = true; return nil })
	if sent || err == nil {
		t.Fatalf("pasted into a window other than the target: sent=%v err=%v", sent, err)
	}
}

// End to end through handleResult: the focus moves while the paste waits for the
// modifiers, so nothing is typed, the user is told, and the translation stays on
// the clipboard.
func TestAutoPasteFocusChangeDuringModifierWait(t *testing.T) {
	for _, tc := range []struct {
		name      string
		switchTo  uintptr
		released  bool
		wantPaste bool
		wantNote  string
	}{
		{"unchanged target", 100, true, true, ""},
		{"focus changed while waiting", 200, true, false, "could not be pasted"},
		{"modifier timeout", 100, false, false, "could not be pasted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, tray, cb, _, fg := uxApp(t)
			a.conf.AutoPaste = true
			sent := 0
			var gotTarget uintptr
			a.paste = func(target uintptr) error {
				gotTarget = target
				wait := func(time.Duration) bool { *fg = tc.switchTo; return tc.released }
				return pasteWithFocus(target, a.foreground, wait, func() error { sent++; return nil })
			}
			cb.text = "a"
			g := pending(a)
			a.handleResult(result{original: "a", translated: "A", generation: g, window: 100, fromSelection: true})
			if gotTarget != 100 {
				t.Fatalf("paste target = %d, want the window the selection came from", gotTarget)
			}
			if (sent == 1) != tc.wantPaste {
				t.Fatalf("sent=%d, want paste=%v", sent, tc.wantPaste)
			}
			if cb.text != "A" {
				t.Fatalf("the translation must stay on the clipboard, got %q", cb.text)
			}
			if tc.wantNote != "" && !strings.Contains(tray.lastNote(), tc.wantNote) {
				t.Fatalf("note = %q, want %q", tray.lastNote(), tc.wantNote)
			}
		})
	}
}
