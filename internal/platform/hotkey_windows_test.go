//go:build windows

package platform

import "testing"

func TestWindowsVK(t *testing.T) {
	// A-Z: 0x41..0x5A
	for c := byte('A'); c <= 'Z'; c++ {
		vk, err := windowsVK(string(c))
		if err != nil || vk != uintptr(c) {
			t.Errorf("windowsVK(%q) = 0x%X, %v; want 0x%X", string(c), vk, err, c)
		}
	}
	// 0-9: 0x30..0x39
	for c := byte('0'); c <= '9'; c++ {
		vk, err := windowsVK(string(c))
		if err != nil || vk != uintptr(c) {
			t.Errorf("windowsVK(%q) = 0x%X, %v; want 0x%X", string(c), vk, err, c)
		}
	}
	// F1-F12: 0x70..0x7B
	fkeys := []struct {
		name string
		want uintptr
	}{
		{"F1", 0x70},
		{"F2", 0x71},
		{"F3", 0x72},
		{"F4", 0x73},
		{"F5", 0x74},
		{"F6", 0x75},
		{"F7", 0x76},
		{"F8", 0x77},
		{"F9", 0x78},
		{"F10", 0x79},
		{"F11", 0x7A},
		{"F12", 0x7B},
	}
	for _, fk := range fkeys {
		vk, err := windowsVK(fk.name)
		if err != nil || vk != fk.want {
			t.Errorf("windowsVK(%q) = 0x%X, %v; want 0x%X", fk.name, vk, err, fk.want)
		}
	}
	// Invalid keys
	bad := []string{"", "F0", "F13", "!", "AB", "Esc", "Enter", "Space"}
	for _, b := range bad {
		if vk, err := windowsVK(b); err == nil {
			t.Errorf("windowsVK(%q) = 0x%X, want error", b, vk)
		}
	}
}

func TestParseHotkeyWindows(t *testing.T) {
	mods, vk, err := parseHotkey("Ctrl+Alt+E")
	if err != nil || mods != (modControl|modAlt|modNoRepeat) || vk != 0x45 {
		t.Fatalf("Ctrl+Alt+E: mods=0x%X vk=0x%X err=%v", mods, vk, err)
	}
	mods, vk, err = parseHotkey("ctrl+7")
	if err != nil || mods != (modControl|modNoRepeat) || vk != 0x37 {
		t.Fatalf("ctrl+7: mods=0x%X vk=0x%X err=%v", mods, vk, err)
	}
	mods, vk, err = parseHotkey("Alt+F5")
	if err != nil || mods != (modAlt|modNoRepeat) || vk != 0x74 {
		t.Fatalf("Alt+F5: mods=0x%X vk=0x%X err=%v", mods, vk, err)
	}
	mods, vk, err = parseHotkey("Win+Shift+F12")
	if err != nil || mods != (modWin|modShift|modNoRepeat) || vk != 0x7B {
		t.Fatalf("Win+Shift+F12: mods=0x%X vk=0x%X err=%v", mods, vk, err)
	}
	for _, bad := range []string{"", "1", "F5", "Ctrl+F0", "Ctrl+F13", "Ctrl+!", "Ctrl+1+2"} {
		if _, _, err := parseHotkey(bad); err == nil {
			t.Errorf("parseHotkey(%q) want error, got nil", bad)
		}
	}
}
