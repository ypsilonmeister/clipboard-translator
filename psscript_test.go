package main

import (
	"strings"
	"testing"
)

func TestSetupPowerShellShape(t *testing.T) {
	ps := setupPowerShell(`C:\Users\O'Brien\CT\setup.ps1`)
	for _, want := range []string{
		`O''Brien`, // single quote doubled inside the PowerShell string
		"-Backend auto",
		"$ErrorActionPreference = 'Stop'",
		"try {", "catch {", // a throw must reach the failure prompt
		"if (-not $?)", // a non-zero `exit` too
		"Read-Host 'Press Enter to close'",
		"exit 1",
	} {
		if !strings.Contains(ps, want) {
			t.Errorf("setup command lacks %q:\n%s", want, ps)
		}
	}
	// A terminating error skips whatever follows the try block, so the check
	// and the prompt must sit inside try/catch.
	if !(strings.Index(ps, "try {") < strings.Index(ps, "-Backend auto") && strings.Index(ps, "-Backend auto") < strings.Index(ps, "catch {")) {
		t.Errorf("the script call must be inside the try block:\n%s", ps)
	}
	if strings.Index(ps, "catch {") > strings.Index(ps, "Read-Host") {
		t.Errorf("Read-Host must be inside the catch block:\n%s", ps)
	}
}

func TestInstallerPowerShellShape(t *testing.T) {
	url, err := installScriptURL("v0.7.0")
	if err != nil {
		t.Fatal(err)
	}
	ps := installerPowerShell(url)
	for _, want := range []string{
		"irm https://raw.githubusercontent.com/ypsilonmeister/clipboard-translator/v0.7.0/install.ps1 | iex",
		"$ErrorActionPreference = 'Stop'",
		"try {", "catch {",
		"Update failed. Press Enter to close",
		"exit 1",
	} {
		if !strings.Contains(ps, want) {
			t.Errorf("installer command lacks %q:\n%s", want, ps)
		}
	}
	if strings.Index(ps, "catch {") > strings.Index(ps, "Read-Host") {
		t.Errorf("Read-Host must be inside the catch block:\n%s", ps)
	}
}

func TestInstallerEnvPinsTheConfirmedVersion(t *testing.T) {
	base := []string{"PATH=x", "CT_VERSION=v0.1.0", "CT_INSTALL_DIR=D:\\old"}
	env := installerEnv(base, `C:\Apps\CT`, "v0.7.0")
	// Later entries win (os/exec keeps the last duplicate), so check the order.
	last := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		last[k] = v
	}
	if last["CT_VERSION"] != "v0.7.0" || last["CT_INSTALL_DIR"] != `C:\Apps\CT` || last["PATH"] != "x" {
		t.Fatalf("env = %v", last)
	}
	if len(base) != 3 {
		t.Fatal("the caller's slice must not be modified")
	}
}
