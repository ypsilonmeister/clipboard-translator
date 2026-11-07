//go:build windows

package main

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestNotifyIconDataSize(t *testing.T) {
	// sizeof(NOTIFYICONDATAW) on 64-bit Windows.
	if got := unsafe.Sizeof(notifyIconData{}); got != 976 {
		t.Fatalf("notifyIconData size = %d, want 976", got)
	}
}

func TestCopyUTF16Truncates(t *testing.T) {
	var dst [4]uint16
	copyUTF16(dst[:], "abcdef")
	if dst[0] != 'a' || dst[2] != 'c' || dst[3] != 0 {
		t.Fatalf("got %v, want a b c NUL", dst)
	}
}

// pumpMessages must report only our own WM_HOTKEY, consume it exactly once, and
// leave other thread messages harmless.
func TestPumpMessagesHotkey(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	postThreadMessageW := user32.NewProc("PostThreadMessageW")
	getCurrentThreadID := kernel32.NewProc("GetCurrentThreadId")
	tid, _, _ := getCurrentThreadID.Call()
	post := func(msg, wparam uintptr) {
		if ok, _, err := postThreadMessageW.Call(tid, msg, wparam, 0); ok == 0 {
			t.Fatalf("PostThreadMessage: %v", err)
		}
	}

	pumpMessages() // start from an empty queue
	if pumpMessages() {
		t.Fatal("empty queue must not report a hotkey")
	}
	post(wmHotkey, hotkeyID+maxWindowsHotkeys) // not one of our ids
	post(wmApp+5, 0)                           // unrelated thread message
	if pumpMessages() {
		t.Fatal("foreign hotkey id / unrelated message must not report a hotkey")
	}
	post(wmApp+5, 0)
	post(wmHotkey, hotkeyID)
	if !pumpMessages() {
		t.Fatal("WM_HOTKEY with our id must be reported")
	}
	if pumpMessages() {
		t.Fatal("hotkey must be consumed exactly once")
	}
}

// Several registered hotkeys share one queue: each Pressed must see only its
// own press, even though the first call drains the queue for all of them.
func TestWindowsHotkeysEachSeeTheirOwnPress(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	postThreadMessageW := user32.NewProc("PostThreadMessageW")
	getCurrentThreadID := kernel32.NewProc("GetCurrentThreadId")
	tid, _, _ := getCurrentThreadID.Call()
	post := func(id uintptr) {
		if ok, _, err := postThreadMessageW.Call(tid, wmHotkey, id, 0); ok == 0 {
			t.Fatalf("PostThreadMessage: %v", err)
		}
	}
	pumpMessages()
	// Unlikely combinations, to avoid clashing with other programs.
	a, errA := newHotkey("Ctrl+Alt+Shift+F")
	b, errB := newHotkey("Ctrl+Alt+Shift+G")
	if errA != nil || errB != nil {
		t.Skipf("could not register test hotkeys: %v / %v", errA, errB)
	}
	ha, hb := a.(*windowsHotkey), b.(*windowsHotkey)
	if ha.id == hb.id {
		t.Fatalf("both hotkeys got id %#x", ha.id)
	}
	post(hb.id)
	if p, _ := ha.Pressed(); p {
		t.Fatal("hotkey A must not see B's press")
	}
	if p, _ := hb.Pressed(); !p {
		t.Fatal("hotkey B lost its press while A drained the queue")
	}
	if p, _ := hb.Pressed(); p {
		t.Fatal("a press must be taken once")
	}
	// A press seen by a plain pumpMessages (model loading) is dropped.
	post(ha.id)
	pumpMessages()
	if p, _ := ha.Pressed(); p {
		t.Fatal("a press while only the tray was serviced must be dropped")
	}
	// Closing frees the id and is safe to repeat.
	id := ha.id
	ha.Close()
	ha.Close()
	c, err := newHotkey("Ctrl+Alt+Shift+H")
	if err != nil {
		t.Skipf("could not register test hotkey: %v", err)
	}
	if c.(*windowsHotkey).id != id {
		t.Errorf("a freed id must be reused, got %#x want %#x", c.(*windowsHotkey).id, id)
	}
	c.Close()
	hb.Close()
}

// The icon embedded via resource_windows_amd64.syso must be found; otherwise the
// tray would silently fall back to the generic application icon.
func TestAppIconIsEmbedded(t *testing.T) {
	hInst, _, _ := getModuleHandleW.Call(0)
	stock, _, _ := loadIconW.Call(0, idiApplication)
	h := loadAppIcon(hInst)
	if h == 0 {
		t.Fatal("no icon could be loaded")
	}
	if h == stock {
		t.Fatal("got the stock icon: the embedded icon resource is missing (run go run ./tools/mksyso)")
	}
}

// The embedded manifest must make the process per-monitor-v2 DPI aware;
// otherwise Windows bitmap-stretches the tray icon and menu on scaled displays.
func TestProcessIsPerMonitorDPIAware(t *testing.T) {
	getCtx := user32.NewProc("GetThreadDpiAwarenessContext")
	equal := user32.NewProc("AreDpiAwarenessContextsEqual")
	if getCtx.Find() != nil || equal.Find() != nil {
		t.Skip("DPI awareness context APIs need Windows 10 1607 or later")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx, _, _ := getCtx.Call()
	const perMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = (HANDLE)-4
	if ok, _, _ := equal.Call(ctx, perMonitorV2); ok == 0 {
		t.Fatalf("DPI awareness context %#x is not per-monitor v2: manifest missing (run go run ./tools/mksyso)", ctx)
	}
}

// Creates a real tray icon in the interactive session and removes it again.
func TestTrayLifecycle(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tr := newTray(t.TempDir() + `\translator.log`)
	wt, ok := tr.(*winTray)
	if !ok {
		t.Skip("tray unavailable in this session (no interactive desktop?)")
	}
	defer tr.Close()
	if wt.hwnd == 0 {
		t.Fatal("tray window not created")
	}
	tr.SetStatus("test status")
	pumpMessages()
	if tr.QuitRequested() {
		t.Fatal("QuitRequested must be false before the user picks Quit")
	}
	wt.quit.Store(true)
	if !tr.QuitRequested() {
		t.Fatal("QuitRequested should reflect the quit flag")
	}
}
