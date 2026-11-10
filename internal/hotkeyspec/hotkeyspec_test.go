package hotkeyspec

import (
	"strings"
	"testing"
)

// The macOS parser (parseMacHotkey) also knows Cmd/Command/Option; loadConfig
// must not reject what the platform parser would accept, and must keep
// rejecting those names where they mean nothing.
func TestValidateHotkeyPerOS(t *testing.T) {
	cases := []struct {
		goos, hotkey string
		ok           bool
	}{
		{"darwin", "Ctrl+Alt+E", true},
		{"darwin", "Cmd+Shift+E", true},
		{"darwin", "command+option+t", true},
		{"darwin", "Control+Option+E", true},
		{"darwin", "Super+E", true},
		{"darwin", "Win+E", true},
		{"darwin", "Cmd+1", true},
		{"darwin", "Cmd+F5", true},
		{"darwin", "Cmd+Option", false},
		{"darwin", "Cmd+F13", false},
		{"darwin", "Cmd+!", false},
		{"windows", "Cmd+Shift+E", false},
		{"windows", "Ctrl+Option+E", false},
		{"windows", "Super+E", false},
		{"windows", "Win+E", true},
		{"windows", "Ctrl+F5", true},
		{"windows", "Win+9", true},
		{"linux", "Cmd+Shift+E", false},
		{"linux", "Option+E", false},
		{"linux", "Super+E", true},
		{"linux", "Super+F12", true},
		{"linux", "Ctrl+0", true},
	}
	for _, c := range cases {
		err := Validate(c.goos, c.hotkey)
		if c.ok && err != nil {
			t.Errorf("%s: %q must be accepted: %v", c.goos, c.hotkey, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: %q must be rejected", c.goos, c.hotkey)
		}
		if err != nil && !strings.Contains(err.Error(), "hotkey") {
			t.Errorf("%s: %q: the error should mention the hotkey, got %v", c.goos, c.hotkey, err)
		}
	}
}

func TestParse(t *testing.T) {
	h, err := Parse("ctrl + ALT+e", false)
	if err != nil || !h.Ctrl || !h.Alt || h.Shift || h.Win || h.Key != "E" {
		t.Fatalf("Parse = %+v, %v", h, err)
	}
	if h, err = Parse("Windows+Shift+q", false); err != nil || !h.Win || !h.Shift || h.Key != "Q" {
		t.Fatalf("Parse = %+v, %v", h, err)
	}
	if h, err = Parse("Ctrl+1", false); err != nil || !h.Ctrl || h.Alt || h.Shift || h.Win || h.Key != "1" {
		t.Fatalf("Parse = %+v, %v", h, err)
	}
	if h, err = Parse("ctrl+f5", false); err != nil || !h.Ctrl || h.Alt || h.Shift || h.Win || h.Key != "F5" {
		t.Fatalf("Parse = %+v, %v", h, err)
	}
	if h, err = Parse("Win+F12", false); err != nil || h.Ctrl || h.Alt || h.Shift || !h.Win || h.Key != "F12" {
		t.Fatalf("Parse = %+v, %v", h, err)
	}
}
