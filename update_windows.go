//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// installRecord is where install.ps1 stores the folder it installed to, for
// "Apps & features".
var installRecord = autostartStore{key: `Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardTranslator`, name: "InstallLocation"}

func recordedInstallLocation() string {
	v, _ := installRecord.Get()
	return v
}

// runInstaller starts install.ps1 in its own console window, aimed at this
// app's folder. The script stops this app (through its STOP file), replaces the
// files and starts the new version; this process only starts it and carries on.
func runInstaller(dir string) error {
	script := "irm " + installScriptURL + " | iex; if (-not $?) { Write-Host ''; Read-Host 'Update failed. Press Enter to close' }"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), "CT_INSTALL_DIR="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
