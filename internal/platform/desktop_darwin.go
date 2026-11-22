//go:build darwin && cgo

package platform

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa -framework Carbon -framework ApplicationServices -framework UserNotifications
#include <stdlib.h>
#include "native_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"github.com/ypsilonmeister/clipboard-translator/internal/hotkeyspec"
)

func PlatformRuntimeError() error {
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

var macFunctionKeyVK = map[int]int{
	1:  0x7A, // kVK_F1
	2:  0x78, // kVK_F2
	3:  0x63, // kVK_F3
	4:  0x76, // kVK_F4
	5:  0x60, // kVK_F5
	6:  0x61, // kVK_F6
	7:  0x62, // kVK_F7
	8:  0x64, // kVK_F8
	9:  0x65, // kVK_F9
	10: 0x6D, // kVK_F10
	11: 0x67, // kVK_F11
	12: 0x6F, // kVK_F12
}

func parseMacHotkey(spec string) (uint32, int, byte, error) {
	var mods uint32
	vk := -1
	var charCode byte
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
			if vk != -1 || charCode != 0 {
				return 0, 0, 0, fmt.Errorf("unsupported hotkey %q: use modifiers and one key (A-Z, 0-9, or F1-F12)", spec)
			}
			if len(part) == 1 && part[0] >= 'A' && part[0] <= 'Z' {
				charCode = part[0] + ('a' - 'A')
			} else if len(part) == 1 && part[0] >= '0' && part[0] <= '9' {
				charCode = part[0]
			} else if fn, ok := hotkeyspec.ParseFunctionKey(part); ok {
				vk = macFunctionKeyVK[fn]
			} else {
				return 0, 0, 0, fmt.Errorf("unsupported hotkey %q: use modifiers and one key (A-Z, 0-9, or F1-F12)", spec)
			}
		}
	}
	if mods == 0 || (vk == -1 && charCode == 0) {
		return 0, 0, 0, errors.New("hotkey must include a modifier and key (A-Z, 0-9, or F1-F12)")
	}
	return mods, vk, charCode, nil
}
func NewHotkey(spec string) (Hotkey, error) {
	mods, vk, charCode, err := parseMacHotkey(spec)
	if err != nil {
		return nil, err
	}
	if C.ct_mac_grab(C.uint(mods), C.int(vk), C.char(charCode)) == 0 {
		return nil, fmt.Errorf("could not register %s (already used, or unsupported keyboard layout?)", spec)
	}
	return &macHotkey{}, nil
}
func (*macHotkey) Pressed() (bool, error) { return PumpMessages(), nil }
func (*macHotkey) Close() error           { C.ct_mac_ungrab(); return nil }
func ForegroundWindow() uintptr           { return uintptr(C.ct_mac_focus()) }
func clipboardSequence() uint32           { return uint32(C.ct_mac_sequence()) }
func accessibilityGranted() bool          { return C.ct_mac_accessible() != 0 }
func WaitModifiersReleased(max time.Duration) bool {
	deadline := time.Now().Add(max)
	for C.ct_mac_modifiers() != 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(15 * time.Millisecond)
	}
	return true
}
func SendPaste() error {
	if C.ct_mac_send(1) == 0 {
		return errors.New("Cmd+V could not be sent; grant Accessibility access to Clipboard Translator in System Settings")
	}
	return nil
}
func sendCopy() error {
	if C.ct_mac_send(0) == 0 {
		return errors.New("Cmd+C could not be sent; grant Accessibility access to Clipboard Translator in System Settings")
	}
	return nil
}

// BeginSelectionCapture starts copying the selection of window win (see
// captureJob for the ordering rules); the app steps it from its run loop.
// Without Accessibility access no keys can be sent, so it returns nil and
// CaptureSelectedText translates the existing clipboard text instead.
func BeginSelectionCapture(cb Clipboard, win uintptr) CaptureTask {
	if !accessibilityGranted() {
		return nil
	}
	return newCaptureJob(cb, win, captureOps{
		foreground:      ForegroundWindow,
		modifiersUp:     func() bool { return C.ct_mac_modifiers() == 0 },
		sequence:        clipboardSequence,
		sendCopy:        sendCopy,
		modifierTimeout: 900 * time.Millisecond,
		copyTimeout:     700 * time.Millisecond,
		settleTime:      80 * time.Millisecond,
	})
}

func CaptureSelectedText(cb Clipboard, win uintptr) (string, bool, error) {
	if task := BeginSelectionCapture(cb, win); task != nil {
		return runCapture(task)
	}
	text, err := cb.Read()
	return text, false, err
}

type macTray struct {
	build    func() []MenuItem
	logPath  string
	commands []int
	state    TrayState
	quit     bool
	notes    *notifier
}

// macNoticeBridge is the Notification Center side of notifier (notify_darwin.m).
type macNoticeBridge struct{ available bool }

func (b macNoticeBridge) Available() bool    { return b.available }
func (b macNoticeBridge) Status() AuthStatus { return AuthStatus(C.ct_mac_notify_status()) }
func (b macNoticeBridge) Refresh() int64     { return int64(C.ct_mac_notify_refresh()) }
func (b macNoticeBridge) Applied() int64     { return int64(C.ct_mac_notify_applied()) }
func (b macNoticeBridge) Request() bool      { return C.ct_mac_notify_request() != 0 }
func (b macNoticeBridge) Post(id, title, body string, sound bool) {
	withCString(id, func(i *C.char) {
		withCString(title, func(t *C.char) {
			withCString(body, func(m *C.char) { C.ct_mac_notify_post(i, t, m, C.int(boolInt(sound))) })
		})
	})
}
func (b macNoticeBridge) Failures() (int, string) {
	var buf [256]C.char
	n := int(C.ct_mac_notify_failures(&buf[0], C.int(len(buf))))
	return n, C.GoString(&buf[0])
}

// macNotificationsItem is the tray's "Notifications..." item.
const macNotificationsItem = 5

// macNotificationsPane is System Settings > Notifications. Whether a deep link
// to the app's own entry works on macOS 13-26 is not verified, so none is used.
const macNotificationsPane = "x-apple.systempreferences:com.apple.Notifications-Settings.extension"

var activeMacTray *macTray

func NewTray(logPath string) Tray {
	t := &macTray{logPath: logPath}
	C.ct_mac_tray_create()
	t.notes = newNotifier(macNoticeBridge{available: C.ct_mac_notify_init() != 0})
	t.notes.openSettings = func() { _ = OpenTarget(macNotificationsPane) }
	activeMacTray = t
	if !accessibilityGranted() {
		log.Print("Accessibility access is not granted; translating existing clipboard text remains available. Open Accessibility Settings from the menu to enable selection capture and auto-paste.")
	}
	return t
}
func (*macTray) SetStatus(text string) { withCString(text, func(p *C.char) { C.ct_mac_status(p) }) }
func (t *macTray) SetState(state TrayState) {
	if t.state == state {
		return
	}
	t.state = state
	C.ct_mac_set_state(C.int(state))
}
func (t *macTray) Notify(title, message string, isError bool) {
	t.NotifyWith(title, message, isError, NoticeOptions{})
}

// NotifyWith always logs and sets the tooltip, as before Issue #67, and posts
// to Notification Center only when the system allows it (see decideNotice).
func (t *macTray) NotifyWith(title, message string, isError bool, o NoticeOptions) {
	log.Printf("%s: %s", title, message)
	t.SetStatus(title + ": " + message)
	t.notes.notice(title, message, isError, o)
}
func (t *macTray) SetNotificationMemory(stored string, save func(string) error) {
	t.notes.setMemory(stored, save)
}

// The app finds the permission side with a type assertion; fail the build,
// not that assertion, if a method goes missing.
var _ NotificationTray = (*macTray)(nil)

func (t *macTray) NotificationStatus() AuthStatus  { return t.notes.settingsStatus() }
func (t *macTray) NotificationStatusPending() bool { return t.notes.pending() }
func (t *macTray) RecheckNotificationStatus()      { t.notes.recheck() }
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
func PumpMessages() bool {
	C.ct_mac_pump()
	if t := activeMacTray; t != nil {
		t.notes.sync()
		if C.ct_mac_wants_menu() != 0 {
			t.notes.refresh(statusFreshFor) // for the next time; this menu shows the cached status
			var items []MenuItem
			if t.build != nil {
				items = t.build()
			}
			items = append(items, MenuItem{Separator: true},
				MenuItem{ID: 3, Label: "Start at login", Checked: macAutostartEnabled()},
				MenuItem{ID: 4, Label: "Open Accessibility Settings..."},
				MenuItem{Label: "Accessibility access granted", Checked: accessibilityGranted(), Disabled: true})
			items = append(items, t.notes.menuLines(macNotificationsItem)...)
			items = append(items, MenuItem{ID: 1, Label: "Open log"}, MenuItem{ID: 2, Label: "Quit"})
			C.ct_mac_menu_show(macMenu(items))
		}
		for id := int(C.ct_mac_command()); id != 0; id = int(C.ct_mac_command()) {
			switch id {
			case 1:
				_ = OpenTarget(t.logPath)
			case 2:
				t.quit = true
			case 3:
				if err := toggleMacAutostart(); err != nil {
					t.Notify("Startup setting failed", err.Error(), true)
				}
			case 4:
				_ = OpenTarget("x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility")
			case macNotificationsItem:
				t.notes.explicitAction() // acts in sync once the status is fresh
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
func ShowError(msg string)     { dialog(msg, false, true) }
func ShowInfo(msg string)      { dialog(msg, false, false) }
func AskYesNo(msg string) bool { return dialog(msg, true, false) }
func OpenTarget(target string) error {
	var ok bool
	withCString(target, func(p *C.char) { ok = C.ct_mac_open(p) != 0 })
	if !ok {
		cmd := exec.Command("open", target)
		if err := cmd.Start(); err != nil {
			log.Printf("open: %v", err)
			return err
		}
		go func() { _ = cmd.Wait() }()
	}
	return nil
}

func beginSetupProgress(path string) { withCString(path, func(p *C.char) { C.ct_mac_setup_begin(p) }) }
func setupCancelled() bool           { return C.ct_mac_setup_cancelled() != 0 }
func endSetupProgress()              { C.ct_mac_setup_end() }
