package platform

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
	ps := installerPowerShell("https://raw.githubusercontent.com/ypsilonmeister/clipboard-translator/v0.7.0/install.ps1")
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

func TestCleanPowerShellEnv(t *testing.T) {
	cases := []struct {
		name string
		base []string
		want []string
	}{
		{
			name: "removes PSModulePath case-insensitively",
			base: []string{
				"PATH=C:\\bin",
				"PSModulePath=C:\\pwsh\\modules",
				"psmodulepath=C:\\other\\modules",
				"PSMODULEPATH=C:\\more\\modules",
				"=C:=C:\\dir",
				"FOO=BAR",
			},
			want: []string{"PATH=C:\\bin", "=C:=C:\\dir", "FOO=BAR"},
		},
		{
			name: "keeps entries when PSModulePath not present",
			base: []string{"A=1", "B=2"},
			want: []string{"A=1", "B=2"},
		},
		{
			name: "empty slice",
			base: []string{},
			want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origLen := len(tc.base)
			got := CleanPowerShellEnv(tc.base)
			if len(tc.base) != origLen {
				t.Fatal("cleanPowerShellEnv modified the input slice")
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestInstallerEnvPinsTheConfirmedVersion(t *testing.T) {
	base := []string{"PATH=x", "PSModulePath=C:\\pwsh\\modules", "CT_VERSION=v0.1.0", "CT_INSTALL_DIR=D:\\old"}
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
	if _, ok := last["PSModulePath"]; ok {
		t.Error("installerEnv must scrub PSModulePath")
	}
	if len(base) != 4 {
		t.Fatal("the caller's slice must not be modified")
	}
}
