package main

import (
	"os"
	"path/filepath"
	"runtime"
)

// vcRuntimeAvailable reports whether the Visual C++ runtime that llama.cpp's
// Windows builds import (vcruntime140_1.dll, added after the first VC++ 2015
// redistributables) can be loaded by the llama-server in serverDir: Windows
// looks next to the exe first, then in System32. It is true on other systems.
// It is a variable so tests do not depend on the machine they run on.
var vcRuntimeAvailable = func(serverDir string) bool {
	if runtime.GOOS != "windows" {
		return true
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	for _, dir := range []string{serverDir, filepath.Join(root, "System32")} {
		if _, err := os.Stat(filepath.Join(dir, "vcruntime140_1.dll")); err == nil {
			return true
		}
	}
	return false
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

const maxHotkeys = 8

// maxWindowsHotkeys is the size of the Windows hotkey id pool (hotkey_windows.go).
const maxWindowsHotkeys = maxHotkeys

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

// completionNotification returns the generic title and body for the
// completion notification. It deliberately omits the source text,
// translation, and language names so sensitive text is not leaked on
// a shared screen.
func completionNotification() (string, string) {
	return "Translation ready", "Translation ready in the clipboard. Press " + pasteShortcut() + " to paste."
}
