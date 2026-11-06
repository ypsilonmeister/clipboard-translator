//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	createNoWindow   = 0x08000000
	createNewConsole = 0x00000010
)

// hideConsole stops a console program (llama-server.exe) from opening its own
// console window when started by this GUI/tray process. Its output is already
// redirected to server.log, so nothing is lost.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// setupCommand builds the PowerShell invocation that runs setup.ps1 and keeps
// the window open (waiting for Enter) only when the script fails, so an error
// can be read.
func setupCommand(dir string) *exec.Cmd {
	script := strings.ReplaceAll(filepath.Join(dir, "setup.ps1"), "'", "''")
	ps := "& '" + script + "' -Backend auto; if (-not $?) { Write-Host ''; Write-Host 'Setup failed.'; Read-Host 'Press Enter to close' ; exit 1 }"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", ps)
	cmd.Dir = dir
	return cmd
}

// runSetup runs setup.ps1 in its own visible console window and waits for it.
func runSetup(dir string) error {
	cmd := setupCommand(dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	return cmd.Run()
}
