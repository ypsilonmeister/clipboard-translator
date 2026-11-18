package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// #133 P1-6: the per-capability states reach the menu and the Settings page,
// and only a missing or refused portal clipboard sends the user to the control
// window.

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

// An `input: "clipboard"` hotkey reads the clipboard directly. When that read
// goes through the GTK fallback (clipboard missing or refused) it needs the
// focused control window, like the menu action; with the portal clipboard
// nothing is shown.
func TestClipboardInputHotkeyFocusesControlForGTKFallback(t *testing.T) {
	for _, manualClipboard := range []bool{false, true} {
		a := apiUnitApp(t)
		a.conf.Profiles[0].Input = config.InputClipboard
		state := platform.DesktopStatus{
			Wayland: true, ManualClipboard: manualClipboard,
			Hotkey:    platform.CapabilityStatus{State: platform.CapabilityAvailable},
			Clipboard: platform.CapabilityStatus{State: platform.CapabilityMissing},
		}
		if !manualClipboard {
			state.Clipboard.State = platform.CapabilityAvailable
		}
		cb := &manualFocusClipboard{asyncDesktopClipboard: &asyncDesktopClipboard{testClipboard: &testClipboard{}, readPending: true, state: state}}
		a.cb = cb
		a.startHotkeyTranslation(0, false)
		want := 0
		if manualClipboard {
			want = 1
		}
		if cb.presented != want || a.pendingRead == nil {
			t.Fatalf("manualClipboard=%v: control window presentations=%d (want %d), pending read=%v", manualClipboard, cb.presented, want, a.pendingRead != nil)
		}
	}
}

// A settled partial set (hotkey works, clipboard portal missing) is not
// waiting for anything: the menu header must not say so.
func TestDesktopMenuHeaderForPermanentPartialSet(t *testing.T) {
	a, _, base, _, _ := uxApp(t)
	missing := platform.CapabilityStatus{State: platform.CapabilityMissing, Message: "missing"}
	state := platform.DesktopStatus{
		Wayland: true, ManualClipboard: true,
		Hotkey:    platform.CapabilityStatus{State: platform.CapabilityAvailable},
		Clipboard: missing, Capture: missing,
	}
	header := func() string {
		a.cb = &asyncDesktopClipboard{testClipboard: base, state: state}
		for _, item := range a.buildMenu() {
			if strings.HasPrefix(item.Label, "Wayland") {
				return item.Label
			}
		}
		t.Fatal("no Wayland header")
		return ""
	}
	if got := header(); got != "Wayland connected (some features unavailable)" {
		t.Fatalf("header %q", got)
	}
	state.Hotkey.State = platform.CapabilityPending
	if got := header(); got != "Wayland: waiting for desktop permissions" {
		t.Fatalf("a pending capability is still waiting: header %q", got)
	}
}
