//go:build linux && cgo

package main

/*
#cgo pkg-config: gtk+-3.0 x11 xtst
#cgo CFLAGS: -Wno-deprecated-declarations
#include <stdlib.h>
#include "native_linux.h"
*/
import "C"

import (
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

//go:embed assets/icon.png
var linuxIcon []byte

func x11EnvironmentError(display, session, wayland string) error {
	if strings.EqualFold(session, "wayland") || wayland != "" {
		return errors.New("Wayland is not supported. Log in to an X11/Xorg session to use Clipboard Translator")
	}
	if display == "" {
		return errors.New("an X11 desktop is required (DISPLAY is not set)")
	}
	return nil
}

func platformRuntimeError() error {
	if err := x11EnvironmentError(os.Getenv("DISPLAY"), os.Getenv("XDG_SESSION_TYPE"), os.Getenv("WAYLAND_DISPLAY")); err != nil {
		return err
	}
	if C.ct_init() == 0 {
		return errors.New("could not initialize GTK 3 on the X11 display")
	}
	return nil
}

func withCString(s string, fn func(*C.char)) {
	p := C.CString(s)
	defer C.free(unsafe.Pointer(p))
	fn(p)
}

type Hotkey interface {
	Pressed() (bool, error)
	Close() error
}
type linuxHotkey struct{}

func newHotkey(spec string) (Hotkey, error) {
	mods, key, err := parseX11Hotkey(spec)
	if err != nil {
		return nil, err
	}
	if C.ct_grab(C.uint(mods), C.ulong(key)) == 0 {
		return nil, fmt.Errorf("could not grab %s on X11 (already used by another app?)", spec)
	}
	return &linuxHotkey{}, nil
}
func (*linuxHotkey) Pressed() (bool, error) { return pumpMessages(), nil }
func (*linuxHotkey) Close() error           { C.ct_ungrab(); return nil }

func foregroundWindow() uintptr { return uintptr(C.ct_focus()) }
func waitModifiersReleased(max time.Duration) bool {
	deadline := time.Now().Add(max)
	for C.ct_modifiers() != 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(15 * time.Millisecond)
	}
	return true
}
func sendPaste() error {
	if C.ct_paste() == 0 {
		return errors.New("XTest keyboard injection is unavailable")
	}
	return nil
}

// PRIMARY does not change CLIPBOARD. Stage the selected source on CLIPBOARD so
// the normal compare-before-replace path can protect later user copies.
var linuxSelectionSequence uint32

func clipboardSequence() uint32 { return linuxSelectionSequence }

func primarySelectedIn(win uintptr) bool {
	return C.ct_primary_selected_in(C.ulong(win)) != 0
}

func captureSelectedText(cb Clipboard, win uintptr) (string, bool, error) {
	text, fresh, err := captureWithPrimary(cb, win, primaryOps{
		selectedIn:  primarySelectedIn,
		readPrimary: func() string { return readX11Text(true) },
	})
	if err == nil && fresh {
		linuxSelectionSequence++
	}
	return text, fresh, err
}
func readX11Text(primary bool) string {
	p := C.ct_read(C.int(boolInt(primary)))
	if p == nil {
		return ""
	}
	defer C.ct_free(unsafe.Pointer(p))
	return C.GoString(p)
}

type linuxTray struct {
	build             func() []MenuItem
	logPath, iconPath string
	quit              bool
	commands          []int
}

var activeLinuxTray *linuxTray

func newTray(logPath string) Tray {
	t := &linuxTray{logPath: logPath}
	f, err := os.CreateTemp("", "clipboard-translator-icon-*.png")
	if err == nil {
		t.iconPath = f.Name()
		_, err = f.Write(linuxIcon)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		log.Printf("tray icon: %v", err)
	}
	withCString(t.iconPath, func(p *C.char) { C.ct_tray_create(p) })
	activeLinuxTray = t
	return t
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func (*linuxTray) SetStatus(text string) { withCString(text, func(p *C.char) { C.ct_status(p) }) }
func (*linuxTray) Notify(title, message string, isError bool) {
	log.Printf("%s: %s", title, message)
	args := []string{"--app-name=Clipboard Translator"}
	if isError {
		args = append(args, "--urgency=critical")
	}
	args = append(args, "--", title, message)
	cmd := exec.Command("notify-send", args...)
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}
func (t *linuxTray) QuitRequested() bool             { return t.quit }
func (t *linuxTray) SetMenu(build func() []MenuItem) { t.build = build }
func (t *linuxTray) Close() {
	C.ct_tray_close()
	activeLinuxTray = nil
	if t.iconPath != "" {
		_ = os.Remove(t.iconPath)
	}
}
func (t *linuxTray) NextCommand() (int, bool) {
	if len(t.commands) == 0 {
		return 0, false
	}
	id := t.commands[0]
	t.commands = t.commands[1:]
	return id, true
}
func linuxMenu(items []MenuItem) unsafe.Pointer {
	menu := C.ct_menu_new()
	for _, it := range items {
		var sub unsafe.Pointer
		if len(it.Sub) > 0 {
			sub = linuxMenu(it.Sub)
		}
		withCString(it.Label, func(label *C.char) {
			C.ct_menu_add(menu, label, C.int(it.ID), C.int(boolInt(it.Checked)), C.int(boolInt(it.Disabled)), C.int(boolInt(it.Separator)), sub)
		})
	}
	return menu
}
func pumpMessages() bool {
	C.ct_pump()
	if t := activeLinuxTray; t != nil {
		for id := int(C.ct_command()); id != 0; id = int(C.ct_command()) {
			switch id {
			case 1:
				openTarget(t.logPath)
			case 2:
				t.quit = true
			case 3:
				if err := toggleLinuxAutostart(); err != nil {
					t.Notify("Startup setting failed", err.Error(), true)
				}
			default:
				t.commands = append(t.commands, id)
			}
		}
	}
	if t := activeLinuxTray; t != nil && C.ct_wants_menu() != 0 {
		var items []MenuItem
		if t.build != nil {
			items = t.build()
		}
		items = append(items, MenuItem{Separator: true}, MenuItem{ID: 3, Label: "Start at login", Checked: linuxAutostartEnabled()}, MenuItem{ID: 1, Label: "Open log"}, MenuItem{ID: 2, Label: "Quit"})
		C.ct_menu_popup(linuxMenu(items))
	}
	return C.ct_hotkey() != 0
}
func dialog(msg string, question, isError bool) bool {
	answer := false
	withCString(msg, func(p *C.char) { answer = C.ct_dialog(p, C.int(boolInt(question)), C.int(boolInt(isError))) != 0 })
	return answer
}
func showError(msg string)     { dialog(msg, false, true) }
func showInfo(msg string)      { dialog(msg, false, false) }
func askYesNo(msg string) bool { return dialog(msg, true, false) }
func openTarget(target string) {
	if !strings.Contains(target, "://") {
		target, _ = filepath.Abs(target)
	}
	cmd := exec.Command("xdg-open", target)
	if err := cmd.Start(); err != nil {
		log.Printf("open: %v", err)
		return
	}
	go func() { _ = cmd.Wait() }()
}
