//go:build linux && cgo && x11test

package main

import (
	"os"
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
	t.Cleanup(func() { hk.Close() })
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
	conflictDesk := true
	t.Cleanup(func() {
		if conflictDesk {
			x11TestConflict(false)
			conflictDesk = false
		}
	})
	if unexpected, err := newHotkey("Ctrl+Alt+E"); err == nil {
		unexpected.Close()
		x11TestConflict(false)
		conflictDesk = false
		t.Fatal("a binding owned by another X11 client must be rejected")
	}
	x11TestConflict(false)
	conflictDesk = false
	hk, err = newHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatalf("binding must recover after a conflict: %v", err)
	}
	t.Cleanup(func() { hk.Close() })

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

const (
	x11XK_Control_L = 0xffe3
	x11XK_Alt_L     = 0xffe9
	x11XK_Shift_L   = 0xffe1
	x11XK_Super_L   = 0xffeb
	x11XK_Caps_Lock = 0xffe5
	x11XK_Num_Lock  = 0xff7f
)

func sendKey(sym uint64) {
	x11TestKey(sym, true)
	time.Sleep(5 * time.Millisecond)
	x11TestKey(sym, false)
	time.Sleep(5 * time.Millisecond)
}

func sendCombo(syms ...uint64) {
	for _, sym := range syms {
		x11TestKey(sym, true)
	}
	time.Sleep(5 * time.Millisecond)
	for i := len(syms) - 1; i >= 0; i-- {
		x11TestKey(syms[i], false)
	}
	time.Sleep(5 * time.Millisecond)
}

func waitPressed(t *testing.T, hk Hotkey, shouldFire bool, desc string) {
	t.Helper()
	timeout := 600 * time.Millisecond
	if !shouldFire {
		if p, err := hk.Pressed(); err != nil {
			t.Fatalf("%s: Pressed() error: %v", desc, err)
		} else if p {
			t.Fatalf("%s: fired unexpectedly on initial drain", desc)
		}
		timeout = 80 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
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
	if !shouldFire && !fired {
		p, err := hk.Pressed()
		if err != nil {
			t.Fatalf("%s: extra Pressed() error: %v", desc, err)
		}
		if p {
			fired = true
		}
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
	t.Cleanup(func() { hk1.Close() })

	hk2, err := newHotkey("Ctrl+Alt+K")
	if err != nil {
		t.Fatalf("newHotkey(Ctrl+Alt+K): %v", err)
	}
	t.Cleanup(func() { hk2.Close() })

	// 2. Duplicate registration is refused (detect by keycode + normalized modifiers)
	if dup, err := newHotkey("alt+control+e"); err == nil {
		dup.Close()
		t.Fatal("duplicate registration 'alt+control+e' must be refused")
	} else if !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate error must mention 'already registered', got: %v", err)
	}

	// 3. Two hotkeys fire independently and latch resets
	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'e')
	waitPressed(t, hk1, true, "hk1 after ctrl+alt+e")
	waitPressed(t, hk2, false, "hk2 after ctrl+alt+e")
	waitPressed(t, hk1, false, "hk1 second read (latched)")

	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'k')
	waitPressed(t, hk2, true, "hk2 after ctrl+alt+k")
	waitPressed(t, hk1, false, "hk1 after ctrl+alt+k")
	waitPressed(t, hk2, false, "hk2 second read (latched)")

	// 4. Auto-repeat suppression: holding produces exactly one event
	x11TestKey(x11XK_Control_L, true)
	x11TestKey(x11XK_Alt_L, true)
	x11TestKey('e', true)
	time.Sleep(80 * time.Millisecond)
	// Inject repeat keydown events for 'e' while held
	x11TestKey('e', true)
	x11TestKey('e', true)
	time.Sleep(50 * time.Millisecond)

	waitPressed(t, hk1, true, "hk1 held down should fire once")
	waitPressed(t, hk1, false, "hk1 while still held should not fire again")

	// Release
	x11TestKey('e', false)
	x11TestKey(x11XK_Alt_L, false)
	x11TestKey(x11XK_Control_L, false)
	time.Sleep(50 * time.Millisecond)

	// Pressing again after release fires
	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'e')
	waitPressed(t, hk1, true, "hk1 after release and re-press should fire")
	waitPressed(t, hk1, false, "hk1 consumed")

	// 5. CapsLock modifier variant triggers hotkey
	capsOn := false
	toggleCaps := func() {
		sendKey(x11XK_Caps_Lock)
		capsOn = !capsOn
	}
	t.Cleanup(func() {
		if capsOn {
			toggleCaps()
		}
	})

	toggleCaps()
	time.Sleep(20 * time.Millisecond)
	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'e')
	waitPressed(t, hk1, true, "hk1 with CapsLock ON")
	waitPressed(t, hk2, false, "hk2 with CapsLock ON after ctrl+alt+e")
	toggleCaps()
	time.Sleep(20 * time.Millisecond)

	// 6. NumLock modifier variant triggers hotkey
	numOn := false
	toggleNum := func() {
		sendKey(x11XK_Num_Lock)
		numOn = !numOn
	}
	t.Cleanup(func() {
		if numOn {
			toggleNum()
		}
	})

	toggleNum()
	time.Sleep(20 * time.Millisecond)
	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'k')
	waitPressed(t, hk2, true, "hk2 with NumLock ON")
	waitPressed(t, hk1, false, "hk1 with NumLock ON after ctrl+alt+k")
	toggleNum()
	time.Sleep(20 * time.Millisecond)

	// 7. Close() of one leaves the other working
	if err := hk1.Close(); err != nil {
		t.Fatalf("hk1.Close(): %v", err)
	}

	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'e')
	waitPressed(t, hk1, false, "closed hk1 must not fire")

	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'k')
	waitPressed(t, hk2, true, "hk2 must still fire after hk1 closed")
	waitPressed(t, hk2, false, "hk2 consumed")

	// 8. Re-registering after Close() works
	hk1New, err := newHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatalf("re-registering Ctrl+Alt+E after Close failed: %v", err)
	}
	t.Cleanup(func() { hk1New.Close() })

	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'e')
	waitPressed(t, hk1New, true, "re-registered hk1 should fire")
	waitPressed(t, hk1New, false, "re-registered hk1 consumed")

	// 9. Conflict from another client gives error for that hotkey only, leaves others working
	x11TestConflictKey('d', 12, true) // grab Ctrl+Alt+D on second X connection
	conflictD := true
	t.Cleanup(func() {
		if conflictD {
			x11TestConflictKey('d', 12, false)
			conflictD = false
		}
	})
	hk3, err := newHotkey("Ctrl+Alt+D")
	if err == nil {
		hk3.Close()
		x11TestConflictKey('d', 12, false)
		conflictD = false
		t.Fatal("conflicting hotkey Ctrl+Alt+D must fail")
	}
	if !strings.Contains(err.Error(), "already used by another") && !strings.Contains(err.Error(), "could not grab") {
		t.Fatalf("expected conflict error, got: %v", err)
	}

	// Others still work!
	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'k')
	waitPressed(t, hk2, true, "hk2 must work despite conflict on Ctrl+Alt+D")

	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'e')
	waitPressed(t, hk1New, true, "hk1New must work despite conflict on Ctrl+Alt+D")

	// Release conflict and re-try
	x11TestConflictKey('d', 12, false)
	conflictD = false
	hk3, err = newHotkey("Ctrl+Alt+D")
	if err != nil {
		t.Fatalf("Ctrl+Alt+D should succeed after conflict released: %v", err)
	}
	t.Cleanup(func() { hk3.Close() })

	sendCombo(x11XK_Control_L, x11XK_Alt_L, 'd')
	waitPressed(t, hk3, true, "hk3 should fire after registration")
	waitPressed(t, hk3, false, "hk3 consumed")
}

func TestX11HotkeyRobustness(t *testing.T) {
	if os.Getenv("CT_TEST_X11") != "1" {
		t.Skip("run only on an isolated Xvfb display with CT_TEST_X11=1")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := platformRuntimeError(); err != nil {
		t.Fatal(err)
	}

	// Subtest 1: X error handler filtering and restoration (Item 1)
	t.Run("XErrorHandlerFilteringAndRestoration", func(t *testing.T) {
		x11TestInstallForwardHandler()
		x11TestSetPrevErrorHandler()

		// 1. Grab conflict (matching display, X_GrabKey 33, BadAccess 10):
		// Must record grab_error = BadAccess and NOT forward to previous handler.
		x11TestSetForwardedError(0)
		x11TestCallGrabErrorHandler(true, 33 /* X_GrabKey */, 10 /* BadAccess */)
		if x11TestGetGrabError() != 10 {
			t.Errorf("expected grab_error == BadAccess (10), got %d", x11TestGetGrabError())
		}
		if x11TestGetForwardedError() != 0 {
			t.Errorf("matching grab conflict error must not be forwarded, got %d", x11TestGetForwardedError())
		}

		// 2. Error from another display connection (e.g. GTK's or other client):
		// Must NOT record as grab_error and MUST forward to previous handler.
		x11TestSetForwardedError(0)
		x11TestCallGrabErrorHandler(false, 33, 10)
		if x11TestGetForwardedError() != 10 {
			t.Errorf("error from different display must forward to previous handler, got %d", x11TestGetForwardedError())
		}

		// 3. Different request code (e.g. 42 instead of X_GrabKey 33):
		// Must NOT record as grab_error and MUST forward to previous handler.
		x11TestSetForwardedError(0)
		x11TestCallGrabErrorHandler(true, 42, 10)
		if x11TestGetForwardedError() != 10 {
			t.Errorf("non-X_GrabKey error must forward to previous handler, got %d", x11TestGetForwardedError())
		}

		// 4. Different error code (e.g. BadValue 2 instead of BadAccess 10):
		// Must NOT record as grab_error and MUST forward to previous handler.
		x11TestSetForwardedError(0)
		x11TestCallGrabErrorHandler(true, 33, 2 /* BadValue */)
		if x11TestGetForwardedError() != 2 {
			t.Errorf("non-BadAccess error must forward to previous handler, got %d", x11TestGetForwardedError())
		}

		// 5. Verify handler restoration after real grab:
		x11TestInstallForwardHandler()
		x11TestSetForwardedError(0)
		hk, err := newHotkey("Ctrl+Alt+E")
		if err != nil {
			t.Fatalf("newHotkey failed: %v", err)
		}
		defer hk.Close()

		// Previous handler was restored: verify by triggering current process-wide handler
		x11TestTriggerCurrentErrorHandler(99)
		if x11TestGetForwardedError() != 99 {
			t.Errorf("previous error handler must be restored after grab, got %d", x11TestGetForwardedError())
		}
	})

	// Subtest 2: Failed re-registration keeps old hotkey working (Item 2)
	t.Run("FailedReRegistrationPreservesOldHotkey", func(t *testing.T) {
		kcE := x11TestKeysymToKeycode('e')
		kcD := x11TestKeysymToKeycode('d')
		kcK := x11TestKeysymToKeycode('k')
		mods := uint32(x11ControlMask | x11Mod1Mask) // Ctrl+Alt

		// Register slot 1 to Ctrl+Alt+E
		res := x11TestHotkeyGrab(1, mods, kcE)
		if res != 1 {
			t.Fatalf("grab slot 1 failed: %d", res)
		}
		t.Cleanup(func() { x11TestHotkeyUngrab(1) })

		// Register slot 2 to Ctrl+Alt+K
		res2 := x11TestHotkeyGrab(2, mods, kcK)
		if res2 != 1 {
			t.Fatalf("grab slot 2 failed: %d", res2)
		}
		t.Cleanup(func() { x11TestHotkeyUngrab(2) })

		// Verify slot 1 fires on Ctrl+Alt+E
		sendCombo(x11XK_Control_L, x11XK_Alt_L, 'e')
		x11TestHotkeyPump()
		if !x11TestHotkeyCheck(1) {
			t.Fatal("slot 1 did not fire for Ctrl+Alt+E")
		}

		// Case A: Duplicate re-registration attempt: slot 1 tries to grab slot 2's key (Ctrl+Alt+K)
		resDup := x11TestHotkeyGrab(1, mods, kcK)
		if resDup != -2 {
			t.Fatalf("expected duplicate re-registration to return -2, got %d", resDup)
		}

		// Slot 1 must still work!
		sendCombo(x11XK_Control_L, x11XK_Alt_L, 'e')
		x11TestHotkeyPump()
		if !x11TestHotkeyCheck(1) {
			t.Fatal("slot 1 (Ctrl+Alt+E) did not fire after duplicate re-registration attempt!")
		}

		// Case B: Conflict re-registration attempt
		// Enable conflict on Ctrl+Alt+D from another X connection
		x11TestConflictKey('d', mods, true)
		conflictActive := true
		t.Cleanup(func() {
			if conflictActive {
				x11TestConflictKey('d', mods, false)
				conflictActive = false
			}
		})

		// Try to re-register slot 1 to Ctrl+Alt+D: must fail (-3 BadAccess)
		resRe := x11TestHotkeyGrab(1, mods, kcD)
		if resRe != -3 {
			t.Fatalf("expected re-registration on conflicting key to return -3, got %d", resRe)
		}

		// Crucial verification: old registration (Ctrl+Alt+E) MUST STILL WORK!
		sendCombo(x11XK_Control_L, x11XK_Alt_L, 'e')
		x11TestHotkeyPump()
		if !x11TestHotkeyCheck(1) {
			t.Fatal("old hotkey (Ctrl+Alt+E) did not fire after failed re-registration!")
		}

		// Release conflict
		x11TestConflictKey('d', mods, false)
		conflictActive = false
	})

	// Subtest 3: Closing one hotkey drains queue and preserves pending events of other hotkeys (Item 4)
	t.Run("UngrabDrainsQueueAndPreservesOthers", func(t *testing.T) {
		hk1, err := newHotkey("Ctrl+Alt+E")
		if err != nil {
			t.Fatalf("newHotkey hk1: %v", err)
		}
		t.Cleanup(func() { hk1.Close() })

		hk2, err := newHotkey("Ctrl+Alt+K")
		if err != nil {
			t.Fatalf("newHotkey hk2: %v", err)
		}
		t.Cleanup(func() { hk2.Close() })

		// Inject keypress for hk1 into X queue
		x11TestKey(x11XK_Control_L, true)
		x11TestKey(x11XK_Alt_L, true)
		x11TestKey('e', true)
		x11TestKey('e', false)
		x11TestKey(x11XK_Alt_L, false)
		x11TestKey(x11XK_Control_L, false)

		// Inject keypress for hk2 into X queue without pumping Go messages
		x11TestKey(x11XK_Control_L, true)
		x11TestKey(x11XK_Alt_L, true)
		x11TestKey('k', true)
		x11TestKey('k', false)
		x11TestKey(x11XK_Alt_L, false)
		x11TestKey(x11XK_Control_L, false)

		// Close hk1 now: ct_hotkey_ungrab must pump the queued event for hk2
		// before clearing hk1, and discard hk1's queued event!
		if err := hk1.Close(); err != nil {
			t.Fatalf("hk1.Close(): %v", err)
		}

		// hk2 must have latched the event and fire!
		waitPressed(t, hk2, true, "hk2 event must be preserved when hk1 is closed")

		// Re-register hk1 on Ctrl+Alt+E: it must NOT fire from the old stale event!
		hk1New, err := newHotkey("Ctrl+Alt+E")
		if err != nil {
			t.Fatalf("newHotkey hk1New: %v", err)
		}
		t.Cleanup(func() { hk1New.Close() })
		waitPressed(t, hk1New, false, "hk1New must not fire from stale ungrabbed event")
	})

	// Subtest 4: Store exact lock-mask variants at grab time and ungrab them (Item 5)
	t.Run("StoreAndUngrabExactLockMaskVariants", func(t *testing.T) {
		kcE := x11TestKeysymToKeycode('e')
		mods := uint32(x11ControlMask | x11Mod1Mask)

		res := x11TestHotkeyGrab(1, mods, kcE)
		if res != 1 {
			t.Fatalf("grab slot 1 failed: %d", res)
		}
		t.Cleanup(func() { x11TestHotkeyUngrab(1) })

		nvar, vars := x11TestGetSlotVariants(1)
		if nvar <= 0 || len(vars) != nvar {
			t.Fatalf("expected stored variants > 0, got nvar=%d, len=%d", nvar, len(vars))
		}
		// Base variant 0 must be 0
		hasZero := false
		for _, v := range vars {
			if v == 0 {
				hasZero = true
				break
			}
		}
		if !hasZero {
			t.Errorf("stored variants must include base mask 0: %v", vars)
		}

		// Ungrab slot 1: must ungrab all stored variants and reset slot state
		x11TestHotkeyUngrab(1)
		nvarAfter, _ := x11TestGetSlotVariants(1)
		if nvarAfter != -1 {
			t.Errorf("slot 1 must be cleared after ungrab, got nvar=%d", nvarAfter)
		}
	})
}
