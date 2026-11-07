//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa -framework Carbon -framework ApplicationServices
#include <stdlib.h>
#include "native_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unsafe"
)

func platformRuntimeError() error {
	if C.ct_mac_init() == 0 {
		return errors.New("AppKit must be initialized on the main OS thread in a macOS desktop session")
	}
	return nil
}
func withCString(s string, fn func(*C.char)) {
	p := C.CString(s)
	defer C.free(unsafe.Pointer(p))
	fn(p)
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

type Hotkey interface {
	Pressed() (bool, error)
	Close() error
}
type macHotkey struct{}

func parseMacHotkey(spec string) (uint32, byte, error) {
	var mods uint32
	var key byte
	for _, part := range strings.Split(strings.ToUpper(strings.ReplaceAll(spec, " ", "")), "+") {
		switch part {
		case "CMD", "COMMAND", "WIN", "SUPER":
			mods |= 1 << 8
		case "SHIFT":
			mods |= 1 << 9
		case "ALT", "OPTION":
			mods |= 1 << 11
		case "CTRL", "CONTROL":
			mods |= 1 << 12
		default:
			if len(part) != 1 || part[0] < 'A' || part[0] > 'Z' || key != 0 {
				return 0, 0, fmt.Errorf("unsupported hotkey %q: use modifiers and one A-Z key", spec)
			}
			key = part[0] + ('a' - 'A')
		}
	}
	if mods == 0 || key == 0 {
		return 0, 0, errors.New("hotkey must include a modifier and letter A-Z")
	}
	return mods, key, nil
}
func newHotkey(spec string) (Hotkey, error) {
	mods, key, err := parseMacHotkey(spec)
	if err != nil {
		return nil, err
	}
	if C.ct_mac_grab(C.uint(mods), C.char(key)) == 0 {
		return nil, fmt.Errorf("could not register %s (already used, or unsupported keyboard layout?)", spec)
	}
	return &macHotkey{}, nil
}
func (*macHotkey) Pressed() (bool, error) { return pumpMessages(), nil }
func (*macHotkey) Close() error           { C.ct_mac_ungrab(); return nil }
func foregroundWindow() uintptr           { return uintptr(C.ct_mac_focus()) }
func clipboardSequence() uint32           { return uint32(C.ct_mac_sequence()) }
func accessibilityGranted() bool          { return C.ct_mac_accessible() != 0 }
func waitModifiersReleased(max time.Duration) bool {
	deadline := time.Now().Add(max)
	for C.ct_mac_modifiers() != 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(15 * time.Millisecond)
	}
	return true
}
func sendPaste() error {
	if C.ct_mac_send(1) == 0 {
		return errors.New("Cmd+V could not be sent; grant Accessibility access to Clipboard Translator in System Settings")
	}
	return nil
}
func captureSelectedText(cb Clipboard) (string, uint32, error) {
	old, err := cb.Read()
	if err != nil {
		return "", 0, err
	}
	before := clipboardSequence()
	if !accessibilityGranted() || !waitModifiersReleased(900*time.Millisecond) {
		return old, before, nil
	}
	if C.ct_mac_send(0) == 0 {
		return old, before, nil
	}
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		if seq := clipboardSequence(); seq != before {
			text, err := cb.Read()
			return text, seq, err
		}
		time.Sleep(15 * time.Millisecond)
	}
	return old, before, nil
}

type macTray struct {
	build    func() []MenuItem
	logPath  string
	commands []int
	quit     bool
}

var activeMacTray *macTray

func newTray(logPath string) Tray {
	t := &macTray{logPath: logPath}
	C.ct_mac_tray_create()
	activeMacTray = t
	if !accessibilityGranted() {
		log.Print("Accessibility access is not granted; translating existing clipboard text remains available. Open Accessibility Settings from the menu to enable selection capture and auto-paste.")
	}
	return t
}
func (*macTray) SetStatus(text string) { withCString(text, func(p *C.char) { C.ct_mac_status(p) }) }
func (t *macTray) Notify(title, message string, isError bool) {
	// No notification permission prompts or notification-service dependency.
	log.Printf("%s: %s", title, message)
	t.SetStatus(title + ": " + message)
}
func (t *macTray) QuitRequested() bool             { return t.quit }
func (t *macTray) SetMenu(build func() []MenuItem) { t.build = build }
func (t *macTray) NextCommand() (int, bool) {
	if len(t.commands) == 0 {
		return 0, false
	}
	id := t.commands[0]
	t.commands = t.commands[1:]
	return id, true
}
func (t *macTray) Close() { C.ct_mac_tray_close(); activeMacTray = nil }
func macMenu(items []MenuItem) unsafe.Pointer {
	menu := C.ct_mac_menu_new()
	for _, it := range items {
		var sub unsafe.Pointer
		if len(it.Sub) > 0 {
			sub = macMenu(it.Sub)
		}
		withCString(it.Label, func(label *C.char) {
			C.ct_mac_menu_add(menu, label, C.int(it.ID), C.int(boolInt(it.Checked)), C.int(boolInt(it.Disabled)), C.int(boolInt(it.Separator)), sub)
		})
	}
	return menu
}
func pumpMessages() bool {
	C.ct_mac_pump()
	if t := activeMacTray; t != nil {
		if C.ct_mac_wants_menu() != 0 {
			var items []MenuItem
			if t.build != nil {
				items = t.build()
			}
			items = append(items, MenuItem{Separator: true},
				MenuItem{ID: 3, Label: "Start at login", Checked: macAutostartEnabled()},
				MenuItem{ID: 4, Label: "Open Accessibility Settings..."},
				MenuItem{Label: "Accessibility access granted", Checked: accessibilityGranted(), Disabled: true},
				MenuItem{ID: 1, Label: "Open log"}, MenuItem{ID: 2, Label: "Quit"})
			C.ct_mac_menu_show(macMenu(items))
		}
		for id := int(C.ct_mac_command()); id != 0; id = int(C.ct_mac_command()) {
			switch id {
			case 1:
				openTarget(t.logPath)
			case 2:
				t.quit = true
			case 3:
				if err := toggleMacAutostart(); err != nil {
					t.Notify("Startup setting failed", err.Error(), true)
				}
			case 4:
				openTarget("x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility")
			default:
				t.commands = append(t.commands, id)
			}
		}
	}
	return C.ct_mac_hotkey() != 0
}
func dialog(msg string, question, isError bool) bool {
	result := false
	withCString(msg, func(p *C.char) { result = C.ct_mac_dialog(p, C.int(boolInt(question)), C.int(boolInt(isError))) != 0 })
	return result
}
func showError(msg string)     { dialog(msg, false, true) }
func showInfo(msg string)      { dialog(msg, false, false) }
func askYesNo(msg string) bool { return dialog(msg, true, false) }
func openTarget(target string) { withCString(target, func(p *C.char) { C.ct_mac_open(p) }) }

func beginSetupProgress(path string) { withCString(path, func(p *C.char) { C.ct_mac_setup_begin(p) }) }
func setupCancelled() bool           { return C.ct_mac_setup_cancelled() != 0 }
func endSetupProgress()              { C.ct_mac_setup_end() }
