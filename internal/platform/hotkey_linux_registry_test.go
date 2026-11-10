//go:build linux

package platform

import (
	"strings"
	"testing"
)

func TestParseX11Hotkey(t *testing.T) {
	mods, key, err := parseX11Hotkey(" Control + Alt + E ")
	if err != nil || mods != 12 || key != 'e' {
		t.Fatalf("%d %d %v", mods, key, err)
	}
	mods, key, err = parseX11Hotkey("Super+Shift+Y")
	if err != nil || mods != 65 || key != 'y' {
		t.Fatalf("%d %d %v", mods, key, err)
	}
	mods, key, err = parseX11Hotkey("Ctrl+5")
	if err != nil || mods != 4 || key != '5' {
		t.Fatalf("%d %d %v", mods, key, err)
	}
	mods, key, err = parseX11Hotkey("Alt+F1")
	if err != nil || mods != 8 || key != 0xffbe {
		t.Fatalf("%d %d %v", mods, key, err)
	}
	mods, key, err = parseX11Hotkey("Super+F12")
	if err != nil || mods != 64 || key != 0xffc9 {
		t.Fatalf("%d %d %v", mods, key, err)
	}
	for _, bad := range []string{"", "E", "Ctrl", "Ctrl+F13", "Ctrl+F0", "Ctrl+!", "Ctrl+E+T", "Ctrl++E"} {
		if _, _, err := parseX11Hotkey(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestNormalizeX11Modifiers(t *testing.T) {
	cases := []struct {
		raw  uint32
		want uint32
	}{
		{0, 0},
		{x11ControlMask, x11ControlMask},
		{x11Mod1Mask, x11Mod1Mask},
		{x11ShiftMask, x11ShiftMask},
		{x11Mod4Mask, x11Mod4Mask},
		{x11ControlMask | x11Mod1Mask, x11ControlMask | x11Mod1Mask},
		// LockMask (2), NumLock (16), ScrollLock (32), Mod5 (128) must be stripped:
		{x11ControlMask | x11Mod1Mask | 2, x11ControlMask | x11Mod1Mask},
		{x11ControlMask | 16 | 32 | 128, x11ControlMask},
		{x11ModMask | 2 | 16 | 32 | 128, x11ModMask},
	}
	for _, tc := range cases {
		if got := normalizeX11Modifiers(tc.raw); got != tc.want {
			t.Errorf("normalizeX11Modifiers(0x%x) = 0x%x; want 0x%x", tc.raw, got, tc.want)
		}
	}
}

func TestHotkeyRegistryLimit(t *testing.T) {
	r := newHotkeyRegistry(2)
	id1, err := r.Register("Ctrl+Alt+E", 26, 12)
	if err != nil || id1 != 1 {
		t.Fatalf("Register 1: got %d, %v; want 1, nil", id1, err)
	}
	id2, err := r.Register("Ctrl+Alt+K", 45, 12)
	if err != nil || id2 != 2 {
		t.Fatalf("Register 2: got %d, %v; want 2, nil", id2, err)
	}
	// 3rd hotkey exceeds limit
	id3, err := r.Register("Ctrl+Alt+D", 40, 12)
	if err == nil || !strings.Contains(err.Error(), "at most 2 hotkeys") {
		t.Fatalf("Register 3 over limit: got %d, %v; want limit error", id3, err)
	}
}

func TestHotkeyRegistryDuplicateDetection(t *testing.T) {
	r := newHotkeyRegistry(8)
	id, err := r.Register("Ctrl+Alt+E", 26, 12)
	if err != nil || id != 1 {
		t.Fatalf("Register: got %d, %v", id, err)
	}

	// Same keycode and same modifiers (even with LockMask set in raw): duplicate!
	_, err = r.Register("alt+control+e", 26, 12|2)
	if err == nil || !strings.Contains(err.Error(), "already registered (same key combination as Ctrl+Alt+E)") {
		t.Fatalf("Duplicate registration should fail with 'already registered (same key combination as Ctrl+Alt+E)', got: %v", err)
	}

	// Different keycode, same modifiers: ok
	id2, err := r.Register("Ctrl+Alt+K", 45, 12)
	if err != nil || id2 != 2 {
		t.Fatalf("Different keycode should succeed, got %d, %v", id2, err)
	}

	// Same keycode, different modifiers: ok
	id3, err := r.Register("Ctrl+Shift+E", 26, 5)
	if err != nil || id3 != 3 {
		t.Fatalf("Different modifiers should succeed, got %d, %v", id3, err)
	}

	if r.Count() != 3 {
		t.Fatalf("Count = %d; want 3", r.Count())
	}
}

func TestHotkeyRegistryDuplicateDigitsAndFunctionKeys(t *testing.T) {
	r := newHotkeyRegistry(8)

	// Digits: Ctrl+5
	id1, err := r.Register("Ctrl+5", 14, x11ControlMask)
	if err != nil || id1 != 1 {
		t.Fatalf("Register Ctrl+5: %v", err)
	}
	_, err = r.Register("ctrl+5", 14, x11ControlMask)
	if err == nil || !strings.Contains(err.Error(), "already registered (same key combination as Ctrl+5)") {
		t.Fatalf("Duplicate Ctrl+5 should fail with registered message, got: %v", err)
	}
	// Different digit: ok
	id2, err := r.Register("Ctrl+6", 15, x11ControlMask)
	if err != nil || id2 != 2 {
		t.Fatalf("Register Ctrl+6: %v", err)
	}

	// Function keys: Super+F12
	id3, err := r.Register("Super+F12", 96, x11Mod4Mask)
	if err != nil || id3 != 3 {
		t.Fatalf("Register Super+F12: %v", err)
	}
	_, err = r.Register("super+f12", 96, x11Mod4Mask)
	if err == nil || !strings.Contains(err.Error(), "already registered (same key combination as Super+F12)") {
		t.Fatalf("Duplicate Super+F12 should fail with registered message, got: %v", err)
	}
	// Different function key: ok
	id4, err := r.Register("Super+F11", 95, x11Mod4Mask)
	if err != nil || id4 != 4 {
		t.Fatalf("Register Super+F11: %v", err)
	}

	// Equivalent spellings: Ctrl+F12 vs ctrl+f12
	id5, err := r.Register("Ctrl+F12", 96, x11ControlMask)
	if err != nil || id5 != 5 {
		t.Fatalf("Register Ctrl+F12: %v", err)
	}
	_, err = r.Register("ctrl+f12", 96, x11ControlMask)
	if err == nil || !strings.Contains(err.Error(), "already registered (same key combination as Ctrl+F12)") {
		t.Fatalf("Duplicate ctrl+f12 vs Ctrl+F12 should fail, got: %v", err)
	}
}

func TestHotkeyRegistryUnregisterAndReassign(t *testing.T) {
	r := newHotkeyRegistry(8)
	id1, _ := r.Register("Ctrl+Alt+E", 26, 12)
	id2, _ := r.Register("Ctrl+Alt+K", 45, 12)
	id3, _ := r.Register("Ctrl+Alt+D", 40, 12)

	if id1 != 1 || id2 != 2 || id3 != 3 {
		t.Fatalf("IDs: %d, %d, %d", id1, id2, id3)
	}

	// Unregister the middle one (id2)
	r.Unregister(2)
	if r.Count() != 2 {
		t.Fatalf("Count after unregister = %d; want 2", r.Count())
	}

	// Now registering a new key should reuse lowest ID (2)
	idNew, err := r.Register("Ctrl+Alt+F", 41, 12)
	if err != nil || idNew != 2 {
		t.Fatalf("Reused ID: got %d, %v; want 2, nil", idNew, err)
	}

	// Unregister id1 ("Ctrl+Alt+E")
	r.Unregister(1)
	// Now re-registering "Ctrl+Alt+E" should succeed
	idRe, err := r.Register("Ctrl+Alt+E", 26, 12)
	if err != nil || idRe != 1 {
		t.Fatalf("Re-register after unregister: got %d, %v; want 1, nil", idRe, err)
	}

	// Unregister non-existent ID is a safe no-op
	r.Unregister(99)
	if r.Count() != 3 {
		t.Fatalf("Count after no-op unregister = %d; want 3", r.Count())
	}
}

func TestHotkeyRegistryLookupAndCount(t *testing.T) {
	r := newHotkeyRegistry(8)
	r.Register("Ctrl+Alt+E", 26, 12)
	reg, ok := r.Lookup(1)
	if !ok || reg.spec != "Ctrl+Alt+E" || reg.keycode != 26 || reg.mods != 12 {
		t.Fatalf("Lookup(1) = %+v, %v", reg, ok)
	}
	_, ok = r.Lookup(99)
	if ok {
		t.Fatal("Lookup(99) should return false")
	}

	r.Clear()
	if r.Count() != 0 {
		t.Fatalf("Count after Clear = %d; want 0", r.Count())
	}
}
