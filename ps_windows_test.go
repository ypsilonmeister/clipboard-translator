//go:build windows

package main

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runPS runs cmd with Enter already "typed" and returns its exit code and the
// console output.
func runPS(t *testing.T, cmd *exec.Cmd) (int, string) {
	t.Helper()
	var out bytes.Buffer
	cmd.Stdin = strings.NewReader("\r\n")
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = nil // no new console in tests
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, out.String()
	case errors.As(err, &ee):
		return ee.ExitCode(), out.String()
	}
	t.Fatalf("running PowerShell: %v", err)
	return -1, ""
}

func TestSetupCommandReachesFailurePrompt(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("powershell.exe not found")
	}
	cases := []struct {
		name, script string
		wantExit     int
		wantPrompt   bool
	}{
		{"throw", "param($Backend)\n$ErrorActionPreference = 'Stop'\nthrow 'boom from setup'", 1, true},
		{"non-zero exit", "param($Backend)\nexit 3", 1, true},
		// A native tool that failed along the way must not fail a script that went on.
		{"stale native exit code", "param($Backend)\ncmd.exe /c exit 1\n$null = 0", 0, false},
		{"success", "param($Backend)\nWrite-Output 'fine'", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "setup.ps1"), []byte(c.script), 0o644); err != nil {
				t.Fatal(err)
			}
			code, out := runPS(t, setupCommand(dir))
			if code != c.wantExit {
				t.Errorf("exit code = %d, want %d; output:\n%s", code, c.wantExit, out)
			}
			// Read-Host's prompt goes to the host, not into the captured output, so
			// the header printed just before it shows the failure branch ran.
			if got := strings.Contains(out, "Setup failed."); got != c.wantPrompt {
				t.Errorf("failure branch reached = %v, want %v; output:\n%s", got, c.wantPrompt, out)
			}
			if c.name == "throw" && !strings.Contains(out, "boom from setup") {
				t.Errorf("the error text must be shown; output:\n%s", out)
			}
		})
	}
}

func TestInstallerCommandReachesFailurePrompt(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("powershell.exe not found")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "install.ps1")
	if err := os.WriteFile(script, []byte("$ErrorActionPreference = 'Stop'\nthrow 'boom from install'"), 0o644); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(script)}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", installerPowerShell(u.String()))
	code, out := runPS(t, cmd)
	if code != 1 || !strings.Contains(out, "The update failed.") || !strings.Contains(out, "boom from install") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestInstallerCommandEnvironment(t *testing.T) {
	cmd := installerCommand(`C:\Apps\CT`, "v0.7.0")
	last := map[string]string{}
	for _, e := range cmd.Env {
		k, v, _ := strings.Cut(e, "=")
		last[strings.ToUpper(k)] = v
	}
	if last["CT_VERSION"] != "v0.7.0" || last["CT_INSTALL_DIR"] != `C:\Apps\CT` {
		t.Fatalf("CT_VERSION / CT_INSTALL_DIR not passed: %v", last)
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNewConsole == 0 {
		t.Fatal("the installer needs its own console window")
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "catch {") || !strings.Contains(joined, installScriptURL) {
		t.Fatalf("unexpected command: %s", joined)
	}
}
