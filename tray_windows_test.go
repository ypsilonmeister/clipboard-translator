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
	tr.Notify("title", "message", false)
	pumpMessages()
	if tr.QuitRequested() {
		t.Fatal("QuitRequested must be false before the user picks Quit")
	}
	wt.quit.Store(true)
	if !tr.QuitRequested() {
		t.Fatal("QuitRequested should reflect the quit flag")
	}
}
