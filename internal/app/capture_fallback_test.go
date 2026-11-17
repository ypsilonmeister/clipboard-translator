package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// press runs one hotkey press whose capture ends as scripted, and returns the
// tray status once the translation is on the clipboard.
func press(t *testing.T, a *app, tray *fakeTray, task *scriptedCapture) string {
	t.Helper()
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask { return task }
	a.startTranslation(0)
	select {
	case res := <-a.results:
		a.handleResult(res)
	case <-time.After(5 * time.Second):
		t.Fatal("no result")
	}
	return tray.status
}

func countNotes(tray *fakeTray, sub string) int {
	n := 0
	for _, m := range tray.notes {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

func TestNoCopyFallbackHasItsOwnStatusAndNotifiesOnce(t *testing.T) {
	a, tray, cb, pastes, _ := uxApp(t)
	a.endpoint = translateStub(t, "Hello").URL
	a.conf.AutoPaste = true
	cb.text = "こんにちは"
	task := func() *scriptedCapture {
		return &scriptedCapture{text: "こんにちは", fallback: platform.FallbackNoCopy}
	}
	st := press(t, a, tray, task())
	if !strings.Contains(st, "no selection was copied") || strings.Contains(st, "translation ready") {
		t.Fatalf("status = %q", st)
	}
	if countNotes(tray, "No selection was copied") != 1 {
		t.Fatalf("notes = %q, want one fallback notice", tray.notes)
	}
	cb.text = "こんにちは"
	press(t, a, tray, task())
	if countNotes(tray, "No selection was copied") != 1 {
		t.Fatalf("notes = %q, the notice must show once per run", tray.notes)
	}
	if *pastes != 0 {
		t.Fatalf("a fallback must never auto-paste (pastes = %d)", *pastes)
	}
}

func TestFreshSelectionKeepsTheNormalStatus(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	a.endpoint = translateStub(t, "Hello").URL
	cb.text = "こんにちは"
	st := press(t, a, tray, &scriptedCapture{text: "こんにちは", fresh: true})
	if !strings.Contains(st, "translation ready") || len(tray.notes) != 0 {
		t.Fatalf("status = %q, notes = %q", st, tray.notes)
	}
}

func TestElevatedFallbackHasItsOwnStatusAndNotice(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	a.endpoint = translateStub(t, "Hello").URL
	cb.text = "こんにちは"
	task := &scriptedCapture{text: "こんにちは", fallback: platform.FallbackElevated}
	st := press(t, a, tray, task)
	if !strings.Contains(st, "administrator window") {
		t.Fatalf("status = %q", st)
	}
	if countNotes(tray, "runs as administrator") != 1 || task.steps != 1 {
		t.Fatalf("notes = %q, steps = %d", tray.notes, task.steps)
	}
	cb.text = "こんにちは"
	press(t, a, tray, &scriptedCapture{text: "こんにちは", fallback: platform.FallbackElevated})
	if countNotes(tray, "runs as administrator") != 1 {
		t.Fatalf("notes = %q, the notice must show once per run", tray.notes)
	}
}

func TestClipboardInputProfileNeverReportsAFallback(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	a.endpoint = translateStub(t, "Hello").URL
	a.conf.Profiles[0].Input = config.InputClipboard
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask {
		t.Fatal("a clipboard profile must not capture")
		return nil
	}
	cb.text = "こんにちは"
	a.startTranslation(0)
	select {
	case res := <-a.results:
		a.handleResult(res)
	case <-time.After(5 * time.Second):
		t.Fatal("no result")
	}
	if !strings.Contains(tray.status, "translation ready") || len(tray.notes) != 0 {
		t.Fatalf("status = %q, notes = %q", tray.status, tray.notes)
	}
}

// A press that translates nothing must not claim a translation, and must not
// use up the one notice of the run.
func TestNoCopyNoticeWaitsForATranslationOnTheClipboard(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	a.endpoint = translateStub(t, "Hello").URL
	noCopy := func(text string) *scriptedCapture {
		return &scriptedCapture{text: text, fallback: platform.FallbackNoCopy}
	}

	// Skipped: the old clipboard text is only a URL.
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask { return noCopy("https://example.com/") }
	a.startTranslation(0)
	if a.translating || len(tray.notes) != 0 || a.noCopyNotified {
		t.Fatalf("translating=%v notes=%q notified=%v after a skip", a.translating, tray.notes, a.noCopyNotified)
	}

	// Dropped: a newer press made the result stale.
	cb.text = "こんにちは"
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask { return noCopy("こんにちは") }
	a.startTranslation(0)
	stale := <-a.results
	a.abortTranslation()
	a.handleResult(stale)
	if len(tray.notes) != 0 || a.noCopyNotified {
		t.Fatalf("notes=%q notified=%v after a dropped result", tray.notes, a.noCopyNotified)
	}

	// Not written: the clipboard changed while translating.
	a.startTranslation(0)
	res := <-a.results
	cb.text = "copied meanwhile"
	a.handleResult(res)
	if countNotes(tray, "No selection was copied") != 0 || a.noCopyNotified {
		t.Fatalf("notes=%q notified=%v although the translation was not put on the clipboard", tray.notes, a.noCopyNotified)
	}

	// The first translation that does land says so.
	cb.text = "こんにちは"
	st := press(t, a, tray, noCopy("こんにちは"))
	if !strings.Contains(st, "no selection was copied") || countNotes(tray, "No selection was copied") != 1 {
		t.Fatalf("status = %q, notes = %q", st, tray.notes)
	}
}

// With notify_on_complete the fallback notice stands in for the completion
// notice once (two balloons in a row would hide the first); later presses get
// the completion notice as usual.
func TestNoCopyNoticeIsNotHiddenByTheCompletionNotice(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	a.endpoint = translateStub(t, "Hello").URL
	a.conf.NotifyOnComplete = true
	_, done := platform.CompletionNotification()
	cb.text = "こんにちは"
	press(t, a, tray, &scriptedCapture{text: "こんにちは", fallback: platform.FallbackNoCopy})
	if len(tray.notes) != 1 || !strings.Contains(tray.notes[0], "No selection was copied") {
		t.Fatalf("notes = %q, want only the fallback notice", tray.notes)
	}
	cb.text = "こんにちは"
	press(t, a, tray, &scriptedCapture{text: "こんにちは", fallback: platform.FallbackNoCopy})
	if len(tray.notes) != 2 || tray.notes[1] != done {
		t.Fatalf("notes = %q, want the completion notice on the second press", tray.notes)
	}
}

// An administrator window with no text on the clipboard: nothing is translated,
// but the user still learns why and what to do, on every press (tooltip) and
// once per run (notice).
func TestElevatedWindowWithoutClipboardTextStillSaysWhy(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	task := func() *scriptedCapture {
		return &scriptedCapture{fallback: platform.FallbackElevated, err: platform.ErrClipboardNoValue}
	}
	for i := 0; i < 2; i++ {
		a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask { return task() }
		a.startTranslation(0)
		if a.translating || a.capturing != nil {
			t.Fatal("nothing must be translated or left pending")
		}
		if !strings.Contains(tray.status, "administrator window") || !strings.Contains(tray.status, "Ctrl+C") {
			t.Fatalf("status = %q", tray.status)
		}
	}
	if countNotes(tray, "runs as administrator") != 1 || len(tray.notes) != 1 {
		t.Fatalf("notes = %q, want one administrator notice", tray.notes)
	}
}

// The tray tooltip is cut at 127 characters on Windows.
func TestFallbackStatusesFitTheTooltip(t *testing.T) {
	a, _, _, _, _ := uxApp(t)
	for _, fb := range []platform.CaptureFallback{platform.FallbackNone, platform.FallbackNoCopy, platform.FallbackElevated} {
		if s := a.readyStatus(fb); len(s) > 127 {
			t.Errorf("status %q is %d characters", s, len(s))
		}
	}
}
