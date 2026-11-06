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
	post(wmHotkey, hotkeyID+1) // some other hotkey id
	post(wmApp+5, 0)           // unrelated thread message
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
