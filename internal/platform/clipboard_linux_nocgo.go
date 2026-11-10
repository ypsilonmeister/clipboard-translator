//go:build linux && !cgo

package platform

import "errors"

func newClipboard() (Clipboard, error) {
	return nil, errors.New("Linux desktop support requires CGO_ENABLED=1")
}
