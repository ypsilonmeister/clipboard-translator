//go:build linux && !cgo

package main

import "errors"

func newClipboard() (Clipboard, error) {
	return nil, errors.New("Linux desktop support requires CGO_ENABLED=1")
}
