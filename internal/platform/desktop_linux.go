//go:build linux && cgo

package platform

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

type linuxHotkey struct {
	id     int
	spec   string
	closed bool
}

var x11Registry = newHotkeyRegistry(maxHotkeys)

func newHotkey(spec string) (Hotkey, error) {
	if err := platformRuntimeError(); err != nil {
		return nil, err
	}
	rawMods, keysym, err := parseX11Hotkey(spec)
	if err != nil {
		return nil, err
	}
	keycode := uint32(C.ct_keysym_to_keycode(C.ulong(keysym)))
	if keycode == 0 {
		return nil, fmt.Errorf("unsupported key %q: no keycode on X11 display", spec)
	}
	normMods := normalizeX11Modifiers(rawMods)

	id, err := x11Registry.Register(spec, keycode, rawMods)
	if err != nil {
		return nil, err
	}

	res := C.ct_hotkey_grab(C.int(id), C.uint(normMods), C.uint(keycode))
	if res != 1 {
		x11Registry.Unregister(id)
		switch res {
		case -1:
			return nil, fmt.Errorf("cannot register hotkey %s: maximum number of hotkeys (%d) reached; unregister an unused profile in Settings", spec, maxHotkeys)
		case -2:
			return nil, fmt.Errorf("hotkey %s is already registered; choose a different key combination in Settings", spec)
		case -3:
			return nil, fmt.Errorf("could not grab %s on X11: already used by another application; choose a different key combination in Settings or disable the shortcut in the other app", spec)
		default:
			return nil, fmt.Errorf("cannot register hotkey %s: could not initialize X11 hotkey; check your X display session", spec)
		}
	}
	return &linuxHotkey{id: id, spec: spec}, nil
}

func (h *linuxHotkey) Pressed() (bool, error) {
	if h.closed {
		return false, nil
	}
	pumpMessages()
	return C.ct_hotkey_check(C.int(h.id)) != 0, nil
}

func (h *linuxHotkey) Close() error {
	if h.closed {
		return nil
	}
	h.closed = true
	x11Registry.Unregister(h.id)
	C.ct_hotkey_ungrab(C.int(h.id))
	return nil
}

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

// beginSelectionCapture is nil on X11: PRIMARY is read at once, nothing waits.
func beginSelectionCapture(Clipboard, uintptr) captureTask { return nil }

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
	C.ct_hotkey_pump()
	if t := activeLinuxTray; t != nil {
		for id := int(C.ct_command()); id != 0; id = int(C.ct_command()) {
			switch id {
			case 1:
				_ = openTarget(t.logPath)
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
	return false
}
func dialog(msg string, question, isError bool) bool {
	answer := false
	withCString(msg, func(p *C.char) { answer = C.ct_dialog(p, C.int(boolInt(question)), C.int(boolInt(isError))) != 0 })
	return answer
}
func showError(msg string)     { dialog(msg, false, true) }
func showInfo(msg string)      { dialog(msg, false, false) }
func askYesNo(msg string) bool { return dialog(msg, true, false) }
func openTarget(target string) error {
	if !strings.Contains(target, "://") {
		target, _ = filepath.Abs(target)
	}
	cmd := exec.Command("xdg-open", target)
	if err := cmd.Start(); err != nil {
		log.Printf("open: %v", err)
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
