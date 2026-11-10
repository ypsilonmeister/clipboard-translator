//go:build windows

package platform

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSetupCommandQuotesThePath(t *testing.T) {
	cmd := setupCommand(`C:\Users\O'Brien\CT`)
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, `O''Brien`) {
		t.Fatalf("single quote in the path must be doubled inside the PowerShell string: %s", joined)
	}
	if !strings.Contains(joined, "-ExecutionPolicy Bypass") || !strings.Contains(joined, "setup.ps1") {
		t.Fatalf("unexpected command: %s", joined)
	}
}

func TestHideConsoleFlags(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	HideConsole(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("SysProcAttr = %+v, want HideWindow and CREATE_NO_WINDOW", cmd.SysProcAttr)
	}
}
