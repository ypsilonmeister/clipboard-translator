package main

import "strings"

// The PowerShell command lines below run setup.ps1 and install.ps1, which set
// $ErrorActionPreference = 'Stop' and report failures with `throw`. A
// terminating error abandons the rest of a -Command string, so a trailing
// `; if (-not $?) { ... }` never runs and the console closes before the error
// can be read. The failure prompt therefore lives in a catch block.
//
// Success is judged by exceptions and by `$?` (false after a script that ends
// with a non-zero `exit`), not by $LASTEXITCODE: that variable also keeps the
// code of any native tool the script ran along the way, so it can be non-zero
// after a successful run.

// psFailurePrompt shows the error, waits for Enter so it can be read, and
// exits non-zero. header is the line printed above the error text.
func psFailurePrompt(header, prompt string) string {
	return "catch { Write-Host ''; Write-Host '" + header + "'; Write-Host ([string]$_) -ForegroundColor Red; " +
		"Read-Host '" + prompt + "'; exit 1 }; exit 0"
}

// setupPowerShell is the -Command text that runs the setup script at path.
func setupPowerShell(scriptPath string) string {
	quoted := strings.ReplaceAll(scriptPath, "'", "''")
	return "$ErrorActionPreference = 'Stop'; try { & '" + quoted + "' -Backend auto; " +
		"if (-not $?) { throw 'setup.ps1 reported a failure.' } } " +
		psFailurePrompt("Setup failed.", "Press Enter to close")
}

// installerPowerShell is the -Command text that downloads and runs install.ps1.
func installerPowerShell(url string) string {
	return "$ErrorActionPreference = 'Stop'; try { irm " + url + " | iex; " +
		"if (-not $?) { throw 'install.ps1 reported a failure.' } } " +
		psFailurePrompt("The update failed.", "Update failed. Press Enter to close")
}

// installerEnv is the environment for the installer: it targets this app's
// folder and installs exactly the release the user agreed to (install.ps1 reads
// CT_VERSION), not whatever is latest by the time the download starts. Our
// entries come last so they win over any CT_* values already in the
// environment.
func installerEnv(base []string, dir, tag string) []string {
	out := make([]string, 0, len(base)+2)
	out = append(out, base...)
	return append(out, "CT_INSTALL_DIR="+dir, "CT_VERSION="+tag)
}
