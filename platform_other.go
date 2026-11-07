//go:build !windows && (!linux || !cgo)

package main

import "errors"

func platformRuntimeError() error {
	return errors.New("supported desktops: Windows x64 and Linux X11 (build with CGO_ENABLED=1 and GTK 3/X11/XTest development libraries)")
}
