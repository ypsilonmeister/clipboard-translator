package main

import (
	"errors"
	"strings"
	"testing"
)

func TestStartupHotkeyConflictFallsBackToNoHotkey(t *testing.T) {
	a, tray, errs := newTestApp(t)
	a.newHotkey = func(spec string) (Hotkey, error) {
		if spec == "Ctrl+Alt+E" {
			return nil, errors.New("already registered")
		}
		return fakeHotkey{spec}, nil
	}

	a.registerStartupHotkey()
	if !a.hasNoHotkey() {
		t.Fatalf("a taken startup hotkey must leave the app on noHotkey{}, got %#v", a.hotkey)
	}
	if a.readyTip != noHotkeyTip {
		t.Fatalf("the tooltip must say no hotkey is active, got %q", a.readyTip)
	}
	if len(*errs) != 0 {
		t.Fatalf("no modal error is expected, got %v", *errs)
	}
	if pressed, err := a.hotkey.Pressed(); pressed || err != nil {
		t.Fatalf("noHotkey must be inert: %v %v", pressed, err)
	}
	// The menu must not claim the unregistered hotkey is active.
	for _, it := range a.buildMenu() {
		if it.Label == "Hotkey" {
			for _, s := range it.Sub {
				if s.Checked {
					t.Errorf("no hotkey is registered, but %q is checked", s.Label)
				}
			}
		}
	}

	// Trying the same (still taken) one again keeps the app alive.
	a.changeHotkey("Ctrl+Alt+E")
	if !a.hasNoHotkey() || !strings.Contains(tray.lastNote(), "No hotkey is active") {
		t.Fatalf("a failed retry must keep noHotkey and tell the user, got %#v / %q", a.hotkey, tray.lastNote())
	}
	if len(*errs) != 0 {
		t.Fatalf("a failed retry must not raise a modal error, got %v", *errs)
	}

	// Picking a free one recovers.
	a.changeHotkey("Ctrl+Shift+Y")
	if hk, ok := a.hotkey.(fakeHotkey); !ok || hk.spec != "Ctrl+Shift+Y" {
		t.Fatalf("the chosen hotkey must be registered, got %#v", a.hotkey)
	}
	if a.conf.Hotkey != "Ctrl+Shift+Y" || !strings.Contains(a.readyTip, "Ctrl+Shift+Y") {
		t.Fatalf("hotkey not applied: %q / %q", a.conf.Hotkey, a.readyTip)
	}
	if c, _ := loadConfig(a.dir); c.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("hotkey not saved: %q", c.Hotkey)
	}
}

func TestRetryingTheConfiguredHotkeyOnceFree(t *testing.T) {
	a, _, _ := newTestApp(t)
	taken := true
	a.newHotkey = func(spec string) (Hotkey, error) {
		if taken {
			return nil, errors.New("already registered")
		}
		return fakeHotkey{spec}, nil
	}
	a.registerStartupHotkey()
	taken = false // the other program let go
	a.changeHotkey(a.conf.Hotkey)
	if a.hasNoHotkey() {
		t.Fatal("choosing the configured hotkey again must retry the registration")
	}
}
