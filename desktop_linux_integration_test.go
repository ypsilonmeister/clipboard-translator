//go:build linux && cgo && x11test

package main

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestX11Desktop(t *testing.T) {
	if os.Getenv("CT_TEST_X11") != "1" {
		t.Skip("run only on an isolated Xvfb display with CT_TEST_X11=1")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := platformRuntimeError(); err != nil {
		t.Fatal(err)
	}
	x11TestCreate()
	defer x11TestDestroy()
	cb, err := newClipboard()
	if err != nil {
		t.Fatal(err)
	}
	defer cb.Close()
	const source = "こんにちは, world 😀"
	x11TestSelect(source)
	got, fromSelection, err := captureSelectedText(cb, foregroundWindow())
	if err != nil || got != source || !fromSelection {
		t.Fatalf("capture = %q %v %v", got, fromSelection, err)
	}
	if applied, err := cb.ReplaceIfUnchanged("different copy", "wrong"); err != nil || applied {
		t.Fatalf("stale comparison: %v %v", applied, err)
	}
	if applied, err := cb.ReplaceIfUnchanged(source, "Hello"); err != nil || !applied {
		t.Fatalf("replace: %v %v", applied, err)
	}
	if got, err := cb.Read(); err != nil || got != "Hello" {
		t.Fatalf("clipboard = %q %v", got, err)
	}
	if foregroundWindow() == 0 {
		t.Fatal("test window has no focus")
	}
	if err := pasteIntoForeground(foregroundWindow()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for x11TestText() != "Hello" && time.Now().Before(deadline) {
		pumpMessages()
		time.Sleep(10 * time.Millisecond)
	}
	if got := x11TestText(); got != "Hello" {
		t.Fatalf("pasted = %q", got)
	}

	// Issue #83: A stale PRIMARY selection from Window A must not be treated as a
	// selection in Window B (which would wrongly auto-paste into Window B).
	x11TestCreateB()
	defer x11TestDestroyB()

	// Window A retains the selection. Switch focus to Window B with nothing selected.
	x11TestSelect("Stale Window A selection")
	x11TestFocusB()
	winB := foregroundWindow()
	if winB == 0 {
		t.Fatal("Window B has no focus")
	}
	if err := cb.Write("unrelated clipboard text"); err != nil {
		t.Fatal(err)
	}
	gotStale, freshStale, err := captureSelectedText(cb, winB)
	if err != nil {
		t.Fatalf("capture in Window B failed: %v", err)
	}
	if freshStale {
		t.Fatalf("stale selection from Window A must not be treated as fresh in Window B (fresh=%v, got=%q)", freshStale, gotStale)
	}
	// The text is still captured (the owner check is a heuristic, see
	// captureWithPrimary); it just must not count as fresh.
	if gotStale != "Stale Window A selection" {
		t.Fatalf("expected the PRIMARY text, got %q", gotStale)
	}
	if act := decidePaste(true, freshStale, winB, foregroundWindow()); act != pasteNone {
		t.Fatalf("decidePaste on stale selection must be pasteNone, got %v", act)
	}

	// But selecting text in Window B itself must be detected as a fresh selection in Window B.
	const bSource = "Window B text"
	x11TestSelectB(bSource)
	gotB, freshB, err := captureSelectedText(cb, foregroundWindow())
	if err != nil || gotB != bSource || !freshB {
		t.Fatalf("same-window capture in Window B failed: got=%q fresh=%v err=%v", gotB, freshB, err)
	}
	if act := decidePaste(true, freshB, winB, foregroundWindow()); act != pasteNow {
		t.Fatalf("decidePaste for fresh selection in Window B must be pasteNow, got %v", act)
	}

	hk, err := newHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatal(err)
	}
	defer hk.Close()
	// Synthetic keys go exclusively to this disposable Xvfb display.
	x11TestKey(0xffe3, true) // Control_L
	x11TestKey(0xffe9, true) // Alt_L
	x11TestKey('e', true)
	x11TestKey('e', false)
	x11TestKey(0xffe9, false)
	x11TestKey(0xffe3, false)
	fired := false
	deadline = time.Now().Add(time.Second)
	for !fired && time.Now().Before(deadline) {
		fired, err = hk.Pressed()
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || !fired {
		t.Fatalf("hotkey did not fire: %v", err)
	}
	if again, _ := hk.Pressed(); again {
		t.Fatal("hotkey fired twice")
	}
	if err := hk.Close(); err != nil {
		t.Fatal(err)
	}
	x11TestConflict(true)
	if unexpected, err := newHotkey("Ctrl+Alt+E"); err == nil {
		unexpected.Close()
		x11TestConflict(false)
		t.Fatal("a binding owned by another X11 client must be rejected")
	}
	x11TestConflict(false)
	hk, err = newHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatalf("binding must recover after a conflict: %v", err)
	}
	defer hk.Close()

	tray := newTray("/tmp/test-translator.log")
	defer tray.Close()
	tray.SetMenu(func() []MenuItem { return []MenuItem{{ID: cmdCancel, Label: "Cancel"}} })
	x11TestMenuClick(cmdCancel)
	pumpMessages()
	if id, ok := tray.NextCommand(); !ok || id != cmdCancel {
		t.Fatalf("menu command = %d %v", id, ok)
	}
	x11TestMenuClick(2)
	pumpMessages()
	if !tray.QuitRequested() {
		t.Fatal("Quit must work while startup is pumping messages")
	}
}
