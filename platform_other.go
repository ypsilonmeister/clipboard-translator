//go:build !windows && ((!linux && !darwin) || !cgo)

package main

import "errors"

func platformRuntimeError() error {
	return errors.New("supported desktops: Windows, Linux X11 and macOS (Linux/macOS desktop builds require CGO_ENABLED=1)")
}
