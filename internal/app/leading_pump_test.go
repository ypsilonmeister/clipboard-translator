package app

import (
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

type pumpTestHotkey struct{}

func (pumpTestHotkey) Pressed() (bool, error) { return false, nil }
func (pumpTestHotkey) Close() error           { return nil }

// A pump before pollHotkeys would swallow the press: PumpMessages drops
// WM_HOTKEY on Windows and returns the press on macOS. Only an app with no
// working hotkey (or on Wayland) may pump on its own.
func TestNeedsLeadingPump(t *testing.T) {
	var hk platform.Hotkey = pumpTestHotkey{}
	cases := []struct {
		name string
		a    *app
		want bool
	}{
		{"working hotkey", &app{hotkey: hk}, false},
		{"no hotkey at all", &app{hotkey: noHotkey{}}, true},
		{"no hotkey, extra works", &app{hotkey: noHotkey{}, extras: []extraHotkey{{hk: hk}}}, false},
		{"no hotkey, extra failed", &app{hotkey: noHotkey{}, extras: []extraHotkey{{why: "taken"}}}, true},
		{"working hotkey, extra failed", &app{hotkey: hk, extras: []extraHotkey{{why: "taken"}}}, false},
	}
	for _, c := range cases {
		if got := c.a.needsLeadingPump(); got != c.want {
			t.Errorf("%s: needsLeadingPump = %v, want %v", c.name, got, c.want)
		}
	}
}
