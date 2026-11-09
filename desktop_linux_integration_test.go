//go:build linux && cgo && x11test

package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
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
	// Window A makes the selection while it has the focus; then Window B appears
	// and takes the focus with nothing selected in it.
	x11TestSelect("Stale Window A selection")
	x11TestCreateB()
	defer x11TestDestroyB()
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

func sendXdotool(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("xdotool", args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+os.Getenv("DISPLAY"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xdotool %v failed: %v (%s)", args, err, out)
	}
}

func waitPressed(t *testing.T, hk Hotkey, shouldFire bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(600 * time.Millisecond)
	fired := false
	for time.Now().Before(deadline) {
		p, err := hk.Pressed()
		if err != nil {
			t.Fatalf("%s: Pressed() error: %v", desc, err)
		}
		if p {
			fired = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fired != shouldFire {
		t.Fatalf("%s: fired = %v; want %v", desc, fired, shouldFire)
	}
}

func TestX11MultipleHotkeys(t *testing.T) {
	if os.Getenv("CT_TEST_X11") != "1" {
		t.Skip("run only on an isolated Xvfb display with CT_TEST_X11=1")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := platformRuntimeError(); err != nil {
		t.Fatal(err)
	}

	// 1. Register two hotkeys
	hk1, err := newHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatalf("newHotkey(Ctrl+Alt+E): %v", err)
	}
	defer hk1.Close()

	hk2, err := newHotkey("Ctrl+Alt+K")
	if err != nil {
		t.Fatalf("newHotkey(Ctrl+Alt+K): %v", err)
	}
	defer hk2.Close()

	// 2. Duplicate registration is refused (detect by keycode + normalized modifiers)
	if dup, err := newHotkey("alt+control+e"); err == nil {
		dup.Close()
		t.Fatal("duplicate registration 'alt+control+e' must be refused")
	} else if !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate error must mention 'already registered', got: %v", err)
	}

	// 3. Two hotkeys fire independently and latch resets
	sendXdotool(t, "key", "ctrl+alt+e")
	waitPressed(t, hk1, true, "hk1 after ctrl+alt+e")
	waitPressed(t, hk2, false, "hk2 after ctrl+alt+e")
	waitPressed(t, hk1, false, "hk1 second read (latched)")

	sendXdotool(t, "key", "ctrl+alt+k")
	waitPressed(t, hk2, true, "hk2 after ctrl+alt+k")
	waitPressed(t, hk1, false, "hk1 after ctrl+alt+k")
	waitPressed(t, hk2, false, "hk2 second read (latched)")

	// 4. Auto-repeat suppression: holding produces exactly one event
	sendXdotool(t, "keydown", "ctrl", "alt", "e")
	time.Sleep(80 * time.Millisecond)
	// Inject repeat keydown events for 'e' while held
	sendXdotool(t, "keydown", "e")
	sendXdotool(t, "keydown", "e")
	time.Sleep(50 * time.Millisecond)

	waitPressed(t, hk1, true, "hk1 held down should fire once")
	waitPressed(t, hk1, false, "hk1 while still held should not fire again")

	// Release
	sendXdotool(t, "keyup", "e", "alt", "ctrl")
	time.Sleep(50 * time.Millisecond)

	// Pressing again after release fires
	sendXdotool(t, "key", "ctrl+alt+e")
	waitPressed(t, hk1, true, "hk1 after release and re-press should fire")
	waitPressed(t, hk1, false, "hk1 consumed")

	// 5. CapsLock modifier variant triggers hotkey
	sendXdotool(t, "key", "Caps_Lock")
	time.Sleep(20 * time.Millisecond)
	sendXdotool(t, "key", "ctrl+alt+e")
	waitPressed(t, hk1, true, "hk1 with CapsLock ON")
	waitPressed(t, hk2, false, "hk2 with CapsLock ON after ctrl+alt+e")
	sendXdotool(t, "key", "Caps_Lock")
	time.Sleep(20 * time.Millisecond)

	// 6. NumLock modifier variant triggers hotkey
	sendXdotool(t, "key", "Num_Lock")
	time.Sleep(20 * time.Millisecond)
	sendXdotool(t, "key", "ctrl+alt+k")
	waitPressed(t, hk2, true, "hk2 with NumLock ON")
	waitPressed(t, hk1, false, "hk1 with NumLock ON after ctrl+alt+k")
	sendXdotool(t, "key", "Num_Lock")
	time.Sleep(20 * time.Millisecond)

	// 7. Close() of one leaves the other working
	if err := hk1.Close(); err != nil {
		t.Fatalf("hk1.Close(): %v", err)
	}

	sendXdotool(t, "key", "ctrl+alt+e")
	waitPressed(t, hk1, false, "closed hk1 must not fire")

	sendXdotool(t, "key", "ctrl+alt+k")
	waitPressed(t, hk2, true, "hk2 must still fire after hk1 closed")
	waitPressed(t, hk2, false, "hk2 consumed")

	// 8. Re-registering after Close() works
	hk1New, err := newHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatalf("re-registering Ctrl+Alt+E after Close failed: %v", err)
	}
	defer hk1New.Close()

	sendXdotool(t, "key", "ctrl+alt+e")
	waitPressed(t, hk1New, true, "re-registered hk1 should fire")
	waitPressed(t, hk1New, false, "re-registered hk1 consumed")

	// 9. Conflict from another client gives error for that hotkey only, leaves others working
	x11TestConflictKey('d', 12, true) // grab Ctrl+Alt+D on second X connection
	hk3, err := newHotkey("Ctrl+Alt+D")
	if err == nil {
		hk3.Close()
		x11TestConflictKey('d', 12, false)
		t.Fatal("conflicting hotkey Ctrl+Alt+D must fail")
	}
	if !strings.Contains(err.Error(), "could not grab") {
		t.Fatalf("expected 'could not grab', got: %v", err)
	}

	// Others still work!
	sendXdotool(t, "key", "ctrl+alt+k")
	waitPressed(t, hk2, true, "hk2 must work despite conflict on Ctrl+Alt+D")

	sendXdotool(t, "key", "ctrl+alt+e")
	waitPressed(t, hk1New, true, "hk1New must work despite conflict on Ctrl+Alt+D")

	// Release conflict and re-try
	x11TestConflictKey('d', 12, false)
	hk3, err = newHotkey("Ctrl+Alt+D")
	if err != nil {
		t.Fatalf("Ctrl+Alt+D should succeed after conflict released: %v", err)
	}
	defer hk3.Close()

	sendXdotool(t, "key", "ctrl+alt+d")
	waitPressed(t, hk3, true, "hk3 should fire after registration")
	waitPressed(t, hk3, false, "hk3 consumed")
}
