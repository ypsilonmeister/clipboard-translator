package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// captureEnv is a scripted desktop for captureWith: a clipboard, a sequence
// counter, a foreground window and the hooks that let a test change them at the
// moment the real code would be waiting or injecting keys.
type captureEnv struct {
	clip      *testClipboard
	seq       uint32
	fg        uintptr
	copies    int
	onWait    func(e *captureEnv) bool // runs while "waiting" for the modifiers
	onCopy    func(e *captureEnv)      // what the target app does with Ctrl+C
	copyError error
}

func (e *captureEnv) ops() captureOps {
	return captureOps{
		foreground:    func() uintptr { return e.fg },
		waitModifiers: func(time.Duration) bool { return e.onWait == nil || e.onWait(e) },
		sequence:      func() uint32 { return e.seq },
		sendCopy: func() error {
			e.copies++
			if e.onCopy != nil {
				e.onCopy(e)
			}
			return e.copyError
		},
		modifierTimeout: time.Second,
		copyTimeout:     50 * time.Millisecond,
	}
}

// copySelection makes Ctrl+C put text on the clipboard.
func copySelection(text string) func(*captureEnv) {
	return func(e *captureEnv) { e.clip.text = text; e.seq++ }
}

func TestCaptureReadsFreshSelection(t *testing.T) {
	e := &captureEnv{clip: &testClipboard{text: "old"}, fg: 7, onCopy: copySelection("selected")}
	got, fresh, err := captureWith(e.clip, 7, e.ops())
	if err != nil || got != "selected" || !fresh || e.copies != 1 {
		t.Fatalf("got=%q fresh=%v err=%v copies=%d", got, fresh, err, e.copies)
	}
}

func TestCaptureFallsBackToClipboardWhenNothingSelected(t *testing.T) {
	e := &captureEnv{clip: &testClipboard{text: "copied earlier"}, fg: 7}
	got, fresh, err := captureWith(e.clip, 7, e.ops())
	if err != nil || got != "copied earlier" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v", got, fresh, err)
	}
	// Keys that cannot be injected degrade the same way.
	e = &captureEnv{clip: &testClipboard{text: "copied earlier"}, fg: 7, copyError: errors.New("blocked")}
	if got, fresh, err = captureWith(e.clip, 7, e.ops()); err != nil || got != "copied earlier" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v", got, fresh, err)
	}
}

// Issue #22: modifiers held past the timeout must not translate stale text.
func TestCaptureAbortsWhenModifiersStayHeld(t *testing.T) {
	e := &captureEnv{clip: &testClipboard{text: "stale"}, fg: 7, onWait: func(*captureEnv) bool { return false }}
	got, fresh, err := captureWith(e.clip, 7, e.ops())
	if !errors.Is(err, errCaptureModifiersHeld) || got != "" || fresh || e.copies != 0 {
		t.Fatalf("got=%q fresh=%v err=%v copies=%d", got, fresh, err, e.copies)
	}
}

// Issue #22: focus moving during the modifier wait must not send Ctrl+C to the new window.
func TestCaptureAbortsWhenFocusChangesDuringModifierWait(t *testing.T) {
	e := &captureEnv{clip: &testClipboard{text: "stale"}, fg: 7, onCopy: copySelection("other window's text"),
		onWait: func(e *captureEnv) bool { e.fg = 8; return true }}
	got, _, err := captureWith(e.clip, 7, e.ops())
	if !errors.Is(err, errCaptureFocusChanged) || got != "" || e.copies != 0 {
		t.Fatalf("got=%q err=%v copies=%d", got, err, e.copies)
	}
}

// Issue #22: a clipboard update during the modifier wait is not a selection.
func TestCaptureSequenceIsMeasuredAcrossTheCopy(t *testing.T) {
	e := &captureEnv{clip: &testClipboard{text: "old"}, fg: 7,
		onWait: func(e *captureEnv) bool { e.clip.text = "changed meanwhile"; e.seq += 5; return true }}
	got, fresh, err := captureWith(e.clip, 7, e.ops())
	if err != nil || fresh || got != "changed meanwhile" || e.copies != 1 {
		t.Fatalf("got=%q fresh=%v err=%v copies=%d", got, fresh, err, e.copies)
	}
}

func TestStartTranslationReportsAbortedCapture(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errCaptureModifiersHeld, "still held down"},
		{errCaptureFocusChanged, "focused window changed"},
	} {
		a, tray, cb, _, _ := uxApp(t)
		cb.text = "stale clipboard"
		a.capture = func(Clipboard, uintptr) (string, bool, error) { return "", false, tc.err }
		a.startTranslation(0)
		if a.translating || len(a.hist.Items()) != 0 {
			t.Fatal("an aborted capture must not start a translation")
		}
		if n := tray.lastNote(); !strings.Contains(n, tc.want) || !strings.Contains(n, "Nothing was translated") {
			t.Fatalf("note = %q, want mention of %q", n, tc.want)
		}
		if cb.text != "stale clipboard" {
			t.Fatalf("the clipboard must be left alone, got %q", cb.text)
		}
	}
}

func TestStartTranslationPassesHotkeyWindowToCapture(t *testing.T) {
	a, _, _, _, fg := uxApp(t)
	*fg = 42
	var got uintptr
	a.capture = func(_ Clipboard, win uintptr) (string, bool, error) {
		got = win
		return "", false, errors.New("stop here")
	}
	a.startTranslation(0)
	if got != 42 {
		t.Fatalf("capture window = %d, want the window in front at the hotkey press", got)
	}
}

func TestCaptureWithPrimaryOwnerMatches(t *testing.T) {
	cb := &testClipboard{text: "existing"}
	ops := primaryOps{
		ownerMatches: func(win uintptr) bool { return win == 10 },
		readPrimary:  func() string { return "fresh selection" },
	}
	got, fresh, err := captureWithPrimary(cb, 10, ops)
	if err != nil || got != "fresh selection" || !fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want fresh selection", got, fresh, err)
	}
	if cb.text != "fresh selection" || cb.writes != 1 {
		t.Fatalf("selection must be staged on clipboard: text=%q writes=%d", cb.text, cb.writes)
	}
}

func TestCaptureWithPrimaryStaleFromOtherWindow(t *testing.T) {
	cb := &testClipboard{text: "existing clipboard"}
	ops := primaryOps{
		// Selection is owned by window 5, but window 10 is active
		ownerMatches: func(win uintptr) bool { return false },
		readPrimary:  func() string { return "stale selection from window 5" },
	}
	got, fresh, err := captureWithPrimary(cb, 10, ops)
	if err != nil || got != "existing clipboard" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want stale selection rejected and existing clipboard returned", got, fresh, err)
	}
	if cb.writes != 0 {
		t.Fatalf("stale selection must not be written to clipboard: writes=%d", cb.writes)
	}
}

func TestCaptureWithPrimaryOwnerMatchesEmptyText(t *testing.T) {
	cb := &testClipboard{text: "existing clipboard"}
	ops := primaryOps{
		ownerMatches: func(win uintptr) bool { return true },
		readPrimary:  func() string { return "" },
	}
	got, fresh, err := captureWithPrimary(cb, 10, ops)
	if err != nil || got != "existing clipboard" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want empty selection falling back to clipboard", got, fresh, err)
	}
	if cb.writes != 0 {
		t.Fatalf("clipboard must not be written when primary is empty: writes=%d", cb.writes)
	}
}

func TestCaptureWithPrimaryZeroWindow(t *testing.T) {
	cb := &testClipboard{text: "existing clipboard"}
	ops := primaryOps{
		ownerMatches: func(win uintptr) bool { return true },
		readPrimary:  func() string { return "selection" },
	}
	got, fresh, err := captureWithPrimary(cb, 0, ops)
	if err != nil || got != "existing clipboard" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want zero window falling back to clipboard", got, fresh, err)
	}
}

type failingWriteClipboard struct {
	testClipboard
}

func (c *failingWriteClipboard) Write(string) error {
	return errors.New("write failed")
}

func TestCaptureWithPrimaryWriteError(t *testing.T) {
	cb := &failingWriteClipboard{testClipboard: testClipboard{text: "existing"}}
	ops := primaryOps{
		ownerMatches: func(win uintptr) bool { return true },
		readPrimary:  func() string { return "selection" },
	}
	got, fresh, err := captureWithPrimary(cb, 10, ops)
	if err == nil || got != "" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want write error", got, fresh, err)
	}
}
