//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// hideConsole stops a console program (llama-server.exe) from opening its own
// console window when started by this GUI/tray process. Its output is already
// redirected to server.log, so nothing is lost.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
