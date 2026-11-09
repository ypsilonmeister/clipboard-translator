//go:build !windows

package main

import (
	"errors"
)

func startFallbackEditor(path string) error {
	return errors.New("no fallback editor")
}
