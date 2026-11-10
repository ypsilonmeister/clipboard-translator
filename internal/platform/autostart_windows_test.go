//go:build windows

package platform

import (
	"testing"
	"unsafe"
)

// Uses a scratch key under HKCU\Software, never the real Run key.
func TestAutostartStore(t *testing.T) {
	const parent = `Software\ClipboardTranslatorTest`
	s := autostartStore{key: parent + `\Run`, name: "ClipboardTranslator"}
	t.Cleanup(func() {
		regDeleteKeyW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(u16(s.key))))
		regDeleteKeyW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(u16(parent))))
	})

	cmd := `"C:\Some Folder\ClipboardTranslator.exe"`
	if s.Enabled(cmd) {
		t.Fatal("must be disabled before Set")
	}
	if err := s.Set(cmd); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !s.Enabled(cmd) {
		t.Fatal("must be enabled after Set")
	}
	if s.Enabled(`"D:\Elsewhere\ClipboardTranslator.exe"`) {
		t.Fatal("a value pointing to another path must count as disabled")
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if s.Enabled(cmd) {
		t.Fatal("must be disabled after Clear")
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear of a missing value must not fail: %v", err)
	}
}
