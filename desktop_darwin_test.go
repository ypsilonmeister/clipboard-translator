//go:build darwin && cgo

package main

import "testing"

func TestMacHotkeyParsing(t *testing.T) {
	mods, key, err := parseMacHotkey("Control + Option + E")
	if err != nil || mods != (1<<12|1<<11) || key != 'e' {
		t.Fatalf("%d %d %v", mods, key, err)
	}
	mods, key, err = parseMacHotkey("Cmd+Shift+T")
	if err != nil || mods != (1<<8|1<<9) || key != 't' {
		t.Fatalf("%d %d %v", mods, key, err)
	}
	for _, bad := range []string{"", "E", "Cmd", "Cmd+F1", "Cmd+E+T", "Alt++E"} {
		if _, _, err := parseMacHotkey(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
