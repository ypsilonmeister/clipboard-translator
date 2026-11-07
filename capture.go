package main

import (
	"errors"
	"time"
)

// Why a selection could not be captured. startTranslation tells the user so
// instead of silently translating whatever happens to be on the clipboard.
var (
	errCaptureModifiersHeld = errors.New("the hotkey's modifier keys were still held down")
	errCaptureFocusChanged  = errors.New("the focused window changed before the selection could be copied")
)

// captureOps are the OS pieces of selection capture, so the ordering rules in
// captureWith can be tested without a desktop.
type captureOps struct {
	foreground      func() uintptr
	waitModifiers   func(time.Duration) bool // true once Ctrl/Alt/Shift/Win are all up
	sequence        func() uint32            // clipboard sequence number
	sendCopy        func() error             // synthetic Ctrl+C
	modifierTimeout time.Duration
	copyTimeout     time.Duration
}

// captureWith copies the selection of window win and reports the clipboard text
// and whether it came from a fresh selection (Ctrl+C changed the clipboard).
// When Ctrl+C changes nothing it returns the existing clipboard text with
// fromSelection false, which is the "translate what I copied" path.
//
// Order matters: modifiers are awaited first, and only then are the old text
// and the sequence number snapshotted and the focus checked, immediately before
// the keys are injected. A hotkey whose modifiers stay held, or whose window
// lost focus meanwhile, aborts instead of copying into another window or
// falling back to unrelated clipboard text.
func captureWith(cb Clipboard, win uintptr, ops captureOps) (text string, fromSelection bool, err error) {
	if !ops.waitModifiers(ops.modifierTimeout) {
		return "", false, errCaptureModifiersHeld
	}
	old, err := cb.Read()
	if err != nil {
		return "", false, err
	}
	before := ops.sequence()
	if ops.foreground() != win {
		return "", false, errCaptureFocusChanged
	}
	if err := ops.sendCopy(); err != nil {
		return old, false, nil // cannot inject keys: fall back to the existing clipboard text
	}
	deadline := time.Now().Add(ops.copyTimeout)
	for time.Now().Before(deadline) {
		if ops.sequence() != before {
			picked, err := cb.Read()
			if err != nil {
				return "", false, err
			}
			return picked, true, nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return old, false, nil
}
