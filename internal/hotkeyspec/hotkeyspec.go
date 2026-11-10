// Package hotkeyspec is the hotkey syntax ("Ctrl+Alt+E"): one parser shared by
// the config check and the per-OS hotkey code, so they cannot drift apart. It
// has no OS dependency.
package hotkeyspec

import (
	"fmt"
	"strings"
)

// Spec is a parsed hotkey such as "Ctrl+Alt+E".
type Spec struct {
	Ctrl, Alt, Shift, Win bool
	Key                   string // upper-case "A".."Z", "0".."9", or "F1".."F12"
}

// ParseFunctionKey parses "F1" through "F12" (case-insensitive) and returns
// the 1-based number (1..12), or false if s is not a function key.
func ParseFunctionKey(s string) (int, bool) {
	if len(s) < 2 || len(s) > 3 || (s[0] != 'F' && s[0] != 'f') {
		return 0, false
	}
	switch strings.ToUpper(s) {
	case "F1":
		return 1, true
	case "F2":
		return 2, true
	case "F3":
		return 3, true
	case "F4":
		return 4, true
	case "F5":
		return 5, true
	case "F6":
		return 6, true
	case "F7":
		return 7, true
	case "F8":
		return 8, true
	case "F9":
		return 9, true
	case "F10":
		return 10, true
	case "F11":
		return 11, true
	case "F12":
		return 12, true
	}
	return 0, false
}

// Parse is the one place that knows the hotkey syntax, so loadConfig
// can reject a bad config.json before the model is loaded and the per-platform
// parsers (RegisterHotKey, X11) cannot drift from it. allowSuper accepts
// "Super" as another name for the Windows key (X11 and macOS). allowMac accepts
// the macOS names Cmd/Command (the same key as Win here) and Option (Alt).
func Parse(s string, allowSuper bool) (Spec, error) {
	return ParseNames(s, allowSuper, false)
}

func ParseNames(s string, allowSuper, allowMac bool) (Spec, error) {
	var h Spec
	for _, p := range strings.Split(strings.ToUpper(strings.ReplaceAll(s, " ", "")), "+") {
		switch p {
		case "CTRL", "CONTROL":
			h.Ctrl = true
		case "ALT":
			h.Alt = true
		case "SHIFT":
			h.Shift = true
		case "WIN", "WINDOWS":
			h.Win = true
		case "CMD", "COMMAND":
			if !allowMac {
				return Spec{}, fmt.Errorf("unsupported hotkey %q: %s is a macOS name; use Ctrl+Alt+E", s, p)
			}
			h.Win = true
		case "OPTION":
			if !allowMac {
				return Spec{}, fmt.Errorf("unsupported hotkey %q: Option is a macOS name; use Alt", s)
			}
			h.Alt = true
		case "SUPER":
			if !allowSuper {
				return Spec{}, fmt.Errorf("unsupported hotkey %q: use Ctrl+Alt+E (letters A-Z, digits 0-9, or F1-F12)", s)
			}
			h.Win = true
		default:
			if h.Key != "" {
				return Spec{}, fmt.Errorf("unsupported hotkey %q: use Ctrl+Alt+E (letters A-Z, digits 0-9, or F1-F12)", s)
			}
			if len(p) == 1 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= '0' && p[0] <= '9')) {
				h.Key = p
			} else if _, ok := ParseFunctionKey(p); ok {
				h.Key = p
			} else {
				return Spec{}, fmt.Errorf("unsupported hotkey %q: use Ctrl+Alt+E (letters A-Z, digits 0-9, or F1-F12)", s)
			}
		}
	}
	if !(h.Ctrl || h.Alt || h.Shift || h.Win) || h.Key == "" {
		return Spec{}, fmt.Errorf("unsupported hotkey %q: it must include a modifier (Ctrl, Alt, Shift or Win) and one key (A-Z, 0-9, or F1-F12), like Ctrl+Alt+E", s)
	}
	return h, nil
}

// Validate checks a hotkey for the given GOOS, so tests can cover all three
// parsers (RegisterHotKey, X11, Carbon) from any machine.
func Validate(goos, s string) error {
	_, err := ParseNames(s, goos != "windows", goos == "darwin")
	return err
}
