//go:build windows

package main

import (
	"os/exec"
)

func notepadCmd(path string) *exec.Cmd {
	return exec.Command("notepad.exe", path)
}

func startFallbackEditor(path string) error {
	cmd := notepadCmd(path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
