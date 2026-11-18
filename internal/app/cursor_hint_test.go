package app

import (
	"errors"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

type fakeCursorHint struct {
	visible bool
	shows   int
	onHide  func()
}

func (h *fakeCursorHint) Show() {
	h.visible = true
	h.shows++
}
func (h *fakeCursorHint) Hide() {
	if h.visible && h.onHide != nil {
		h.onHide()
	}
	h.visible = false
}
func (*fakeCursorHint) Close() {}

func TestCursorHintFollowsCurrentHotkey(t *testing.T) {
	a, _, cb, _, _ := uxApp(t)
	hint := &fakeCursorHint{}
	a.cursorHint = hint
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask {
		return &scriptedCapture{done: func() bool { return false }}
	}
	a.startTranslation(0)
	if hint.shows != 0 {
		t.Fatal("cursor hint shown with the setting disabled")
	}

	a.conf.ShowCursorHint = true
	a.startTranslation(0)
	old := a.generation
	a.startTranslation(0)
	if !hint.visible || hint.shows != 2 || a.trayState != platform.TrayBusy {
		t.Fatalf("hotkey starts: visible=%v shows=%d tray=%v", hint.visible, hint.shows, a.trayState)
	}
	a.handleResult(result{generation: old, err: errors.New("stale result")})
	if !hint.visible {
		t.Fatal("stale result hid the current hint")
	}

	cb.text = "こんにちは"
	hint.onHide = func() {
		if cb.text != "Hello" {
			t.Fatal("hint disappeared before the translation reached the clipboard")
		}
	}
	a.handleResult(result{generation: a.generation, original: cb.text, translated: "Hello"})
	if hint.visible || a.trayState != platform.TrayDone {
		t.Fatalf("success: visible=%v tray=%v", hint.visible, a.trayState)
	}
}

func TestCursorHintHidesOnFailureOrAbort(t *testing.T) {
	for _, ending := range []string{"capture error", "skipped input", "translation error", "abort"} {
		t.Run(ending, func(t *testing.T) {
			a, _, _, _, _ := uxApp(t)
			hint := &fakeCursorHint{}
			a.cursorHint = hint
			a.conf.ShowCursorHint = true
			task := &scriptedCapture{done: func() bool { return false }}
			a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask { return task }
			a.startTranslation(0)
			g := a.generation
			switch ending {
			case "capture error":
				task.err = errors.New("capture failed")
				task.done = nil
				a.stepCapture()
			case "skipped input":
				task.done = nil
				a.stepCapture()
			case "translation error":
				a.handleResult(result{generation: g, err: errors.New("translation failed")})
			case "abort":
				a.abortTranslation()
				a.handleResult(result{generation: g, translated: "late result"})
			}
			if hint.visible || hint.shows != 1 {
				t.Fatalf("after %s: visible=%v shows=%d", ending, hint.visible, hint.shows)
			}
		})
	}
}
