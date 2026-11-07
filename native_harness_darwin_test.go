//go:build darwin && cgo && mactest

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AppKit must run on the process's main thread, including in a Go test binary.
func init() {
	if os.Getenv("CT_TEST_MACOS") == "1" {
		runtime.LockOSThread()
	}
}

func runPlatformSmokeTests() bool {
	if os.Getenv("CT_TEST_MACOS") != "1" {
		return false
	}
	if err := macNativeSmoke(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("PASS: macOS native AppKit, pasteboard, hotkey registration and menu commands")
	return true
}
func macNativeSmoke() error {
	if err := platformRuntimeError(); err != nil {
		return err
	}
	cb, err := newClipboard()
	if err != nil {
		return err
	}
	defer cb.Close()
	previous, err := cb.Read()
	if err != nil {
		return err
	}
	defer cb.Write(previous)
	const source = "こんにちは, macOS 😀"
	before := clipboardSequence()
	if err := cb.Write(source); err != nil {
		return err
	}
	if clipboardSequence() == before {
		return fmt.Errorf("pasteboard changeCount did not advance")
	}
	if got, err := cb.Read(); err != nil || got != source {
		return fmt.Errorf("pasteboard roundtrip: %q %v", got, err)
	}
	if changed, err := cb.ReplaceIfUnchanged("different text", "wrong"); changed || err != nil {
		return fmt.Errorf("stale pasteboard comparison: %v %v", changed, err)
	}
	if changed, err := cb.ReplaceIfUnchanged(source, "Hello"); !changed || err != nil {
		return fmt.Errorf("pasteboard replacement: %v %v", changed, err)
	}
	hk, err := newHotkey("Ctrl+Alt+Shift+J")
	if err != nil {
		return err
	}
	defer hk.Close()
	macTestHotkey(true)
	if pressed, err := hk.Pressed(); !pressed || err != nil {
		return fmt.Errorf("Carbon hotkey event was not delivered: %v", err)
	}
	macTestHotkey(false)
	if pressed, _ := hk.Pressed(); pressed {
		return fmt.Errorf("hotkey event fired twice")
	}
	tray := newTray("/tmp/clipboard-translator-macos-smoke.log")
	defer tray.Close()
	tray.SetStatus("Native smoke test")
	tray.SetMenu(func() []MenuItem { return []MenuItem{{ID: cmdCancel, Label: "Cancel"}} })
	macTestMenu(cmdCancel)
	pumpMessages()
	if id, ok := tray.NextCommand(); !ok || id != cmdCancel {
		return fmt.Errorf("menu command = %d %v", id, ok)
	}
	macTestMenu(2)
	pumpMessages()
	if !tray.QuitRequested() {
		return fmt.Errorf("menu Quit was not processed")
	}
	dir, err := os.MkdirTemp("", "clipboard-translator-macos-setup-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "setup.sh")
	if err := os.WriteFile(script, []byte("echo setup-smoke\n"), 0o600); err != nil {
		return err
	}
	if err := runSetup(dir); err != nil {
		return fmt.Errorf("setup progress window: %w", err)
	}
	if err := os.WriteFile(script, []byte("echo cancel-smoke\nsleep 30\n"), 0o600); err != nil {
		return err
	}
	macTestCancelSetupSoon()
	begin := time.Now()
	if err := runSetup(dir); err == nil || !strings.Contains(err.Error(), "cancelled") {
		return fmt.Errorf("setup cancellation: %v", err)
	}
	if time.Since(begin) > 5*time.Second {
		return fmt.Errorf("setup cancellation was unresponsive")
	}
	// No CGEvent injection, accessibility prompts or model downloads in CI.
	return nil
}
