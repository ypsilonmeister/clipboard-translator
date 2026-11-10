//go:build windows

package platform

import (
	"fmt"
	"log"
	"os"
	"strings"
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
	appIconID      = 1 // RT_GROUP_ICON id written by tools/mksyso
	imageIcon      = 1
	smCxSmIcon     = 49
	smCySmIcon     = 50
	swShowNormal   = 1
	mbOK           = 0x0
	mbIconError    = 0x10

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	menuOpenLog   = 1
	menuQuit      = 2
	menuAutostart = 3

	mfGrayed    = 0x0001
	mfChecked   = 0x0008
	mfPopup     = 0x0010
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
	loadImageW           = user32.NewProc("LoadImageW")
	getSystemMetrics     = user32.NewProc("GetSystemMetrics")
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
	menuBuild      func() []MenuItem // app-specific menu items, see Tray.SetMenu
	cmds           []int             // picked app items, drained by NextCommand (UI thread only)
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

// NewTray creates the tray icon. On failure it logs the reason and returns a
// no-op tray so the translator keeps working without an icon.
func NewTray(logPath string) Tray {
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

// loadAppIcon returns the icon embedded in the executable at the system's
// small-icon size. The manifest makes the process DPI aware, so that size grows
// with the display scale (16, 20, 24, ... px) and the matching image of the
// icon is picked instead of a stretched 16px one. A build without the resource
// (for example without resource_windows_amd64.syso) gets the stock application
// icon.
func loadAppIcon(hInst uintptr) uintptr {
	cx, _, _ := getSystemMetrics.Call(smCxSmIcon)
	cy, _, _ := getSystemMetrics.Call(smCySmIcon)
	if h, _, _ := loadImageW.Call(hInst, appIconID, imageIcon, cx, cy, 0); h != 0 {
		return h
	}
	h, _, _ := loadIconW.Call(0, idiApplication)
	return h
}

// AppIconIsEmbedded reports whether the icon resource linked into the
// executable (resource_windows_amd64.syso, which belongs to package main) is
// found, instead of the generic application icon the tray falls back to.
func AppIconIsEmbedded() bool {
	hInst, _, _ := getModuleHandleW.Call(0)
	stock, _, _ := loadIconW.Call(0, idiApplication)
	h := loadAppIcon(hInst)
	return h != 0 && h != stock
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
	t.icon = loadAppIcon(hInst)
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

func (t *winTray) SetMenu(build func() []MenuItem) { t.menuBuild = build }

func (t *winTray) NextCommand() (int, bool) {
	if len(t.cmds) == 0 {
		return 0, false
	}
	id := t.cmds[0]
	t.cmds = t.cmds[1:]
	return id, true
}

// menuLabel escapes '&' (a keyboard-accelerator marker in menu text) so labels
// such as clipboard text or "A & B" are shown literally.
func menuLabel(s string) string { return strings.ReplaceAll(s, "&", "&&") }

// buildPopup turns items into a popup menu (submenus included). Destroying the
// returned menu destroys its submenus too.
func buildPopup(items []MenuItem) uintptr {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return 0
	}
	for _, it := range items {
		switch {
		case it.Separator:
			appendMenuW.Call(menu, mfSeparator, 0, 0)
		case len(it.Sub) > 0:
			sub := buildPopup(it.Sub)
			flags := uintptr(mfPopup)
			if it.Disabled {
				flags |= mfGrayed
			}
			appendMenuW.Call(menu, flags, sub, uintptr(unsafe.Pointer(u16(menuLabel(it.Label)))))
		default:
			flags := uintptr(0)
			if it.Checked {
				flags |= mfChecked
			}
			if it.Disabled {
				flags |= mfGrayed
			}
			appendMenuW.Call(menu, flags, uintptr(it.ID), uintptr(unsafe.Pointer(u16(menuLabel(it.Label)))))
		}
	}
	return menu
}

func (t *winTray) showMenu() {
	var appItems []MenuItem
	if t.menuBuild != nil {
		appItems = t.menuBuild()
	}
	if len(appItems) > 0 {
		appItems = append(appItems, MenuItem{Separator: true})
	}
	menu := buildPopup(appItems)
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
	if cmd >= FirstAppCommand {
		t.cmds = append(t.cmds, int(cmd))
		return
	}
	switch cmd {
	case menuOpenLog:
		_ = OpenTarget(t.logPath)
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

// PumpMessages dispatches every pending message of the calling thread (tray
// window, menus) and reports whether one of the registered hotkeys fired. It
// must be called on the thread that created the tray and registered the hotkeys.
// Callers that only want the tray serviced (model loading, a server restart)
// use this: a hotkey pressed meanwhile is dropped, as before.
func PumpMessages() bool { return pump(false) }

// pumpHotkeys is pumpMessages for the main loop: it also records which hotkeys
// fired, for each windowsHotkey.Pressed to take.
func pumpHotkeys() { pump(true) }

func pump(record bool) bool {
	var msg winMsg
	fired := false
	for {
		ok, _, _ := peekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove)
		if ok == 0 {
			return fired
		}
		if msg.Message == wmHotkey {
			if isHotkeyID(msg.WParam) {
				fired = true
				if record {
					firedHotkeys[msg.WParam] = true
				}
			}
			continue
		}
		dispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// OpenTarget opens a file or URL with the program Windows associates with it.
func OpenTarget(target string) error {
	r, _, _ := shellExecuteW.Call(0, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16(target))), 0, 0, swShowNormal)
	if r <= 32 {
		return fmt.Errorf("ShellExecute failed with code %d", r)
	}
	return nil
}

// ShowInfo shows a plain information box.
func ShowInfo(msg string) {
	const mbIconInformation = 0x40
	messageBoxW.Call(0, uintptr(unsafe.Pointer(u16(msg))), uintptr(unsafe.Pointer(u16("Clipboard Translator"))), mbOK|mbIconInformation)
}

// AskYesNo shows a Yes/No question and reports whether the user chose Yes.
func AskYesNo(msg string) bool {
	const mbYesNo, mbIconQuestion, idYes = 0x4, 0x20, 6
	r, _, _ := messageBoxW.Call(0, uintptr(unsafe.Pointer(u16(msg))), uintptr(unsafe.Pointer(u16("Clipboard Translator"))), mbYesNo|mbIconQuestion)
	return r == idYes
}

// ShowError reports a fatal problem to the user. The GUI build has no console,
// so a message box is the only way to make a startup failure visible.
func ShowError(msg string) {
	if hwnd, _, _ := getConsoleWindowProc.Call(); hwnd != 0 {
		return // a console is attached; the log line is already visible
	}
	messageBoxW.Call(0, uintptr(unsafe.Pointer(u16(msg))), uintptr(unsafe.Pointer(u16("Clipboard Translator"))), mbOK|mbIconError)
}
