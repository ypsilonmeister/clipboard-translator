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
