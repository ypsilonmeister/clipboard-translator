//go:build windows

package main

import (
	"os/exec"
	"testing"
)

func TestHideConsoleFlags(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	hideConsole(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("SysProcAttr = %+v, want HideWindow and CREATE_NO_WINDOW", cmd.SysProcAttr)
	}
}
