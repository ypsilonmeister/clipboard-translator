package main

import "runtime"

func serverBinaryName() string {
	if runtime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

func setupScriptName() string {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		return "setup.sh"
	}
	return "setup.ps1"
}

func pasteShortcut() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+V"
	}
	return "Ctrl+V"
}
