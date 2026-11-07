//go:build windows

package main

import (
	"log"
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

// installerCommand builds the PowerShell invocation of install.ps1, aimed at
// this app's folder and at release tag.
func installerCommand(dir, tag string) *exec.Cmd {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", installerPowerShell(installScriptURL))
	cmd.Env = installerEnv(os.Environ(), dir, tag)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	return cmd
}

// runInstaller starts install.ps1 in its own console window, aimed at this
// app's folder and at release tag. The script stops this app (through its STOP
// file), replaces the files and starts the new version; this process only
// starts it and carries on. A failure keeps that window open (see
// installerPowerShell); the outcome is also logged.
func runInstaller(dir, tag string) error {
	cmd := installerCommand(dir, tag)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("the updater to %s ended with an error: %v", tag, err)
		}
	}()
	return nil
}
