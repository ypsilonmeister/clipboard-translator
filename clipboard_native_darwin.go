//go:build darwin && cgo

package main

/*
#include "native_darwin.h"
*/
import "C"

import (
	"errors"
	"strings"
	"unsafe"
)

type macClipboard struct{}

func newClipboard() (Clipboard, error) { return &macClipboard{}, nil }
func (*macClipboard) Read() (string, error) {
	p := C.ct_mac_read()
	if p == nil {
		return "", errors.New("macOS pasteboard read failed")
	}
	defer C.ct_mac_free(unsafe.Pointer(p))
	return C.GoString(p), nil
}
func (*macClipboard) Write(text string) error {
	if strings.IndexByte(text, 0) >= 0 {
		return errors.New("clipboard text contains a NUL byte")
	}
	ok := false
	withCString(text, func(p *C.char) { ok = C.ct_mac_write(p) != 0 })
	if !ok {
		return errors.New("macOS pasteboard write failed")
	}
	return nil
}
func (*macClipboard) Close() error { return nil }

// NSPasteboard has no atomic compare-and-set. Recheck changeCount immediately
// before writing; other processes can still race the final write.
func (c *macClipboard) ReplaceIfUnchanged(expected, translated string) (bool, error) {
	if strings.IndexByte(expected, 0) >= 0 || strings.IndexByte(translated, 0) >= 0 {
		return false, errors.New("clipboard text contains a NUL byte")
	}
	current, err := c.Read()
	if err != nil {
		return false, err
	}
	if !sameClipboardText(current, expected) {
		return false, nil
	}
	result := 0
	withCString(current, func(a *C.char) { withCString(translated, func(b *C.char) { result = int(C.ct_mac_replace(a, b)) }) })
	if result < 0 {
		return false, errors.New("macOS pasteboard write failed")
	}
	return result == 1, nil
}
