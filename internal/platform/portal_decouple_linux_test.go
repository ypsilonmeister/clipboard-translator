package platform

import (
	"testing"
	"time"
)

// The tests in this file pin #133 P1-1: portalClient is Register + connection
// only, and portalHotkeys / portalRemote each create and own their sessions,
// so one feature's failure never takes the other down.

// boundDesktop connects, registers, creates the shortcut session, binds one
// hotkey and leaves the remote feature at its own CreateSession request.
func boundDesktop(t *testing.T) (*portalDesktop, *fakeDesktopWire, *portalHotkey) {
	t.Helper()
	d, w := desktopFixture(t)
	h, err := d.hotkeys.NewHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatal(err)
	}
	desktopCapabilities(d, w)
	desktopSessions(d, w)
	if w.binds != 1 || d.hotkeys.session == "" {
		t.Fatalf("shortcuts were not bound on their own session: binds=%d session=%q", w.binds, d.hotkeys.session)
	}
	w.events = append(w.events, portalEvent{kind: "response", token: d.hotkeys.pending, bindings: []portalBinding{{id: h.id, trigger: "Ctrl+Alt+E"}}})
	d.Step()
	if !h.active || h.actual != "Ctrl+Alt+E" {
		t.Fatalf("hotkey not bound: active=%v actual=%q", h.active, h.actual)
	}
	if d.remote.operation != "create" || d.remote.pending == "" {
		t.Fatalf("remote must create its own session after the shortcuts: operation=%q", d.remote.operation)
	}
	q := w.requests[len(w.requests)-1]
	if q.iface != portalRemoteDesktop || q.token != d.remote.pending {
		t.Fatalf("unexpected remote session request %+v", q)
	}
	return d, w, h
}

func assertHotkeyStillWorks(t *testing.T, d *portalDesktop, w *fakeDesktopWire, h *portalHotkey, session string, requests int) {
	t.Helper()
	if d.client.phase != "ready" || d.client.err != nil || w.closes != 0 {
		t.Fatalf("remote failure reached the connection: phase=%s err=%v closes=%d", d.client.phase, d.client.err, w.closes)
	}
	for _, s := range w.closedSessions {
		if s == session {
			t.Fatal("remote failure closed the shortcut session")
		}
	}
	if d.hotkeys.session != session || d.hotkeys.err != nil || !h.active || h.actual != "Ctrl+Alt+E" {
		t.Fatalf("hotkey lost its binding: session=%q err=%v active=%v actual=%q", d.hotkeys.session, d.hotkeys.err, h.active, h.actual)
	}
	w.events = append(w.events, portalEvent{kind: "activated", session: session, id: h.id})
	if fired, err := h.Pressed(); !fired || err != nil {
		t.Fatalf("hotkey must still fire after a remote failure: fired=%v err=%v", fired, err)
	}
	w.events = append(w.events, portalEvent{kind: "deactivated", session: session, id: h.id})
	d.Step()
	if w.binds != 1 || len(w.requests) != requests {
		t.Fatalf("remote failure caused a shortcut rebind: binds=%d requests=%d", w.binds, len(w.requests))
	}
}

// #241 on GNOME: the screen shield inhibits RemoteDesktop.CreateSession and
// xdg-desktop-portal-gnome answers Response 2. The hotkey must stay bound.
func TestPortalRemoteCreateRefusalLeavesHotkeysBound(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event func(token string) portalEvent
	}{
		{"response-2-inhibited", func(token string) portalEvent { return portalEvent{kind: "response", token: token, code: 2} }},
		{"response-1-cancelled", func(token string) portalEvent { return portalEvent{kind: "response", token: token, code: 1} }},
		{"method-error", func(token string) portalEvent { return portalEvent{kind: "failed", token: token} }},
		{"malformed-session", func(token string) portalEvent {
			return portalEvent{kind: "response", token: token, session: "/not/a/portal/session"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, w, h := boundDesktop(t)
			defer d.Close()
			shortcuts := d.hotkeys.session
			requests := len(w.requests)
			w.events = append(w.events, tc.event(d.remote.pending))
			d.Step()
			if d.remote.phase != "failed" || d.remote.err == nil {
				t.Fatalf("remote failure not recorded: phase=%s err=%v", d.remote.phase, d.remote.err)
			}
			assertHotkeyStillWorks(t, d, w, h, shortcuts, requests)
			s := d.DesktopStatus()
			if s.Ready || !s.Retry || s.Message != d.remote.err.Error() {
				t.Fatalf("status must report only the remote failure: %+v", s)
			}
			if _, err := d.Read(); err == nil {
				t.Fatal("clipboard allowed without a remote session")
			}
			// Retry repairs only the remote feature: a fresh RemoteDesktop
			// session, no new shortcut session and no rebind.
			d.RetryDesktop()
			d.Step()
			if d.remote.operation != "create" || len(w.requests) != requests+1 {
				t.Fatalf("retry must create a new remote session: operation=%q requests=%d", d.remote.operation, len(w.requests))
			}
			if q := w.requests[len(w.requests)-1]; q.iface != portalRemoteDesktop {
				t.Fatalf("retry created %s instead of RemoteDesktop", q.iface)
			}
			if w.binds != 1 || d.hotkeys.session != shortcuts || !h.active {
				t.Fatal("remote retry disturbed the shortcuts")
			}
		})
	}
}

// Failures after the remote session exists stay inside the remote feature too.
func TestPortalRemoteLaterFailuresLeaveHotkeysBound(t *testing.T) {
	for _, kind := range []string{"select-refused", "start-refused", "revoked", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			d, w, h := boundDesktop(t)
			defer d.Close()
			shortcuts := d.hotkeys.session
			remote := "/org/freedesktop/portal/desktop/session/test/remote"
			w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, session: remote})
			d.Step()
			if d.remote.session != remote || d.remote.operation != "select" {
				t.Fatalf("remote did not adopt its own session: session=%q operation=%q", d.remote.session, d.remote.operation)
			}
			switch kind {
			case "select-refused":
				w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, code: 2})
			case "start-refused", "revoked":
				w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending})
				d.Step()
				w.events = append(w.events, portalEvent{kind: "method", token: d.remote.pending})
				d.Step()
				if kind == "start-refused" {
					w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, code: 1})
				} else {
					w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, devices: 1, clipboard: true, restore: "token"})
					d.Step()
					if d.remote.phase != "ready" {
						t.Fatalf("remote not ready: %s", d.remote.phase)
					}
					w.events = append(w.events, portalEvent{kind: "closed", session: remote})
				}
			case "timeout":
				d.now = func() time.Time { return time.Now().Add(4 * time.Minute) }
				d.client.now = d.now
			}
			requests := len(w.requests)
			d.Step()
			// #133 P1-2: Session.Closed parks the remote feature in "waiting"
			// (reconnect with the kept token) instead of failing it.
			want := "failed"
			if kind == "revoked" {
				want = "waiting"
			}
			if d.remote.phase != want || d.remote.session != "" {
				t.Fatalf("remote failure not recorded: phase=%s session=%q", d.remote.phase, d.remote.session)
			}
			closedRemote := false
			for _, s := range w.closedSessions {
				closedRemote = closedRemote || s == remote
			}
			if kind != "revoked" && !closedRemote {
				t.Fatal("a failed remote feature must close its own session")
			}
			if kind == "revoked" && closedRemote {
				t.Fatal("a session the portal closed must not be closed again")
			}
			assertHotkeyStillWorks(t, d, w, h, shortcuts, requests)
		})
	}
}

// The reverse direction: a refused GlobalShortcuts session fails only the
// shortcuts; keyboard/clipboard authorization still proceeds.
func TestPortalShortcutCreateRefusalLeavesRemoteWorking(t *testing.T) {
	d, w := desktopFixture(t)
	defer d.Close()
	h, _ := d.hotkeys.NewHotkey("Ctrl+Alt+E")
	desktopCapabilities(d, w)
	w.events = append(w.events, portalEvent{kind: "registered"})
	d.Step()
	if d.hotkeys.operation != "create" || len(w.requests) != 1 || w.requests[0].iface != portalShortcuts {
		t.Fatalf("shortcuts must create their own session first: %+v", w.requests)
	}
	w.events = append(w.events, portalEvent{kind: "response", token: d.hotkeys.pending, code: 2})
	d.Step()
	if d.hotkeys.err == nil || h.active || w.binds != 0 {
		t.Fatal("shortcut refusal not recorded")
	}
	if d.client.phase != "ready" || w.closes != 0 || d.remote.operation != "create" {
		t.Fatalf("shortcut refusal blocked the remote feature: client=%s closes=%d remote=%q", d.client.phase, w.closes, d.remote.operation)
	}
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, session: "/org/freedesktop/portal/desktop/session/test/remote"})
	d.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending})
	d.Step()
	w.events = append(w.events, portalEvent{kind: "method", token: d.remote.pending})
	d.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, devices: 1, clipboard: true})
	d.Step()
	if d.remote.phase != "ready" || d.remote.err != nil {
		t.Fatalf("remote must authorize independently: phase=%s err=%v", d.remote.phase, d.remote.err)
	}
	if s := d.DesktopStatus(); !s.Retry || s.Message != d.hotkeys.err.Error() {
		t.Fatalf("status must report the shortcut failure: %+v", s)
	}
	// Closing the shortcut session later never touches the remote session.
	d.RetryDesktop()
	d.Step()
	shortcutCreate := w.requests[len(w.requests)-1]
	if shortcutCreate.iface != portalShortcuts {
		t.Fatalf("retry must recreate only the shortcut session, got %s", shortcutCreate.iface)
	}
	w.events = append(w.events, portalEvent{kind: "response", token: shortcutCreate.token, session: "/org/freedesktop/portal/desktop/session/test/shortcuts"})
	d.Step()
	w.events = append(w.events, portalEvent{kind: "closed", session: d.hotkeys.session})
	d.Step()
	if d.remote.phase != "ready" || d.remote.session == "" || d.client.phase != "ready" || w.closes != 0 {
		t.Fatal("shortcut session close disturbed the remote session")
	}
	for _, s := range w.closedSessions {
		if s == d.remote.session {
			t.Fatal("shortcut failure closed the remote session")
		}
	}
}

// A real connection loss is still shared: both features stop.
func TestPortalConnectionLossStopsBothFeatures(t *testing.T) {
	d, w, h := boundDesktop(t)
	defer d.Close()
	w.events = append(w.events, portalEvent{kind: "failed"})
	d.Step()
	if d.client.phase != "failed" || w.closes != 1 {
		t.Fatalf("connection loss not detected: phase=%s closes=%d", d.client.phase, w.closes)
	}
	if fired, err := h.Pressed(); fired || err == nil {
		t.Fatal("hotkey must report the lost connection")
	}
	if d.remote.phase != "failed" {
		t.Fatalf("remote must stop on connection loss: %s", d.remote.phase)
	}
	if s := d.DesktopStatus(); !s.Retry || s.Ready {
		t.Fatalf("connection loss must be retryable: %+v", s)
	}
}
