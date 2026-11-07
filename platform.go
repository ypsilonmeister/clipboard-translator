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

// maxWindowsHotkeys is the size of the Windows hotkey id pool (hotkey_windows.go).
const maxWindowsHotkeys = 8

// hotkeyLimit is how many hotkeys one run can register. Windows keeps one
// RegisterHotKey id per hotkey; the X11 and macOS code holds a single grab, so
// more than one would silently replace the first.
func hotkeyLimit() int {
	if runtime.GOOS == "windows" {
		return maxWindowsHotkeys
	}
	return 1
}

func pasteShortcut() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+V"
	}
	return "Ctrl+V"
}
