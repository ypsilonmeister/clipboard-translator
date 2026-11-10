//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// The release exe is a GUI-subsystem binary (-H=windowsgui), so it starts
// with no console and its standard handles go nowhere. The setup
// subcommand is a console tool: attach to the parent process's console
// (PowerShell, cmd, Setup.cmd) so its progress and errors can be read.
var (
	setupKernel32         = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole     = setupKernel32.NewProc("AttachConsole")
	procCreateConsoleFile = setupKernel32.NewProc("CreateFileW")
)

const attachParentProcess = ^uintptr(0) // (DWORD)-1

func attachParentConsole() {
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r == 0 {
		return // no parent console (for example a double-click): nowhere to print
	}
	if f := openConsoleFile("CONOUT$"); f != nil {
		os.Stdout = f
		os.Stderr = f
	}
	if f := openConsoleFile("CONIN$"); f != nil {
		os.Stdin = f
	}
}

func openConsoleFile(name string) *os.File {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil
	}
	h, _, _ := procCreateConsoleFile.Call(
		uintptr(unsafe.Pointer(p)),
		0xC0000000, // GENERIC_READ | GENERIC_WRITE
		3,          // FILE_SHARE_READ | FILE_SHARE_WRITE
		0,
		3, // OPEN_EXISTING
		0, 0)
	if h == uintptr(syscall.InvalidHandle) {
		return nil
	}
	return os.NewFile(h, name)
}
