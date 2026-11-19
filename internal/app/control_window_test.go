package app

import (
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// #133 P1-8: the control window shows the same per-capability lines as the
// menu and Settings, and they follow each state change.

type capTray struct {
	*fakeTray
	calls [][]string
}

func (c *capTray) SetCapabilities(lines []string) { c.calls = append(c.calls, lines) }

func TestControlWindowCapabilityLinesFollowState(t *testing.T) {
	a, ft, base, _, _ := uxApp(t)
	ct := &capTray{fakeTray: ft}
	a.tray = ct
	cb := &asyncDesktopClipboard{testClipboard: base, state: partialStatus()}
	a.cb = cb
	a.tickDesktop()
	if len(ct.calls) != 1 {
		t.Fatalf("calls = %d", len(ct.calls))
	}
	want := []string{
		"Hotkey: not provided by this desktop; use Translate clipboard",
		"Clipboard: ready",
		"Selection capture: needs attention (Retry Wayland permissions)",
	}
	if strings.Join(ct.calls[0], "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q", ct.calls[0])
	}
	// Same lines as the menu and Settings (one source of truth).
	if got := a.settingsState().DesktopCapabilities; strings.Join(got, "|") != strings.Join(ct.calls[0], "|") {
		t.Fatalf("settings %q differ from control window %q", got, ct.calls[0])
	}
	// No state change: no redundant GTK update.
	a.tickDesktop()
	if len(ct.calls) != 1 {
		t.Fatalf("unchanged state pushed again: %d calls", len(ct.calls))
	}
	// A capability recovering updates the window.
	cb.state.Capture.State = platform.CapabilityAvailable
	a.tickDesktop()
	if len(ct.calls) != 2 || ct.calls[1][2] != "Selection capture: ready" {
		t.Fatalf("calls = %q", ct.calls)
	}
	// A backend that evaluates nothing clears the window's lines.
	cb.state = platform.DesktopStatus{Wayland: true, Message: "connecting"}
	a.tickDesktop()
	if n := len(ct.calls); n != 3 || len(ct.calls[n-1]) != 0 {
		t.Fatalf("calls = %q", ct.calls)
	}
}

func TestCapabilityLinesDistinguishEveryState(t *testing.T) {
	cases := map[platform.CapabilityState]string{
		platform.CapabilityAvailable: "Hotkey: ready",
		platform.CapabilityPending:   "Hotkey: waiting for permission",
		platform.CapabilityWaiting:   "Hotkey: reconnecting (unlock the screen if it is locked)",
		platform.CapabilityRefused:   "Hotkey: needs attention (Retry Wayland permissions)",
		platform.CapabilityMissing:   "Hotkey: not provided by this desktop; use Translate clipboard",
	}
	seen := map[string]bool{}
	for st, want := range cases {
		lines := capabilityLines(platform.DesktopStatus{Wayland: true, Hotkey: platform.CapabilityStatus{State: st}})
		if len(lines) != 1 || lines[0] != want {
			t.Fatalf("%v: %q", st, lines)
		}
		seen[lines[0]] = true
	}
	if len(seen) != len(cases) {
		t.Fatal("two states share a line")
	}
}
