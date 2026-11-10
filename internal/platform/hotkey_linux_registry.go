//go:build linux

package platform

import (
	"errors"
	"fmt"

	"github.com/ypsilonmeister/clipboard-translator/internal/hotkeyspec"
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
	parsed, err := hotkeyspec.ParseNames(spec, true, false)
	if err != nil {
		return 0, 0, err
	}
	var mods uint32
	if parsed.Ctrl {
		mods |= x11ControlMask
	}
	if parsed.Alt {
		mods |= x11Mod1Mask
	}
	if parsed.Shift {
		mods |= x11ShiftMask
	}
	if parsed.Win {
		mods |= x11Mod4Mask
	}
	var key uint64
	if len(parsed.Key) == 1 && parsed.Key[0] >= 'A' && parsed.Key[0] <= 'Z' {
		key = uint64(parsed.Key[0] + ('a' - 'A'))
	} else if len(parsed.Key) == 1 && parsed.Key[0] >= '0' && parsed.Key[0] <= '9' {
		key = uint64(parsed.Key[0])
	} else if fn, ok := hotkeyspec.ParseFunctionKey(parsed.Key); ok {
		key = uint64(0xffbe + fn - 1)
	} else {
		return 0, 0, fmt.Errorf("unsupported hotkey %q: use Ctrl+Alt+E (letters A-Z, digits 0-9, or F1-F12)", spec)
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
			return 0, fmt.Errorf("hotkey %s is already registered (same key combination as %s)", spec, reg.spec)
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
