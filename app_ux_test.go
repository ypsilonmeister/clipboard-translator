package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// uxApp is an app wired for translation-flow tests: a fake clipboard, a fake
// foreground window and a recording paste function.
func uxApp(t *testing.T) (a *app, tray *fakeTray, cb *testClipboard, pastes *int, fg *uintptr) {
	t.Helper()
	a, tray, _ = newTestApp(t)
	cb = &testClipboard{}
	a.cb = cb
	win := uintptr(100)
	fg = &win
	a.foreground = func() uintptr { return *fg }
	n := 0
	pastes = &n
	a.paste = func() error { n++; return nil }
	return
}

// pending puts a translation "in flight" the way startTranslation does.
func pending(a *app) uint64 {
	a.generation++
	a.translating = true
	a.cancelCurrent = func() {}
	return a.generation
}

func TestHandleResultCopiesAndRemembers(t *testing.T) {
	a, tray, cb, pastes, _ := uxApp(t)
	cb.text = "こんにちは"
	g := pending(a)
	a.handleResult(result{original: "こんにちは", translated: "Hello", generation: g, direction: translationDirection{"ja", "en"}, window: 100, fromSelection: true})
	if cb.text != "Hello" {
		t.Fatalf("clipboard = %q", cb.text)
	}
	if items := a.hist.Items(); len(items) != 1 || items[0].Translated != "Hello" {
		t.Fatalf("history = %+v", items)
	}
	if a.translating {
		t.Fatal("translating must be cleared")
	}
	if *pastes != 0 {
		t.Fatal("auto-paste is off by default and must not paste")
	}
	if !strings.Contains(tray.status, "translation ready") {
		t.Fatalf("status = %q", tray.status)
	}
}

func TestHandleResultAutoPaste(t *testing.T) {
	a, tray, cb, pastes, fg := uxApp(t)
	a.conf.AutoPaste = true

	// Same window, fresh selection: paste.
	cb.text = "a"
	g := pending(a)
	a.handleResult(result{original: "a", translated: "A", generation: g, window: 100, fromSelection: true})
	if *pastes != 1 || !strings.Contains(tray.status, "pasted") {
		t.Fatalf("pastes=%d status=%q", *pastes, tray.status)
	}

	// Focus moved while the model worked: do not paste into the wrong window.
	cb.text = "b"
	g = pending(a)
	*fg = 200
	a.handleResult(result{original: "b", translated: "B", generation: g, window: 100, fromSelection: true})
	if *pastes != 1 {
		t.Fatalf("must not paste into another window, pastes=%d", *pastes)
	}
	if !strings.Contains(tray.lastNote(), "active window changed") || cb.text != "B" {
		t.Fatalf("note=%q clipboard=%q", tray.lastNote(), cb.text)
	}

	// Only the old clipboard was translated (no selection): never paste.
	*fg = 100
	cb.text = "c"
	g = pending(a)
	a.handleResult(result{original: "c", translated: "C", generation: g, window: 100, fromSelection: false})
	if *pastes != 1 {
		t.Fatalf("must not paste when nothing was selected, pastes=%d", *pastes)
	}

	// A paste that fails is reported, and the translation stays in the clipboard.
	a.paste = func() error { return errors.New("keys held") }
	cb.text = "d"
	g = pending(a)
	a.handleResult(result{original: "d", translated: "D", generation: g, window: 100, fromSelection: true})
	if !strings.Contains(tray.lastNote(), "could not be pasted") || cb.text != "D" {
		t.Fatalf("note=%q clipboard=%q", tray.lastNote(), cb.text)
	}
}

func TestHandleResultIgnoresStaleAndFailed(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	cb.text = "x"
	old := pending(a)
	g := pending(a) // a newer request replaced it
	a.handleResult(result{original: "x", translated: "X", generation: old})
	if cb.text != "x" || !a.translating {
		t.Fatal("a stale result must change nothing")
	}
	a.handleResult(result{original: "x", generation: g, err: errors.New("boom")})
	if cb.text != "x" || a.translating || len(a.hist.Items()) != 0 {
		t.Fatal("a failed translation must not touch the clipboard or the history")
	}
	if !strings.Contains(tray.lastNote(), "translator.log") { // fakeTray records the balloon text only
		t.Fatalf("a failure must be reported, note=%q", tray.lastNote())
	}
	n := len(tray.notes)
	g = pending(a)
	a.handleResult(result{original: "x", generation: g, err: context.Canceled})
	if len(tray.notes) != n {
		t.Fatal("a cancelled request must not show an error")
	}
	// The user copied something else meanwhile: keep it, remember nothing.
	cb.text = "typed meanwhile"
	g = pending(a)
	a.handleResult(result{original: "x", translated: "X", generation: g})
	if cb.text != "typed meanwhile" || len(a.hist.Items()) != 0 {
		t.Fatal("a result for an outdated clipboard must be discarded")
	}
}

func TestCancelFromMenu(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	cancelled := false
	g := pending(a)
	a.cancelCurrent = func() { cancelled = true }
	a.readyTip = "ready"

	if items := a.buildMenu(); items[0].ID != cmdCancel {
		t.Fatalf("while translating, Cancel must be the first menu item, got %+v", items[0])
	}
	a.handleCommand(cmdCancel)
	if !cancelled || a.translating || a.generation == g || tray.status != "ready" {
		t.Fatalf("cancelled=%v translating=%v generation=%d (was %d) status=%q", cancelled, a.translating, a.generation, g, tray.status)
	}
	for _, it := range a.buildMenu() {
		if it.ID == cmdCancel {
			t.Fatal("when idle there is nothing to cancel")
		}
	}
	// The cancelled request's late result is ignored.
	a.handleResult(result{original: "x", translated: "X", generation: g})
	if len(a.hist.Items()) != 0 {
		t.Fatal("a result of a cancelled request must be ignored")
	}
}

func TestElapsedSecondsInTooltip(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	pending(a)
	a.translateStart = time.Now().Add(-3 * time.Second)
	a.lastTranslSec = 0
	a.tickTranslationStatus()
	if !strings.Contains(tray.status, "(3s)") {
		t.Fatalf("status = %q", tray.status)
	}
	tray.status = "untouched"
	a.tickTranslationStatus() // same second: no needless tooltip update
	if tray.status != "untouched" {
		t.Fatal("the tooltip must only change when the second changes")
	}
	a.translating = false
	a.translateStart = time.Now().Add(-10 * time.Second)
	a.tickTranslationStatus()
	if tray.status != "untouched" {
		t.Fatal("nothing to show when idle")
	}
}

func TestAutoPasteToggleIsSaved(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	a.handleCommand(cmdAutoPaste)
	if !a.conf.AutoPaste {
		t.Fatal("toggle on")
	}
	if c, _ := loadConfig(a.dir); !c.AutoPaste {
		t.Fatal("the setting must be saved")
	}
	if !strings.Contains(tray.lastNote(), "pasted into the window") {
		t.Fatalf("turning it on must say what it does, got %q", tray.lastNote())
	}
	checked := false
	for _, it := range a.buildMenu() {
		if it.ID == cmdAutoPaste {
			checked = it.Checked
		}
	}
	if !checked {
		t.Fatal("the menu item must be checked")
	}
	a.handleCommand(cmdAutoPaste)
	if a.conf.AutoPaste {
		t.Fatal("toggle off")
	}
	if c, _ := loadConfig(a.dir); c.AutoPaste {
		t.Fatal("off must be saved too")
	}
}

func TestHistoryMenuCopiesAndClears(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	find := func(label string) []MenuItem {
		for _, it := range a.buildMenu() {
			if strings.HasPrefix(it.Label, label) {
				return it.Sub
			}
		}
		return nil
	}
	if sub := find("Recent translations"); len(sub) != 1 || !sub[0].Disabled {
		t.Fatalf("an empty history shows one disabled placeholder, got %+v", sub)
	}
	a.hist.Add(entry("first", "erste"))
	a.hist.Add(entry("second & more", "zweite"))
	sub := find("Recent translations")
	if len(sub) != 4 || sub[0].ID != cmdHistoryBase || sub[1].ID != cmdHistoryBase+1 || sub[3].ID != cmdHistoryClear {
		t.Fatalf("history submenu = %+v", sub)
	}
	if !strings.HasPrefix(sub[0].Label, "second & more") {
		t.Fatalf("newest first, got %q", sub[0].Label)
	}

	a.handleCommand(cmdHistoryBase + 1) // the older entry
	if cb.text != "erste" || !strings.Contains(tray.lastNote(), "Copied") {
		t.Fatalf("clipboard=%q note=%q", cb.text, tray.lastNote())
	}
	a.handleCommand(cmdHistoryBase + 99) // a stale index must do nothing
	if cb.text != "erste" {
		t.Fatal("a stale history index must be ignored")
	}
	a.handleCommand(cmdHistoryClear)
	if len(a.hist.Items()) != 0 {
		t.Fatal("history not cleared")
	}
}
