package main

import (
	"os"
	"path/filepath"
	"runtime"
)

// vcRuntimeInstalled reports whether the Visual C++ runtime that llama.cpp's
// Windows builds import (vcruntime140_1.dll, added after the first VC++ 2015
// redistributables) is in System32. It is true on other systems. It only
// decides which message to show: a DLL next to llama-server would also work, so
// a missing file never blocks a start.
func vcRuntimeInstalled() bool {
	if runtime.GOOS != "windows" {
		return true
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	_, err := os.Stat(filepath.Join(root, "System32", "vcruntime140_1.dll"))
	return err == nil
}

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
