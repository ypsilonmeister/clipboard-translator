//go:build !windows

package main

import "errors"

type Hotkey interface {
	Pressed() (bool, error)
	Close() error
}

func newHotkey(string) (Hotkey, error) { return nil, errors.New("Windows only") }
func clipboardSequence() uint32        { return 0 }
func captureSelectedText(cb Clipboard) (string, uint32, error) {
	s, err := cb.Read()
	return s, 0, err
}
