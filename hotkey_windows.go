//go:build windows

package main

import (
	"fmt"
	"time"
	"unsafe"
)

const (
	wmHotkey      = 0x0312
	pmRemove      = 0x0001
	modAlt        = 0x0001
	modControl    = 0x0002
	modShift      = 0x0004
	modWin        = 0x0008
	modNoRepeat   = 0x4000
	keyboardInput = 1
	keyEventKeyUp = 0x0002
	vkControl     = 0x11
	vkMenu        = 0x12
	vkShift       = 0x10
	vkLWin        = 0x5B
	vkRWin        = 0x5C
	vkC           = 0x43
	// Hotkey ids are hotkeyID .. hotkeyID+maxWindowsHotkeys-1, one per profile.
	hotkeyID = 0x72AB
)

var (
	registerHotKey             = user32.NewProc("RegisterHotKey")
	unregisterHotKey           = user32.NewProc("UnregisterHotKey")
	peekMessageW               = user32.NewProc("PeekMessageW")
	sendInput                  = user32.NewProc("SendInput")
	getAsyncKeyState           = user32.NewProc("GetAsyncKeyState")
	getClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
)

type winPoint struct{ X, Y int32 }
type winMsg struct {
	HWND    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      winPoint
	Private uint32
}
type keyboardInputRecord struct {
	Type      uint32
	Pad       uint32
	VK        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	Pad2      uint32
	ExtraInfo uintptr
	Reserved  [8]byte // INPUT union includes 32-byte MOUSEINPUT on Win64
}

type Hotkey interface {
	Pressed() (bool, error)
	Close() error
}

// windowsHotkey is one RegisterHotKey registration. All of them share the
// thread's message queue, so the pump records which ids fired and each hotkey
// takes its own (see pumpHotkeys). Everything runs on the UI thread.
type windowsHotkey struct {
	id     uintptr
	closed bool
}

var (
	hotkeyIDUsed [maxWindowsHotkeys]bool
	// firedHotkeys holds ids whose WM_HOTKEY was read by pumpHotkeys and not yet
	// taken by that hotkey's Pressed.
	firedHotkeys = map[uintptr]bool{}
)

func isHotkeyID(id uintptr) bool { return id >= hotkeyID && id < hotkeyID+maxWindowsHotkeys }

func windowsVK(key string) (uintptr, error) {
	if len(key) == 1 {
		c := key[0]
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			return uintptr(c), nil
		}
	}
	if fn, ok := parseFunctionKey(key); ok {
		return uintptr(0x70 + fn - 1), nil
	}
	return 0, fmt.Errorf("unsupported key %q", key)
}

func parseHotkey(s string) (uint32, uintptr, error) {
	h, err := parseHotkeySpec(s, false) // same syntax check as loadConfig
	if err != nil {
		return 0, 0, err
	}
	var mods uint32
	if h.ctrl {
		mods |= modControl
	}
	if h.alt {
		mods |= modAlt
	}
	if h.shift {
		mods |= modShift
	}
	if h.win {
		mods |= modWin
	}
	vk, err := windowsVK(h.key)
	if err != nil {
		return 0, 0, err
	}
	return mods | modNoRepeat, vk, nil
}
func newHotkey(spec string) (Hotkey, error) {
	mods, vk, err := parseHotkey(spec)
	if err != nil {
		return nil, err
	}
	slot := -1
	for i, used := range hotkeyIDUsed {
		if !used {
			slot = i
			break
		}
	}
	if slot < 0 {
		return nil, fmt.Errorf("at most %d hotkeys can be registered", maxWindowsHotkeys)
	}
	id := uintptr(hotkeyID + slot)
	ok, _, callErr := registerHotKey.Call(0, id, uintptr(mods), vk)
	if ok == 0 {
		return nil, fmt.Errorf("RegisterHotKey failed (used by another app?): %w", callErr)
	}
	hotkeyIDUsed[slot] = true
	return &windowsHotkey{id: id}, nil
}

// Pressed drains the thread's message queue (which also services the tray
// window) and reports whether this hotkey fired.
func (h *windowsHotkey) Pressed() (bool, error) {
	pumpHotkeys()
	fired := firedHotkeys[h.id]
	delete(firedHotkeys, h.id)
	return fired, nil
}

func (h *windowsHotkey) Close() error {
	if h.closed {
		return nil
	}
	h.closed = true
	unregisterHotKey.Call(0, h.id)
	hotkeyIDUsed[h.id-hotkeyID] = false
	delete(firedHotkeys, h.id)
	return nil
}

func clipboardSequence() uint32 {
	n, _, _ := getClipboardSequenceNumber.Call()
	return uint32(n)
}
func modifierPressed() bool {
	for _, k := range []uintptr{vkControl, vkMenu, vkShift, vkLWin, vkRWin} {
		n, _, _ := getAsyncKeyState.Call(k)
		if n&0x8000 != 0 {
			return true
		}
	}
	return false
}
func sendCopy() error {
	inputs := []keyboardInputRecord{
		{Type: keyboardInput, VK: vkControl},
		{Type: keyboardInput, VK: vkC},
		{Type: keyboardInput, VK: vkC, Flags: keyEventKeyUp},
		{Type: keyboardInput, VK: vkControl, Flags: keyEventKeyUp},
	}
	sent, _, err := sendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	if sent != uintptr(len(inputs)) {
		return fmt.Errorf("SendInput copied %d keys: %w", sent, err)
	}
	return nil
}

const vkV = 0x56

var getForegroundWindow = user32.NewProc("GetForegroundWindow")

// foregroundWindow returns the window that currently has the keyboard focus
// (0 when there is none).
func foregroundWindow() uintptr {
	h, _, _ := getForegroundWindow.Call()
	return h
}

// waitModifiersReleased waits until Ctrl, Alt, Shift and Win are all up, so a
// synthetic Ctrl+V is not turned into Ctrl+Alt+V by keys still held from the
// hotkey. It reports whether they were released within max.
func waitModifiersReleased(max time.Duration) bool {
	deadline := time.Now().Add(max)
	for modifierPressed() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(15 * time.Millisecond)
	}
	return true
}

// sendPaste types Ctrl+V into the focused window.
func sendPaste() error {
	inputs := []keyboardInputRecord{
		{Type: keyboardInput, VK: vkControl},
		{Type: keyboardInput, VK: vkV},
		{Type: keyboardInput, VK: vkV, Flags: keyEventKeyUp},
		{Type: keyboardInput, VK: vkControl, Flags: keyEventKeyUp},
	}
	sent, _, err := sendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	if sent != uintptr(len(inputs)) {
		return fmt.Errorf("SendInput pasted %d keys: %w", sent, err)
	}
	return nil
}

// captureSelectedText tries to copy the selected content of window win, the
// window that was in front when the hotkey was pressed. If Ctrl+C does not
// change the clipboard, it uses the previous clipboard text (fromSelection is
// then false). See captureWith for the ordering rules.
func captureSelectedText(cb Clipboard, win uintptr) (string, bool, error) {
	return captureWith(cb, win, captureOps{
		foreground:      foregroundWindow,
		waitModifiers:   waitModifiersReleased, // keep Ctrl+Alt+E from contaminating the simulated Ctrl+C
		sequence:        clipboardSequence,
		sendCopy:        sendCopy,
		modifierTimeout: 900 * time.Millisecond,
		copyTimeout:     700 * time.Millisecond,
	})
}
