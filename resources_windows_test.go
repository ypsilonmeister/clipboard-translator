//go:build windows

package main

import (
	"runtime"
	"syscall"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// resource_windows_amd64.syso is linked into this package, so the tests that
// check what it carries live here, not next to the tray code.

// The icon embedded via resource_windows_amd64.syso must be found; otherwise the
// tray would silently fall back to the generic application icon.
func TestAppIconIsEmbedded(t *testing.T) {
	if !platform.AppIconIsEmbedded() {
		t.Fatal("the embedded icon resource is missing or no icon could be loaded (run go run ./tools/mksyso)")
	}
}

// The embedded manifest must make the process per-monitor-v2 DPI aware;
// otherwise Windows bitmap-stretches the tray icon and menu on scaled displays.
func TestProcessIsPerMonitorDPIAware(t *testing.T) {
	user32 := syscall.NewLazyDLL("user32.dll")
	getCtx := user32.NewProc("GetThreadDpiAwarenessContext")
	equal := user32.NewProc("AreDpiAwarenessContextsEqual")
	if getCtx.Find() != nil || equal.Find() != nil {
		t.Skip("DPI awareness context APIs need Windows 10 1607 or later")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx, _, _ := getCtx.Call()
	const perMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = (HANDLE)-4
	if ok, _, _ := equal.Call(ctx, perMonitorV2); ok == 0 {
		t.Fatalf("DPI awareness context %#x is not per-monitor v2: manifest missing (run go run ./tools/mksyso)", ctx)
	}
}
