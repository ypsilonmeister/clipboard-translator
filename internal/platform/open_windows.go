//go:build windows

package platform

import (
	"os/exec"
)

func notepadCmd(path string) *exec.Cmd {
	return exec.Command("notepad.exe", path)
}

func StartFallbackEditor(path string) error {
	cmd := notepadCmd(path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
