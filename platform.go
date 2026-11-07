package main

import "runtime"

func serverBinaryName() string {
	if runtime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

func setupScriptName() string {
	if runtime.GOOS == "linux" {
		return "setup.sh"
	}
	return "setup.ps1"
}
