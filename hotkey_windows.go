//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"
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
	hotkeyID      = 0x72AB
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
type windowsHotkey struct{}

func parseHotkey(s string) (uint32, uintptr, error) {
	parts := strings.Split(strings.ToUpper(strings.ReplaceAll(s, " ", "")), "+")
	var mods uint32
	var key uintptr
	for _, p := range parts {
		switch p {
		case "CTRL", "CONTROL":
			mods |= modControl
		case "ALT":
			mods |= modAlt
		case "SHIFT":
			mods |= modShift
		case "WIN", "WINDOWS":
			mods |= modWin
		default:
			if len(p) != 1 || p[0] < 'A' || p[0] > 'Z' || key != 0 {
				return 0, 0, fmt.Errorf("unsupported hotkey %q: use Ctrl+Alt+E (one A-Z key)", s)
			}
			key = uintptr(p[0])
		}
	}
	if mods == 0 || key == 0 {
		return 0, 0, errors.New("hotkey must include a modifier and letter A-Z")
	}
	return mods | modNoRepeat, key, nil
}
func newHotkey(spec string) (Hotkey, error) {
	mods, vk, err := parseHotkey(spec)
	if err != nil {
		return nil, err
	}
	ok, _, callErr := registerHotKey.Call(0, hotkeyID, uintptr(mods), vk)
	if ok == 0 {
		return nil, fmt.Errorf("RegisterHotKey failed (used by another app?): %w", callErr)
	}
	return &windowsHotkey{}, nil
}
func (*windowsHotkey) Pressed() (bool, error) {
	var msg winMsg
	found := false
	for {
		ok, _, _ := peekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, wmHotkey, wmHotkey, pmRemove)
		if ok == 0 {
			break
		}
		if msg.Message == wmHotkey && msg.WParam == hotkeyID {
			found = true
		}
	}
	return found, nil
}
func (*windowsHotkey) Close() error { unregisterHotKey.Call(0, hotkeyID); return nil }

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

// captureSelectedText tries to copy selected content in the focused app.
// If Ctrl+C does not change the clipboard, it uses the previous clipboard text.
func captureSelectedText(cb Clipboard) (string, uint32, error) {
	old, err := cb.Read()
	if err != nil {
		return "", 0, err
	}
	before := clipboardSequence()
	// Avoid Ctrl+Alt+E modifiers contaminating the simulated Ctrl+C.
	deadline := time.Now().Add(900 * time.Millisecond)
	for modifierPressed() && time.Now().Before(deadline) {
		time.Sleep(15 * time.Millisecond)
	}
	if modifierPressed() {
		return old, before, nil
	}
	if err := sendCopy(); err != nil {
		return old, before, nil
	}
	deadline = time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		seq := clipboardSequence()
		if seq != before {
			picked, err := cb.Read()
			if err != nil {
				return "", 0, err
			}
			return picked, seq, nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return old, before, nil
}
