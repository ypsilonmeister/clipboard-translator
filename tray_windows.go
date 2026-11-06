//go:build windows

package main

import (
	"log"
	"os"
	"sync/atomic"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	wmApp          = 0x8000
	wmTrayCallback = wmApp + 1
	wmLButtonUp    = 0x0202
	wmRButtonUp    = 0x0205
	wmContextMenu  = 0x007B
	wmNull         = 0x0000

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	nifInfo    = 0x10

	niifInfo    = 0x01
	niifError   = 0x03
	niifNoSound = 0x10

	idiApplication = 32512
	swShowNormal   = 1
	mbOK           = 0x0
	mbIconError    = 0x10

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	menuOpenLog   = 1
	menuQuit      = 2
	menuAutostart = 3

	mfChecked   = 0x0008
	mfSeparator = 0x0800
)

var (
	shell32 = syscall.NewLazyDLL("shell32.dll")

	shellNotifyIconW     = shell32.NewProc("Shell_NotifyIconW")
	shellExecuteW        = shell32.NewProc("ShellExecuteW")
	registerClassExW     = user32.NewProc("RegisterClassExW")
	defWindowProcW       = user32.NewProc("DefWindowProcW")
	dispatchMessageW     = user32.NewProc("DispatchMessageW")
	loadIconW            = user32.NewProc("LoadIconW")
	createPopupMenu      = user32.NewProc("CreatePopupMenu")
	appendMenuW          = user32.NewProc("AppendMenuW")
	trackPopupMenu       = user32.NewProc("TrackPopupMenu")
	destroyMenu          = user32.NewProc("DestroyMenu")
	getCursorPos         = user32.NewProc("GetCursorPos")
	setForegroundWindow  = user32.NewProc("SetForegroundWindow")
	postMessageW         = user32.NewProc("PostMessageW")
	registerWindowMsgW   = user32.NewProc("RegisterWindowMessageW")
	messageBoxW          = user32.NewProc("MessageBoxW")
	getModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	getConsoleWindowProc = kernel32.NewProc("GetConsoleWindow")
)

// notifyIconData mirrors NOTIFYICONDATAW on 64-bit Windows (976 bytes).
type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type winTray struct {
	hwnd           uintptr
	icon           uintptr
	logPath        string
	exeCommand     string // quoted path used for the "start with Windows" entry
	taskbarCreated uint32
	tip            string
	quit           atomic.Bool
}

// The window procedure is a plain callback, so it reaches the tray through a
// package-level variable. Only one tray exists per process.
var (
	activeTray *winTray
	wndProcPtr = syscall.NewCallback(trayWndProc)
)

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// copyUTF16 copies s into dst, truncating so that a terminating NUL always fits.
func copyUTF16(dst []uint16, s string) {
	u := utf16.Encode([]rune(s))
	if len(u) > len(dst)-1 {
		u = u[:len(dst)-1]
	}
	copy(dst, u)
}

// newTray creates the tray icon. On failure it logs the reason and returns a
// no-op tray so the translator keeps working without an icon.
func newTray(logPath string) Tray {
	t := &winTray{logPath: logPath, tip: "Clipboard Translator"}
	if exe, err := os.Executable(); err == nil {
		t.exeCommand = `"` + exe + `"`
	}
	if err := t.init(); err != nil {
		log.Printf("tray icon unavailable: %v", err)
		return noopTray{}
	}
	return t
}

func (t *winTray) init() error {
	hInst, _, _ := getModuleHandleW.Call(0)
	className := u16("ClipboardTranslatorTray")
	wc := wndClassEx{
		LpfnWndProc:   wndProcPtr,
		HInstance:     hInst,
		LpszClassName: className,
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if atom, _, err := registerClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return err
	}
	// A hidden top-level window (not message-only) so it also receives the
	// "TaskbarCreated" broadcast and can restore the icon after Explorer restarts.
	hwnd, _, err := createWindowExW.Call(0, uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(u16("ClipboardTranslator tray"))), 0, 0, 0, 0, 0, 0, 0, hInst, 0)
	if hwnd == 0 {
		return err
	}
	t.hwnd = hwnd
	t.icon, _, _ = loadIconW.Call(0, idiApplication)
	m, _, _ := registerWindowMsgW.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))
	t.taskbarCreated = uint32(m)
	activeTray = t
	if !t.add() {
		activeTray = nil
		destroyWindow.Call(hwnd)
		t.hwnd = 0
		return syscall.EINVAL
	}
	return nil
}

func (t *winTray) data(flags uint32) notifyIconData {
	d := notifyIconData{HWnd: t.hwnd, UID: 1, UFlags: flags, UCallbackMessage: wmTrayCallback, HIcon: t.icon}
	d.CbSize = uint32(unsafe.Sizeof(d))
	return d
}

func (t *winTray) add() bool {
	d := t.data(nifMessage | nifIcon | nifTip)
	copyUTF16(d.SzTip[:], t.tip)
	ok, _, _ := shellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&d)))
	return ok != 0
}

func (t *winTray) QuitRequested() bool { return t.quit.Load() }

func (t *winTray) SetStatus(text string) {
	if t.hwnd == 0 || text == t.tip {
		return
	}
	t.tip = text
	d := t.data(nifTip)
	copyUTF16(d.SzTip[:], text)
	shellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&d)))
}

func (t *winTray) Notify(title, message string, isError bool) {
	if t.hwnd == 0 {
		return
	}
	d := t.data(nifInfo)
	copyUTF16(d.SzInfoTitle[:], title)
	copyUTF16(d.SzInfo[:], message)
	d.DwInfoFlags = niifInfo
	if isError {
		d.DwInfoFlags = niifError
	} else {
		d.DwInfoFlags |= niifNoSound
	}
	shellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&d)))
}

func (t *winTray) Close() {
	if t.hwnd == 0 {
		return
	}
	d := t.data(0)
	shellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&d)))
	destroyWindow.Call(t.hwnd)
	t.hwnd = 0
	activeTray = nil
}

func (t *winTray) showMenu() {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	autostartOn := t.exeCommand != "" && appAutostart.Enabled(t.exeCommand)
	if t.exeCommand != "" {
		flags := uintptr(0)
		if autostartOn {
			flags = mfChecked
		}
		appendMenuW.Call(menu, flags, menuAutostart, uintptr(unsafe.Pointer(u16("Start with Windows"))))
	}
	appendMenuW.Call(menu, 0, menuOpenLog, uintptr(unsafe.Pointer(u16("Open log"))))
	appendMenuW.Call(menu, mfSeparator, 0, 0)
	appendMenuW.Call(menu, 0, menuQuit, uintptr(unsafe.Pointer(u16("Quit"))))
	var pt winPoint
	getCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Required so the menu closes when the user clicks elsewhere.
	setForegroundWindow.Call(t.hwnd)
	cmd, _, _ := trackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd, uintptr(pt.X), uintptr(pt.Y), 0, t.hwnd, 0)
	postMessageW.Call(t.hwnd, wmNull, 0, 0)
	switch cmd {
	case menuOpenLog:
		shellExecuteW.Call(0, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16(t.logPath))), 0, 0, swShowNormal)
	case menuAutostart:
		var err error
		if autostartOn {
			err = appAutostart.Clear()
		} else {
			err = appAutostart.Set(t.exeCommand)
		}
		if err != nil {
			log.Printf("start-with-Windows setting failed: %v", err)
			t.Notify("Clipboard Translator", "Could not change the startup setting: "+err.Error(), true)
		}
	case menuQuit:
		t.quit.Store(true)
	}
}

func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	if t := activeTray; t != nil {
		switch {
		case uint32(msg) == wmTrayCallback:
			switch uint32(lparam) {
			case wmRButtonUp, wmContextMenu, wmLButtonUp:
				t.showMenu()
			}
			return 0
		case t.taskbarCreated != 0 && uint32(msg) == t.taskbarCreated:
			t.add()
			return 0
		}
	}
	ret, _, _ := defWindowProcW.Call(hwnd, msg, wparam, lparam)
	return ret
}

// pumpMessages dispatches every pending message of the calling thread (tray
// window, menus) and reports whether the registered hotkey fired. It must be
// called on the thread that created the tray and registered the hotkey.
func pumpMessages() bool {
	var msg winMsg
	fired := false
	for {
		ok, _, _ := peekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove)
		if ok == 0 {
			return fired
		}
		if msg.Message == wmHotkey {
			if msg.WParam == hotkeyID {
				fired = true
			}
			continue
		}
		dispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// showError reports a fatal problem to the user. The GUI build has no console,
// so a message box is the only way to make a startup failure visible.
func showError(msg string) {
	if hwnd, _, _ := getConsoleWindowProc.Call(); hwnd != 0 {
		return // a console is attached; the log line is already visible
	}
	messageBoxW.Call(0, uintptr(unsafe.Pointer(u16(msg))), uintptr(unsafe.Pointer(u16("Clipboard Translator"))), mbOK|mbIconError)
}
