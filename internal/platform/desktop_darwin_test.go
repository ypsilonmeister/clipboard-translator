//go:build darwin && cgo

package platform

import "testing"

func TestMacHotkeyParsing(t *testing.T) {
	mods, vk, key, err := parseMacHotkey("Control + Option + E")
	if err != nil || mods != (1<<12|1<<11) || vk != -1 || key != 'e' {
		t.Fatalf("%d %d %d %v", mods, vk, key, err)
	}
	mods, vk, key, err = parseMacHotkey("Cmd+Shift+T")
	if err != nil || mods != (1<<8|1<<9) || vk != -1 || key != 't' {
		t.Fatalf("%d %d %d %v", mods, vk, key, err)
	}
	mods, vk, key, err = parseMacHotkey("Cmd+Shift+5")
	if err != nil || mods != (1<<8|1<<9) || vk != -1 || key != '5' {
		t.Fatalf("%d %d %d %v", mods, vk, key, err)
	}
	mods, vk, key, err = parseMacHotkey("Cmd+F1")
	if err != nil || mods != (1<<8) || vk != 0x7A || key != 0 {
		t.Fatalf("%d %d %d %v", mods, vk, key, err)
	}
	mods, vk, key, err = parseMacHotkey("Option+F12")
	if err != nil || mods != (1<<11) || vk != 0x6F || key != 0 {
		t.Fatalf("%d %d %d %v", mods, vk, key, err)
	}
	for _, bad := range []string{"", "E", "Cmd", "Cmd+F13", "Cmd+F0", "Cmd+!", "Cmd+E+T", "Alt++E"} {
		if _, _, _, err := parseMacHotkey(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
