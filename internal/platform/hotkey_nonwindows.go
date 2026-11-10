//go:build !windows && ((!linux && !darwin) || !cgo)

package platform

import (
	"errors"
	"time"
)

type Hotkey interface {
	Pressed() (bool, error)
	Close() error
}

func NewHotkey(string) (Hotkey, error) { return nil, errors.New("Windows only") }

func ForegroundWindow() uintptr                            { return 0 }
func WaitModifiersReleased(max time.Duration) bool         { return true }
func SendPaste() error                                     { return errors.New("Windows only") }
func clipboardSequence() uint32                            { return 0 }
func BeginSelectionCapture(Clipboard, uintptr) CaptureTask { return nil }
func CaptureSelectedText(cb Clipboard, win uintptr) (string, bool, error) {
	s, err := cb.Read()
	return s, false, err
}
