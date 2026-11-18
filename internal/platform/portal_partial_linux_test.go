package platform

import (
	"errors"
	"strings"
	"testing"
)

// The tests in this file pin #133 P1-6: hotkey, clipboard and capture are
// independent capabilities (design memo §5.3). A partial set degrades only the
// capability that is missing, refused or waiting; the others keep working and
// DesktopStatus reports each one separately.

const partialRemoteSession = "/org/freedesktop/portal/desktop/session/test/remote"

// partialDesktop answers the three capability probes. Missing interfaces
// answer version 0, as the GIO probe does for an absent interface.
func partialDesktop(t *testing.T, shortcuts, remoteKeyboard, clipboard bool) (*portalDesktop, *fakeDesktopWire, *manualTestClipboard, *int) {
	t.Helper()
	d, w := desktopFixture(t)
	gtk := &manualTestClipboard{text: "gtk text"}
	d.manualClipboard = gtk
	shown := new(int)
	d.showManual = func() { *shown++ }
	version := func(ok bool) uint32 {
		if ok {
			return 1
		}
		return 0
	}
	devices := uint32(0)
	if remoteKeyboard {
		devices = 1
	}
	w.events = append(w.events,
		portalEvent{kind: "capability", id: portalShortcuts, code: version(shortcuts)},
		portalEvent{kind: "capability", id: portalRemoteDesktop, code: 1, devices: devices},
		portalEvent{kind: "capability", id: portalClipboardInterface, code: version(clipboard)})
	d.Step()
	return d, w, gtk, shown
}

func requestsFor(w *fakeDesktopWire, iface string) int {
	n := 0
	for _, q := range w.requests {
		if q.iface == iface {
			n++
		}
	}
	return n
}

// grantRemote answers the remote feature's CreateSession, SelectDevices,
// RequestClipboard and Start; devices/clipboard are what Start grants.
func grantRemote(t *testing.T, d *portalDesktop, w *fakeDesktopWire, devices uint32, clipboard bool) {
	t.Helper()
	if d.remote.operation != "create" {
		t.Fatalf("remote must be creating its session, operation=%q phase=%s", d.remote.operation, d.remote.phase)
	}
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, session: partialRemoteSession})
	d.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending})
	d.Step()
	w.events = append(w.events, portalEvent{kind: "method", token: d.remote.pending})
	d.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, devices: devices, clipboard: clipboard, restore: "granted-token"})
	d.Step()
}

// desktopPortalRead reads through portalDesktop (the path the menu action uses) and
// answers the portal's SelectionRead with text.
func desktopPortalRead(t *testing.T, d *portalDesktop, w *fakeDesktopWire, text string) string {
	t.Helper()
	w.events = append(w.events, portalEvent{kind: "selection-owner", session: d.remote.session, mime: portalTextMIME})
	if _, err := d.Read(); !errors.Is(err, errPortalPending) {
		t.Fatalf("portal read must be asynchronous, got %v", err)
	}
	w.events = append(w.events, portalEvent{kind: "read", token: d.remote.clip.read.token, data: []byte(text)})
	got, err := d.Read()
	if err != nil {
		t.Fatalf("portal read failed: %v", err)
	}
	return got
}

func wantCapabilities(t *testing.T, s DesktopStatus, hotkey, clipboard, capture CapabilityState) {
	t.Helper()
	if s.Hotkey.State != hotkey || s.Clipboard.State != clipboard || s.Capture.State != capture {
		t.Fatalf("capabilities hotkey=%s clipboard=%s capture=%s, want %s/%s/%s (status %+v)",
			s.Hotkey.State, s.Clipboard.State, s.Capture.State, hotkey, clipboard, capture, s)
	}
}

func keyCalls(w *fakeDesktopWire) int {
	n := 0
	for _, c := range w.calls {
		if c.kind == "key" {
			n++
		}
	}
	return n
}

// runCaptureTask steps a capture task until it finishes (bounded).
func runCaptureTask(t *testing.T, task CaptureTask) (string, bool, error) {
	t.Helper()
	for i := 0; i < 100; i++ {
		done, text, fresh, err := task.Step()
		if done {
			return text, fresh, err
		}
	}
	t.Fatal("capture task did not finish")
	return "", false, nil
}

// GNOME 46/47: RemoteDesktop and Clipboard exist, GlobalShortcuts does not.
// The menu action must use the portal clipboard, not the focus-bound GTK one.
func TestPortalPartialNoHotkeyUsesPortalClipboard(t *testing.T) {
	d, w, gtk, shown := partialDesktop(t, false, true, true)
	defer d.Close()
	h, err := d.hotkeys.NewHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatal(err)
	}
	if w.registered != waylandAppID || d.manual {
		t.Fatalf("a missing hotkey alone must not switch to full manual mode: registered=%q manual=%v", w.registered, d.manual)
	}
	if *shown != 1 {
		t.Fatalf("the control window (the only trigger) must be shown once, shown=%d", *shown)
	}
	w.events = append(w.events, portalEvent{kind: "registered"})
	d.Step()
	if n := requestsFor(w, portalShortcuts); n != 0 {
		t.Fatalf("GlobalShortcuts session requested without the portal: %d", n)
	}
	// Remote authorization does not wait for shortcuts that cannot exist.
	s := d.DesktopStatus()
	wantCapabilities(t, s, CapabilityMissing, CapabilityPending, CapabilityPending)
	if !s.Manual || s.ManualClipboard || s.Ready || s.Retry || !s.PortalCapabilitiesMissing {
		t.Fatalf("status %+v", s)
	}
	grantRemote(t, d, w, 1, true)
	s = d.DesktopStatus()
	wantCapabilities(t, s, CapabilityMissing, CapabilityAvailable, CapabilityAvailable)
	if !s.Manual || s.ManualClipboard || s.Ready || s.Retry {
		t.Fatalf("status %+v", s)
	}
	if !strings.Contains(s.Message, "GlobalShortcuts") || !strings.Contains(s.Message, "Translate clipboard") {
		t.Fatalf("message must say which capability is missing and what to do: %q", s.Message)
	}
	if s.Hotkey.Retryable() {
		t.Fatal("Retry cannot add a missing portal")
	}
	// The menu action reads and writes the portal clipboard without focusing
	// the control window.
	d.PresentControl()
	if *shown != 1 {
		t.Fatal("portal clipboard access must not present the control window")
	}
	if got := desktopPortalRead(t, d, w, "portal text"); got != "portal text" {
		t.Fatalf("read %q, want the portal clipboard", got)
	}
	if gtk.text != "gtk text" {
		t.Fatal("the GTK fallback clipboard was touched")
	}
	if err := d.Write("translated"); err != nil && !errors.Is(err, errPortalPending) {
		t.Fatalf("portal write: %v", err)
	}
	set := false
	for _, c := range w.calls {
		set = set || c.kind == "set"
	}
	if !set || gtk.text != "gtk text" {
		t.Fatal("write must go through the portal SetSelection")
	}
	if fired, err := h.Pressed(); fired || err != nil {
		t.Fatalf("a hotkey without the portal must stay silent: fired=%v err=%v", fired, err)
	}
	requests := len(w.requests)
	d.RetryDesktop()
	d.Step()
	if len(w.requests) != requests || requestsFor(w, portalShortcuts) != 0 {
		t.Fatal("Retry must not touch working or missing capabilities")
	}
}

// GlobalShortcuts exists but the Clipboard portal does not: the hotkey still
// binds and fires; the clipboard goes through the focused control window and
// the hotkey translates the clipboard text (no selection capture).
func TestPortalPartialNoClipboardKeepsHotkey(t *testing.T) {
	for _, tc := range []struct {
		name                string
		keyboard, clipboard bool
	}{
		{"clipboard-portal-missing", true, false},
		{"remote-keyboard-missing", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, w, gtk, shown := partialDesktop(t, true, tc.keyboard, tc.clipboard)
			defer d.Close()
			h, err := d.hotkeys.NewHotkey("Ctrl+Alt+E")
			if err != nil {
				t.Fatal(err)
			}
			if w.registered != waylandAppID || d.manual || *shown != 0 {
				t.Fatalf("registered=%q manual=%v shown=%d", w.registered, d.manual, *shown)
			}
			desktopSessions(d, w)
			session := d.hotkeys.session
			w.events = append(w.events, portalEvent{kind: "response", token: d.hotkeys.pending, bindings: []portalBinding{{id: h.id, trigger: "Ctrl+Alt+E"}}})
			d.Step()
			if !h.active || h.actual != "Ctrl+Alt+E" {
				t.Fatal("hotkey must bind without the clipboard portal")
			}
			if n := requestsFor(w, portalRemoteDesktop); n != 0 || d.remote.phase != "idle" {
				t.Fatalf("RemoteDesktop must not be requested without a usable clipboard: requests=%d phase=%s", n, d.remote.phase)
			}
			s := d.DesktopStatus()
			wantCapabilities(t, s, CapabilityAvailable, CapabilityMissing, CapabilityMissing)
			if s.Manual || !s.ManualClipboard || s.Ready || s.Retry || !s.PortalCapabilitiesMissing {
				t.Fatalf("status %+v", s)
			}
			if !strings.Contains(s.Message, "control window") || !strings.Contains(s.Message, "already on the clipboard") {
				t.Fatalf("message must name the clipboard and capture fallbacks: %q", s.Message)
			}
			w.events = append(w.events, portalEvent{kind: "activated", session: session, id: h.id})
			if fired, err := h.Pressed(); !fired || err != nil {
				t.Fatalf("hotkey must fire: fired=%v err=%v", fired, err)
			}
			// The hotkey translates the clipboard through the control window and
			// says so (FallbackNoCopy); no keys are sent.
			task := d.beginCapture()
			if task == nil {
				t.Fatal("no capture task")
			}
			text, fresh, err := runCaptureTask(t, task)
			if err != nil || text != "gtk text" || fresh || task.Fallback() != FallbackNoCopy {
				t.Fatalf("capture=%q fresh=%v err=%v fallback=%v", text, fresh, err, task.Fallback())
			}
			if *shown != 1 || keyCalls(w) != 0 {
				t.Fatalf("manual capture must focus the control window and send no keys: shown=%d keys=%d", *shown, keyCalls(w))
			}
			if ok, err := d.ReplaceIfUnchanged("gtk text", "翻訳"); !ok || err != nil || gtk.text != "翻訳" {
				t.Fatalf("replace=%v err=%v text=%q", ok, err, gtk.text)
			}
			d.RetryDesktop()
			d.Step()
			if requestsFor(w, portalRemoteDesktop) != 0 || w.binds != 1 || d.hotkeys.session != session {
				t.Fatal("Retry must not disturb the hotkey or request a missing portal")
			}
		})
	}
}

// Hotkey bound, but the user refused (or the portal failed) the clipboard
// session: clipboard and capture are refused, the hotkey keeps working, and the
// GTK fallback is not used (the portal exists; Retry asks again).
func TestPortalPartialClipboardRefusedKeepsHotkey(t *testing.T) {
	d, w, h := boundDesktop(t)
	defer d.Close()
	gtk := &manualTestClipboard{text: "gtk text"}
	d.manualClipboard = gtk
	shortcuts := d.hotkeys.session
	requests := len(w.requests)
	shield(&w.fakeRemoteWire, false)
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, code: 1})
	d.Step()
	s := d.DesktopStatus()
	wantCapabilities(t, s, CapabilityAvailable, CapabilityRefused, CapabilityRefused)
	if !s.Retry || s.Ready || s.Manual || s.ManualClipboard || s.Message != s.Clipboard.Message || s.Capture.Message != "" {
		t.Fatalf("status %+v", s)
	}
	assertHotkeyStillWorks(t, d, w, h, shortcuts, requests)
	if _, err := d.Read(); err == nil || gtk.text != "gtk text" {
		t.Fatalf("a refused portal clipboard must report its error, not fall back silently: err=%v", err)
	}
	d.RetryDesktop()
	d.Step()
	grantRemote(t, d, w, 1, true)
	s = d.DesktopStatus()
	wantCapabilities(t, s, CapabilityAvailable, CapabilityAvailable, CapabilityAvailable)
	if !s.Ready || s.Retry || w.binds != 1 || d.hotkeys.session != shortcuts {
		t.Fatalf("Retry must repair only the clipboard: %+v binds=%d", s, w.binds)
	}
}

// The screen shield makes the clipboard wait; the hotkey keeps working and
// nothing offers Retry.
func TestPortalPartialClipboardWaitingKeepsHotkey(t *testing.T) {
	d, w, h := boundDesktop(t)
	defer d.Close()
	shortcuts := d.hotkeys.session
	requests := len(w.requests)
	shield(&w.fakeRemoteWire, true)
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, code: 2})
	d.Step()
	s := d.DesktopStatus()
	wantCapabilities(t, s, CapabilityAvailable, CapabilityWaiting, CapabilityWaiting)
	if s.Retry || s.Ready || !strings.Contains(s.Message, "unlocked") {
		t.Fatalf("status %+v", s)
	}
	assertHotkeyStillWorks(t, d, w, h, shortcuts, requests)
}

// Start granted the clipboard but not the keyboard: capture is unavailable,
// the clipboard and hotkey keep working, and the hotkey translates the
// clipboard text through the portal and says so.
func TestPortalPartialCaptureWithoutKeyboard(t *testing.T) {
	d, w, h := boundDesktop(t)
	defer d.Close()
	gtk := &manualTestClipboard{text: "gtk text"}
	d.manualClipboard = gtk
	shown := 0
	d.showManual = func() { shown++ }
	shortcuts := d.hotkeys.session
	shield(&w.fakeRemoteWire, false)
	grantRemote(t, d, w, 0, true)
	s := d.DesktopStatus()
	wantCapabilities(t, s, CapabilityAvailable, CapabilityAvailable, CapabilityRefused)
	if s.Ready || !s.Retry || s.Manual || s.ManualClipboard || s.Message != s.Capture.Message || !strings.Contains(s.Message, "keyboard") {
		t.Fatalf("status %+v", s)
	}
	if token, _ := d.tokens.Load(); token != "granted-token" {
		t.Fatalf("a clipboard-only grant is still a grant; token=%q", token)
	}
	if got := desktopPortalRead(t, d, w, "portal text"); got != "portal text" {
		t.Fatalf("read %q", got)
	}
	w.events = append(w.events, portalEvent{kind: "activated", session: shortcuts, id: h.id})
	if fired, err := h.Pressed(); !fired || err != nil {
		t.Fatalf("hotkey must fire: fired=%v err=%v", fired, err)
	}
	task := d.beginCapture()
	text, fresh, err := runCaptureTask(t, task)
	if err != nil || text != "portal text" || fresh || task.Fallback() != FallbackNoCopy {
		t.Fatalf("capture=%q fresh=%v err=%v fallback=%v", text, fresh, err, task.Fallback())
	}
	if keyCalls(w) != 0 || shown != 0 || gtk.text != "gtk text" {
		t.Fatalf("no keys, no control window and no GTK clipboard: keys=%d shown=%d", keyCalls(w), shown)
	}
	if k := d.remote.beginKeys('c'); !k.done || k.err == nil {
		t.Fatal("keys must be refused without a granted keyboard")
	}
	// Retry forgets the keyboard-less grant so the portal asks again, and
	// touches only the remote feature.
	remoteRequests := requestsFor(w, portalRemoteDesktop)
	d.RetryDesktop()
	d.Step()
	if requestsFor(w, portalRemoteDesktop) != remoteRequests+1 || w.binds != 1 || d.hotkeys.session != shortcuts {
		t.Fatalf("Retry must ask for the remote session again only: remote=%d binds=%d", requestsFor(w, portalRemoteDesktop), w.binds)
	}
	if token, _ := d.tokens.Load(); token != "" {
		t.Fatal("Retry must drop the token that restores the grant without keyboard")
	}
	grantRemote(t, d, w, 1, true)
	s = d.DesktopStatus()
	wantCapabilities(t, s, CapabilityAvailable, CapabilityAvailable, CapabilityAvailable)
	if !s.Ready || s.Retry {
		t.Fatalf("status %+v", s)
	}
}

// The shortcut dialog was refused: only the hotkey is refused; the clipboard
// is still authorized and the menu action works through the portal.
func TestPortalPartialHotkeyRefusedKeepsClipboard(t *testing.T) {
	d, w := desktopFixture(t)
	defer d.Close()
	h, err := d.hotkeys.NewHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatal(err)
	}
	desktopCapabilities(d, w)
	desktopSessions(d, w)
	w.events = append(w.events, portalEvent{kind: "response", token: d.hotkeys.pending, code: 1})
	d.Step()
	shield(&w.fakeRemoteWire, false)
	grantRemote(t, d, w, 1, true)
	s := d.DesktopStatus()
	wantCapabilities(t, s, CapabilityRefused, CapabilityAvailable, CapabilityAvailable)
	if !s.Retry || s.Ready || s.Manual || s.Message != s.Hotkey.Message {
		t.Fatalf("status %+v", s)
	}
	if got := desktopPortalRead(t, d, w, "portal text"); got != "portal text" {
		t.Fatalf("read %q", got)
	}
	if _, err := h.Pressed(); err == nil {
		t.Fatal("the refused hotkey must report its error")
	}
	remote := d.remote.session
	d.RetryDesktop()
	d.Step()
	if d.hotkeys.pending == "" || d.remote.session != remote || d.remote.phase != "ready" {
		t.Fatalf("Retry must rebind shortcuts only: shortcuts pending=%q remote=%q phase=%s", d.hotkeys.pending, d.remote.session, d.remote.phase)
	}
}

// Before the probes finish and after a connection failure every capability
// shares the connection's state.
func TestPortalCapabilitiesFollowConnection(t *testing.T) {
	d, w := desktopFixture(t)
	defer d.Close()
	wantCapabilities(t, d.DesktopStatus(), CapabilityPending, CapabilityPending, CapabilityPending)
	// Two answers are not enough to decide anything.
	w.events = append(w.events,
		portalEvent{kind: "capability", id: portalShortcuts, code: 0},
		portalEvent{kind: "capability", id: portalRemoteDesktop, code: 1, devices: 1})
	d.Step()
	if w.registered != "" || d.manual || d.portalCapabilitiesMissing {
		t.Fatal("capabilities must be decided only after every probe answered")
	}
	w.events = append(w.events, portalEvent{kind: "failed"})
	d.Step()
	s := d.DesktopStatus()
	wantCapabilities(t, s, CapabilityRefused, CapabilityRefused, CapabilityRefused)
	if !s.Retry {
		t.Fatalf("status %+v", s)
	}
	// Full manual mode reports every capability missing.
	m := (&portalDesktop{manual: true}).DesktopStatus()
	wantCapabilities(t, m, CapabilityMissing, CapabilityMissing, CapabilityMissing)
	if !m.Manual || !m.ManualClipboard {
		t.Fatalf("status %+v", m)
	}
}
