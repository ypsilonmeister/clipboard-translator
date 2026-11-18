package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// #133 P1-6: the per-capability states reach the menu and the Settings page,
// and only a missing portal clipboard sends the user to the control window.

func partialStatus() platform.DesktopStatus {
	return platform.DesktopStatus{
		Wayland: true, Manual: true, PortalCapabilitiesMissing: true,
		Message:   "No translation hotkey",
		Hotkey:    platform.CapabilityStatus{State: platform.CapabilityMissing},
		Clipboard: platform.CapabilityStatus{State: platform.CapabilityAvailable},
		Capture:   platform.CapabilityStatus{State: platform.CapabilityRefused},
	}
}

func TestDesktopCapabilityLinesInMenuAndSettings(t *testing.T) {
	a, _, base, _, _ := uxApp(t)
	a.cb = &asyncDesktopClipboard{testClipboard: base, state: partialStatus()}
	want := []string{
		"Hotkey: not provided by this desktop; use Translate clipboard",
		"Clipboard: ready",
		"Selection capture: needs attention (Retry Wayland permissions)",
	}
	var labels []string
	for _, item := range a.buildMenu() {
		labels = append(labels, item.Label)
	}
	joined := strings.Join(labels, "\n")
	if !strings.Contains(joined, "Manual mode (hotkey unavailable)\n"+strings.Join(want, "\n")+"\nRetry Wayland permissions") {
		t.Fatalf("menu labels:\n%s", joined)
	}
	if got := a.settingsState().DesktopCapabilities; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("settings lines = %q", got)
	}
	// Backends that do not evaluate capabilities show no lines.
	a.cb = &asyncDesktopClipboard{testClipboard: base, state: platform.DesktopStatus{Wayland: true}}
	if lines := a.settingsState().DesktopCapabilities; len(lines) != 0 {
		t.Fatalf("unexpected lines %q", lines)
	}
}

// A state change of one capability refreshes the UI even if the lead message
// stays the same.
func TestDesktopCapabilityChangeRefreshesStatus(t *testing.T) {
	a, _, base, _, _ := uxApp(t)
	cb := &asyncDesktopClipboard{testClipboard: base, state: partialStatus()}
	a.cb = cb
	a.tickDesktop()
	before := a.desktopStamp
	cb.state.Capture.State = platform.CapabilityAvailable
	a.tickDesktop()
	if a.desktopStamp == before {
		t.Fatal("capability change did not refresh the desktop status")
	}
}

// With only the hotkey missing the menu action reads the portal clipboard: a
// timeout must not tell the user to focus the control window.
func TestManualActionTimeoutWordingFollowsClipboardCapability(t *testing.T) {
	for _, manualClipboard := range []bool{false, true} {
		a := apiUnitApp(t)
		state := partialStatus()
		state.ManualClipboard = manualClipboard
		c := &manualFocusClipboard{asyncDesktopClipboard: &asyncDesktopClipboard{testClipboard: &testClipboard{}, readPending: true, state: state}}
		a.cb = c
		a.handleCommand(cmdTranslateClipboard)
		if a.pendingRead == nil {
			t.Fatal("read not pending")
		}
		a.pendingRead.deadline = time.Time{}
		a.tickClipboard()
		notes := a.tray.(*fakeTray).notes
		if len(notes) == 0 {
			t.Fatal("no notification")
		}
		if got := strings.Contains(notes[len(notes)-1], "focus the app control window"); got != manualClipboard {
			t.Fatalf("manualClipboard=%v note=%q", manualClipboard, notes[len(notes)-1])
		}
	}
}
