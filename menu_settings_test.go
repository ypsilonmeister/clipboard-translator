package main

import "testing"

// The app puts Settings first and enabled, even with no active hotkey.
// (The native popup keeping it enabled is platform's TestPopupFirstItemStaysEnabled.)
func TestMenuSettingsFirstWithNoActiveHotkeys(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.hotkey = noHotkey{}
	items := a.buildMenu()
	if len(items) == 0 || items[0].Label != "Settings..." || items[0].Disabled {
		t.Fatalf("first item = %+v; want an enabled Settings...", items)
	}
}
