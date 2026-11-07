//go:build !windows && ((!linux && !darwin) || !cgo)

package main

import (
	"errors"
	"time"
)

type Hotkey interface {
	Pressed() (bool, error)
	Close() error
}

func newHotkey(string) (Hotkey, error) { return nil, errors.New("Windows only") }

func foregroundWindow() uintptr                    { return 0 }
func waitModifiersReleased(max time.Duration) bool { return true }
func sendPaste() error                             { return errors.New("Windows only") }
func clipboardSequence() uint32                    { return 0 }
func captureSelectedText(cb Clipboard, win uintptr) (string, bool, error) {
	s, err := cb.Read()
	return s, false, err
}
