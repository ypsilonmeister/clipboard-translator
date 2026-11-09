//go:build linux

package main

import (
	"errors"
	"fmt"
	"strings"
)

const (
	x11ShiftMask   = 1
	x11LockMask    = 2
	x11ControlMask = 4
	x11Mod1Mask    = 8  // Alt
	x11Mod4Mask    = 64 // Super / Win
	x11ModMask     = x11ShiftMask | x11ControlMask | x11Mod1Mask | x11Mod4Mask
)

func normalizeX11Modifiers(mods uint32) uint32 {
	return mods & x11ModMask
}

func parseX11Hotkey(spec string) (uint32, uint64, error) {
	var mods uint32
	var key uint64
	for _, part := range strings.Split(strings.ToUpper(strings.ReplaceAll(spec, " ", "")), "+") {
		switch part {
		case "CTRL", "CONTROL":
			mods |= x11ControlMask
		case "ALT":
			mods |= x11Mod1Mask
		case "SHIFT":
			mods |= x11ShiftMask
		case "WIN", "WINDOWS", "SUPER":
			mods |= x11Mod4Mask
		default:
			if key != 0 {
				return 0, 0, fmt.Errorf("unsupported hotkey %q: use modifiers and one key (A-Z, 0-9, or F1-F12)", spec)
			}
			if len(part) == 1 && part[0] >= 'A' && part[0] <= 'Z' {
				key = uint64(part[0] + ('a' - 'A'))
			} else if len(part) == 1 && part[0] >= '0' && part[0] <= '9' {
				key = uint64(part[0])
			} else if fn, ok := parseFunctionKey(part); ok {
				key = uint64(0xffbe + fn - 1)
			} else {
				return 0, 0, fmt.Errorf("unsupported hotkey %q: use modifiers and one key (A-Z, 0-9, or F1-F12)", spec)
			}
		}
	}
	if mods == 0 || key == 0 {
		return 0, 0, errors.New("hotkey must include a modifier and key (A-Z, 0-9, or F1-F12)")
	}
	return mods, key, nil
}

type hotkeyRegistration struct {
	id      int
	spec    string
	keycode uint32
	mods    uint32 // normalized
}

type hotkeyRegistry struct {
	limit   int
	entries []hotkeyRegistration
}

func newHotkeyRegistry(limit int) *hotkeyRegistry {
	return &hotkeyRegistry{limit: limit}
}

func (r *hotkeyRegistry) Register(spec string, keycode uint32, rawMods uint32) (int, error) {
	if keycode == 0 {
		return 0, errors.New("invalid keycode: 0")
	}
	normMods := normalizeX11Modifiers(rawMods)
	for _, reg := range r.entries {
		if reg.keycode == keycode && reg.mods == normMods {
			return 0, fmt.Errorf("hotkey %s is already registered (keycode %d, modifiers 0x%x)", spec, keycode, normMods)
		}
	}
	if len(r.entries) >= r.limit {
		return 0, fmt.Errorf("at most %d hotkeys can be registered", r.limit)
	}
	id := 1
	for ; id <= r.limit; id++ {
		used := false
		for _, reg := range r.entries {
			if reg.id == id {
				used = true
				break
			}
		}
		if !used {
			break
		}
	}
	r.entries = append(r.entries, hotkeyRegistration{
		id:      id,
		spec:    spec,
		keycode: keycode,
		mods:    normMods,
	})
	return id, nil
}

func (r *hotkeyRegistry) Unregister(id int) {
	for i, reg := range r.entries {
		if reg.id == id {
			r.entries = append(r.entries[:i], r.entries[i+1:]...)
			return
		}
	}
}

func (r *hotkeyRegistry) Lookup(id int) (hotkeyRegistration, bool) {
	for _, reg := range r.entries {
		if reg.id == id {
			return reg, true
		}
	}
	return hotkeyRegistration{}, false
}

func (r *hotkeyRegistry) Count() int {
	return len(r.entries)
}

func (r *hotkeyRegistry) Clear() {
	r.entries = nil
}
