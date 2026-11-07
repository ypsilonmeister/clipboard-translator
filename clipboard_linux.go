//go:build linux && cgo

package main

/*
#include "native_linux.h"
*/
import "C"

import (
	"errors"
	"strings"
)

type linuxClipboard struct{}

func newClipboard() (Clipboard, error) {
	if C.ct_init() == 0 {
		return nil, errors.New("X11 clipboard is unavailable")
	}
	return &linuxClipboard{}, nil
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
	if err != nil || current != expected {
		return false, err
	}
	if err := c.Write(translated); err != nil {
		return false, err
	}
	return true, nil
}
