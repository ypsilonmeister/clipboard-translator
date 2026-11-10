//go:build windows

package platform

import (
	"testing"
	"unsafe"
)

var (
	getMenuItemCount = user32.NewProc("GetMenuItemCount")
	getMenuStringW   = user32.NewProc("GetMenuStringW")
	getMenuState     = user32.NewProc("GetMenuState")
	getSubMenu       = user32.NewProc("GetSubMenu")
)

const mfByPosition = 0x400

func menuText(menu uintptr, pos int) string {
	buf := make([]uint16, 256)
	n, _, _ := getMenuStringW.Call(menu, uintptr(pos), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), mfByPosition)
	return syscallUTF16(buf[:n])
}

func syscallUTF16(b []uint16) string {
	out := make([]rune, len(b))
	for i, c := range b {
		out[i] = rune(c)
	}
	return string(out)
}

func TestMenuLabelEscapesAmpersands(t *testing.T) {
	if got := menuLabel("A & B && C"); got != "A && B &&&& C" {
		t.Fatalf("menuLabel = %q", got)
	}
}

// Builds a real Win32 popup menu (without showing it) and reads it back.
func TestBuildPopupStructure(t *testing.T) {
	menu := buildPopup([]MenuItem{
		{ID: 101, Label: "Plain"},
		{ID: 102, Label: "Fish & Chips", Checked: true},
		{ID: 103, Label: "Off", Disabled: true},
		{Separator: true},
		{Label: "Sub", Sub: []MenuItem{{ID: 110, Label: "One", Checked: true}, {ID: 111, Label: "Two"}}},
	})
	if menu == 0 {
		t.Fatal("no menu")
	}
	defer destroyMenu.Call(menu)

	if n, _, _ := getMenuItemCount.Call(menu); n != 5 {
		t.Fatalf("item count = %d, want 5", n)
	}
	if got := menuText(menu, 0); got != "Plain" {
		t.Errorf("item 0 = %q", got)
	}
	// GetMenuString returns the stored text; "&&" is how Windows is told to show one literal "&".
	if got := menuText(menu, 1); got != "Fish && Chips" {
		t.Errorf("item 1 = %q (a literal & must be stored as &&)", got)
	}
	const mfCheckedState, mfDisabledState = 0x8, 0x3 // MF_CHECKED; MF_GRAYED|MF_DISABLED
	st1, _, _ := getMenuState.Call(menu, 1, mfByPosition)
	if st1&mfCheckedState == 0 {
		t.Errorf("item 1 must be checked, state=%#x", st1)
	}
	st2, _, _ := getMenuState.Call(menu, 2, mfByPosition)
	if st2&mfDisabledState == 0 {
		t.Errorf("item 2 must be disabled, state=%#x", st2)
	}
	sub, _, _ := getSubMenu.Call(menu, 4)
	if sub == 0 {
		t.Fatal("item 4 must be a submenu")
	}
	if n, _, _ := getMenuItemCount.Call(sub); n != 2 {
		t.Fatalf("submenu count = %d, want 2", n)
	}
	if got := menuText(sub, 0); got != "One" {
		t.Errorf("submenu item 0 = %q", got)
	}
}

// Commands picked from the tray queue up and come back in order, once each.
func TestTrayNextCommand(t *testing.T) {
	tr := &winTray{}
	tr.cmds = append(tr.cmds, 101, 105)
	for _, want := range []int{101, 105} {
		if id, ok := tr.NextCommand(); !ok || id != want {
			t.Fatalf("NextCommand = %d, %v; want %d", id, ok, want)
		}
	}
	if _, ok := tr.NextCommand(); ok {
		t.Fatal("the queue must be empty after both commands")
	}
}

// Read back the native popup without displaying it or interacting with the desktop.
func TestPopupSettingsFirstWithNoActiveHotkeys(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.hotkey = noHotkey{}
	menu := buildPopup(a.buildMenu())
	if menu == 0 {
		t.Fatal("no menu")
	}
	defer destroyMenu.Call(menu)
	if got := menuText(menu, 0); got != "Settings..." {
		t.Fatalf("first item=%q", got)
	}
	st, _, _ := getMenuState.Call(menu, 0, mfByPosition)
	if st == ^uintptr(0) || st&0x3 != 0 {
		t.Fatalf("Settings must be enabled: state=%#x", st)
	}
}
