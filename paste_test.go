package main

import (
	"errors"
	"testing"
	"time"
)

func TestPasteRechecksFocusAfterWaiting(t *testing.T) {
	for _, tc := range []struct {
		name                string
		initial, after      uintptr
		released, wantPaste bool
	}{
		{"same target", 10, 10, true, true},
		{"changed while waiting", 10, 20, true, false},
		{"unknown target", 0, 0, true, false},
		{"modifiers held", 10, 10, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			focus, sent := tc.initial, false
			err := pasteWithFocus(func() uintptr { return focus }, func(time.Duration) bool { focus = tc.after; return tc.released }, func() error { sent = true; return nil })
			if sent != tc.wantPaste || (err == nil) != tc.wantPaste {
				t.Fatalf("sent=%v err=%v", sent, err)
			}
		})
	}
	want := errors.New("injection failed")
	err := pasteWithFocus(func() uintptr { return 10 }, func(time.Duration) bool { return true }, func() error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("lost paste error: %v", err)
	}
}
