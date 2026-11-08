//go:build linux && cgo

package main

import "testing"

func TestX11EnvironmentValidation(t *testing.T) {
	for _, tc := range []struct {
		display, session, wayland string
		ok                        bool
	}{
		{":1", "x11", "", true}, {":1", "", "", true},
		{"", "x11", "", false}, {":1", "wayland", "", false}, {":1", "x11", "wayland-0", false},
	} {
		if err := x11EnvironmentError(tc.display, tc.session, tc.wayland); (err == nil) != tc.ok {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}

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
