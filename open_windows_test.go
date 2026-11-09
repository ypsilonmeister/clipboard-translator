//go:build windows

package main

import (
	"path/filepath"
	"testing"
)

func TestNotepadCmd(t *testing.T) {
	path := filepath.Join("C:", "test", "config.json")
	cmd := notepadCmd(path)
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "notepad.exe" || cmd.Args[1] != path {
		t.Fatalf("unexpected cmd args: %v, want [notepad.exe %s]", cmd.Args, path)
	}
}
