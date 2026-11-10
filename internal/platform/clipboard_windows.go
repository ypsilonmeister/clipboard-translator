//go:build windows

package platform

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	createWindowExW            = user32.NewProc("CreateWindowExW")
	destroyWindow              = user32.NewProc("DestroyWindow")
	openClipboard              = user32.NewProc("OpenClipboard")
	closeClipboard             = user32.NewProc("CloseClipboard")
	getClipboardData           = user32.NewProc("GetClipboardData")
	setClipboardData           = user32.NewProc("SetClipboardData")
	isClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	emptyClipboard             = user32.NewProc("EmptyClipboard")
	globalAlloc                = kernel32.NewProc("GlobalAlloc")
	globalFree                 = kernel32.NewProc("GlobalFree")
	globalLock                 = kernel32.NewProc("GlobalLock")
	globalUnlock               = kernel32.NewProc("GlobalUnlock")
	globalSize                 = kernel32.NewProc("GlobalSize")
)

type winClipboard struct{ hwnd uintptr }

func newClipboard() (Clipboard, error) {
	// A hidden window is required: EmptyClipboard() after OpenClipboard(NULL)
	// can leave the clipboard without an owner and make SetClipboardData fail.
	class, _ := syscall.UTF16PtrFromString("STATIC")
	title, _ := syscall.UTF16PtrFromString("ClipboardTranslator")
	hwnd, _, err := createWindowExW.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if hwnd == 0 {
		return nil, fmt.Errorf("create clipboard owner window: %w", err)
	}
	return &winClipboard{hwnd: hwnd}, nil
}

func (w *winClipboard) open() error {
	for i := 0; i < 8; i++ {
		if r, _, _ := openClipboard.Call(w.hwnd); r != 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("clipboard is busy")
}

func (w *winClipboard) Read() (string, error) {
	if err := w.open(); err != nil {
		return "", err
	}
	defer closeClipboard.Call()
	return w.readLocked()
}

// readLocked requires the caller to hold OpenClipboard.
// lockedPtr turns an address returned by GlobalLock into a pointer. That memory
// belongs to Windows, not the Go heap, so the garbage collector never moves or
// frees it; reading the pointer back through its own address keeps go vet's
// unsafeptr check quiet about the uintptr round trip.
func lockedPtr(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

func (w *winClipboard) readLocked() (string, error) {
	if ok, _, _ := isClipboardFormatAvailable.Call(cfUnicodeText); ok == 0 {
		return "", nil
	}
	h, _, _ := getClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", errors.New("GetClipboardData failed")
	}
	ptr, _, _ := globalLock.Call(h)
	if ptr == 0 {
		return "", errors.New("GlobalLock failed")
	}
	defer globalUnlock.Call(h)
	nBytes, _, _ := globalSize.Call(h)
	if nBytes == 0 {
		return "", nil
	}
	// Guard against unexpected or corrupt clipboard memory sizes.
	if nBytes > 32*1024*1024 {
		return "", errors.New("clipboard text too large")
	}
	chars := unsafe.Slice((*uint16)(lockedPtr(ptr)), int(nBytes/2))
	i := 0
	for i < len(chars) && chars[i] != 0 {
		i++
	}
	return string(utf16.Decode(chars[:i])), nil
}

func (w *winClipboard) Write(s string) error {
	if err := w.open(); err != nil {
		return err
	}
	defer closeClipboard.Call()
	return w.writeLocked(s)
}

// ReplaceIfUnchanged does not depend on GetClipboardSequenceNumber, which may
// advance when clipboard formats are updated without changing Unicode text.
// Opening the clipboard across the comparison AND write prevents TOCTOU races
// with other clipboard writers.
func (w *winClipboard) ReplaceIfUnchanged(expected, translated string) (bool, error) {
	if err := w.open(); err != nil {
		return false, err
	}
	defer closeClipboard.Call()
	current, err := w.readLocked()
	if err != nil {
		return false, err
	}
	if !sameClipboardText(current, expected) {
		return false, nil
	}
	if err := w.writeLocked(translated); err != nil {
		return false, err
	}
	return true, nil
}

// writeLocked requires the caller to hold OpenClipboard.
func (w *winClipboard) writeLocked(s string) error {
	utf16buf := utf16.Encode([]rune(s))
	utf16buf = append(utf16buf, 0)
	bytes := uintptr(len(utf16buf) * 2)
	h, _, _ := globalAlloc.Call(gmemMoveable, bytes)
	if h == 0 {
		return errors.New("GlobalAlloc failed")
	}
	ptr, _, _ := globalLock.Call(h)
	if ptr == 0 {
		globalFree.Call(h)
		return errors.New("GlobalLock failed")
	}
	copy(unsafe.Slice((*uint16)(lockedPtr(ptr)), len(utf16buf)), utf16buf)
	globalUnlock.Call(h)
	if ok, _, _ := emptyClipboard.Call(); ok == 0 {
		globalFree.Call(h)
		return errors.New("EmptyClipboard failed")
	}
	if ok, _, _ := setClipboardData.Call(cfUnicodeText, h); ok == 0 {
		globalFree.Call(h)
		return errors.New("SetClipboardData failed")
	}
	return nil // Clipboard owns the memory from here on.
}

func (w *winClipboard) Close() error {
	if w.hwnd != 0 {
		destroyWindow.Call(w.hwnd)
		w.hwnd = 0
	}
	return nil
}
