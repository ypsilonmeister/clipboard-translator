//go:build linux && cgo

package platform

/*
#include "native_linux.h"
*/
import "C"

import (
	"errors"
	"strings"
	"unsafe"
)

type linuxClipboard struct{ portalWorkflowUnavailable bool }

func (c *linuxClipboard) DesktopStatus() DesktopStatus {
	return DesktopStatus{PortalWorkflowUnavailable: c.portalWorkflowUnavailable}
}
func (*linuxClipboard) RetryDesktop() {}

func NewClipboard() (Clipboard, error) {
	if linuxWayland {
		if activePortalDesktop == nil {
			return nil, errors.New("Wayland desktop was not initialized")
		}
		return activePortalDesktop, nil
	}
	if C.ct_init() == 0 {
		return nil, errors.New("X11 clipboard is unavailable")
	}
	return &linuxClipboard{portalWorkflowUnavailable: linuxPortalWorkflowUnavailable}, nil
}
func (*linuxClipboard) Read() (string, error) { return readX11Text(false), nil }
func (*linuxClipboard) Write(text string) error {
	if strings.IndexByte(text, 0) >= 0 {
		return errors.New("clipboard text contains a NUL byte")
	}
	ok := false
	withCString(text, func(p *C.char) { ok = C.ct_write(p) != 0 })
	if !ok {
		return errors.New("X11 clipboard write failed")
	}
	return nil
}
func (*linuxClipboard) Close() error { return nil }

// X11 has no atomic compare-and-set operation for selections. Check immediately
// before claiming CLIPBOARD; another X11 client can still race this operation.
func (c *linuxClipboard) ReplaceIfUnchanged(expected, translated string) (bool, error) {
	current, err := c.Read()
	if err != nil || !SameClipboardText(current, expected) {
		return false, err
	}
	if err := c.Write(translated); err != nil {
		return false, err
	}
	return true, nil
}

// Manual mode uses only the focused GTK client's regular clipboard access.
type manualGTKClipboard struct{}

func (*manualGTKClipboard) Read() (string, error) {
	p := C.ct_manual_read()
	if p == nil {
		return "", ErrClipboardPending
	}
	defer C.ct_free(unsafe.Pointer(p))
	return C.GoString(p), nil
}
func (*manualGTKClipboard) Write(text string) error {
	if strings.IndexByte(text, 0) >= 0 {
		return errors.New("clipboard text contains a NUL byte")
	}
	withCString(text, func(p *C.char) { C.ct_manual_write(p) })
	return nil
}
func (*manualGTKClipboard) Close() error { return nil }
func (c *manualGTKClipboard) ReplaceIfUnchanged(old, text string) (bool, error) {
	current, err := c.Read()
	if err != nil || !SameClipboardText(current, old) {
		return false, err
	}
	return true, c.Write(text)
}

func (*manualGTKClipboard) CancelPendingRead() { C.ct_manual_cancel_read() }
