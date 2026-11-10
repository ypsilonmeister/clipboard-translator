package platform

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeDesktopWire struct {
	fakeRemoteWire
	probes []string
	binds  int
}

func (w *fakeDesktopWire) Probe(iface string)                                            { w.probes = append(w.probes, iface) }
func (w *fakeDesktopWire) BindShortcuts(session, token string, bindings []portalBinding) { w.binds++ }

func desktopFixture(t *testing.T) (*portalDesktop, *fakeDesktopWire) {
	t.Helper()
	w := &fakeDesktopWire{}
	store, err := newPortalTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := &portalDesktop{now: time.Now, tokens: store, open: func() (portalDesktopWire, error) { return w, nil }}
	d.connect()
	return d, w
}
func desktopCapabilities(d *portalDesktop, w *fakeDesktopWire) {
	for _, iface := range []string{portalShortcuts, portalRemoteDesktop, portalClipboardInterface} {
		w.events = append(w.events, portalEvent{kind: "capability", id: iface, code: 1, devices: 1})
	}
	d.Step()
}
func desktopSessions(d *portalDesktop, w *fakeDesktopWire) {
	w.events = append(w.events, portalEvent{kind: "registered"})
	d.Step()
	for token, iface := range d.client.pending {
		suffix := "remote"
		if iface == portalShortcuts {
			suffix = "shortcuts"
		}
		w.events = append(w.events, portalEvent{kind: "response", token: token, session: "/org/freedesktop/portal/desktop/session/test/" + suffix})
	}
	d.Step()
}
func TestPortalDesktopCapabilitiesBeforeRegistration(t *testing.T) {
	d, w := desktopFixture(t)
	defer d.Close()
	if len(w.probes) != 3 || len(w.registered) != 0 {
		t.Fatal("must probe before registration")
	}
	desktopCapabilities(d, w)
	if d.client.phase != "registering" || w.registered != waylandAppID {
		t.Fatal("capabilities should start registration")
	}
	if s := d.DesktopStatus(); s.Ready || s.Retry {
		t.Fatal("connecting must not report ready or retry")
	}
}
func TestPortalDesktopMissingCapabilityAndRetry(t *testing.T) {
	d, w := desktopFixture(t)
	defer d.Close()
	h, err := d.hotkeys.NewHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatal(err)
	}
	w.events = append(w.events, portalEvent{kind: "capability", id: portalRemoteDesktop, code: 1})
	d.Step()
	if s := d.DesktopStatus(); !s.Retry || s.Ready || !strings.Contains(s.Message, "GNOME 48+") {
		t.Fatal("missing keyboard must be actionable")
	}
	if _, err := d.Read(); err == nil {
		t.Fatal("unavailable access allowed")
	}
	next := &fakeDesktopWire{}
	d.open = func() (portalDesktopWire, error) { return next, nil }
	d.RetryDesktop()
	if h.owner != d.hotkeys || len(d.hotkeys.entries) != 1 || h.actual != "" {
		t.Fatal("reconnect must retain and reset local handles")
	}
	desktopCapabilities(d, next)
	desktopSessions(d, next)
	if next.binds != 1 || d.remote.phase != "idle" {
		t.Fatal("keyboard dialog must wait for shortcuts")
	}
	next.events = append(next.events, portalEvent{kind: "response", token: d.hotkeys.pending, bindings: []portalBinding{{id: h.id, trigger: "Ctrl+Shift+F9"}}})
	d.Step()
	if d.remote.phase != "select" {
		t.Fatal("remote authorization must follow shortcut response")
	}
	next.events = append(next.events, portalEvent{kind: "response", token: d.remote.pending, code: 1})
	d.Step()
	if !d.DesktopStatus().Retry || !h.active || d.client.phase != "ready" {
		t.Fatal("refusal must preserve shortcuts")
	}
	session := d.hotkeys.session
	d.RetryDesktop()
	d.Step()
	if d.remote.phase != "create" || d.hotkeys.session != session {
		t.Fatal("retry must repair only the failed remote session")
	}
}
func TestPortalDesktopConnectionFailureIsRecoverable(t *testing.T) {
	d := &portalDesktop{now: time.Now, open: func() (portalDesktopWire, error) { return nil, errors.New("bus unavailable") }}
	d.connect()
	if s := d.DesktopStatus(); !s.Retry || s.Ready {
		t.Fatal("bus failure should be recoverable")
	}
	if _, err := d.Read(); err == nil {
		t.Fatal("disconnected clipboard accepted")
	}
	d.Close()
	d.RetryDesktop()
}

func TestPortalMissingCapabilitiesEnableManualClipboardOnly(t *testing.T) {
	for _, iface := range []string{portalShortcuts, portalRemoteDesktop, portalClipboardInterface} {
		t.Run(iface, func(t *testing.T) {
			d, w := desktopFixture(t)
			defer d.Close()
			shown := false
			d.showManual = func() { shown = true }
			clipboard := &manualTestClipboard{text: "Hello"}
			d.manualClipboard = clipboard
			w.events = append(w.events, portalEvent{kind: "capability", id: iface, code: 0})
			d.Step()
			s := d.DesktopStatus()
			if !d.manual || !shown || s.Ready || !strings.Contains(s.Message, "Manual mode") {
				t.Fatalf("status=%+v manual=%v shown=%v", s, d.manual, shown)
			}
			text, err := d.Read()
			if err != nil || text != "Hello" {
				t.Fatalf("read=%q %v", text, err)
			}
			if ok, err := d.ReplaceIfUnchanged("Hello", "こんにちは"); !ok || err != nil {
				t.Fatalf("replace=%v %v", ok, err)
			}
			if ok, _ := d.ReplaceIfUnchanged("Hello", "stale"); ok {
				t.Fatal("overwrote newer clipboard")
			}
			if len(w.registered) != 0 {
				t.Fatal("missing portals must not request authorization")
			}
		})
	}
	d, w := desktopFixture(t)
	defer d.Close()
	desktopCapabilities(d, w)
	if d.manual {
		t.Fatal("complete portals changed to manual mode")
	}
}

type manualTestClipboard struct{ text string }

func (c *manualTestClipboard) Read() (string, error)   { return c.text, nil }
func (c *manualTestClipboard) Write(text string) error { c.text = text; return nil }
func (*manualTestClipboard) Close() error              { return nil }
func (c *manualTestClipboard) ReplaceIfUnchanged(old, text string) (bool, error) {
	if c.text != old {
		return false, nil
	}
	c.text = text
	return true, nil
}
