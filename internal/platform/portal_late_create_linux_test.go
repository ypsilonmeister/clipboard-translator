package platform

import (
	"strings"
	"testing"
	"time"
)

// A CreateSession request canceled at its deadline can still complete: Request.Close
// is fire-and-forget and a success may already be queued. The feature that retired
// the request must close the late session itself, or it lives on the shared peer
// until disconnect.

func countClosed(w *fakeDesktopWire, session string) int {
	n := 0
	for _, s := range w.closedSessions {
		if s == session {
			n++
		}
	}
	return n
}

func advance(d *portalDesktop, by time.Duration) {
	base := time.Now().Add(by)
	d.now = func() time.Time { return base }
	d.client.now = d.now
}

func TestPortalLateRemoteCreateClosesOrphan(t *testing.T) {
	d, w, h := boundDesktop(t)
	defer d.Close()
	shortcuts := d.hotkeys.session
	retired := d.remote.pending
	advance(d, 20*time.Second)
	d.Step()
	if d.remote.phase != "failed" {
		t.Fatalf("create deadline not enforced: %s", d.remote.phase)
	}
	d.RetryDesktop()
	d.Step()
	current := d.remote.pending
	if d.remote.operation != "create" || current == "" || current == retired {
		t.Fatalf("retry must start a fresh create: operation=%q", d.remote.operation)
	}
	late := "/org/freedesktop/portal/desktop/session/test/late_remote"
	w.events = append(w.events, portalEvent{kind: "response", token: retired, session: late})
	d.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: retired, session: late})
	d.Step()
	if n := countClosed(w, late); n != 1 {
		t.Fatalf("late remote session closed %d times, want 1", n)
	}
	if d.remote.pending != current || d.remote.operation != "create" || d.remote.session != "" {
		t.Fatalf("late response disturbed the current retry: pending=%q operation=%q session=%q", d.remote.pending, d.remote.operation, d.remote.session)
	}
	assertHotkeyStillWorks(t, d, w, h, shortcuts, len(w.requests))
}

func TestPortalLateShortcutCreateClosesOrphan(t *testing.T) {
	d, w := desktopFixture(t)
	defer d.Close()
	h, _ := d.hotkeys.NewHotkey("Ctrl+Alt+E")
	desktopCapabilities(d, w)
	w.events = append(w.events, portalEvent{kind: "registered"})
	d.Step()
	retired := d.hotkeys.pending
	if d.hotkeys.operation != "create" || retired == "" {
		t.Fatal("shortcuts must be creating their session")
	}
	advance(d, 20*time.Second)
	d.Step()
	if d.hotkeys.err == nil || d.hotkeys.pending != "" {
		t.Fatal("shortcut create deadline not enforced")
	}
	// The sibling feature proceeds to its own session.
	if d.remote.operation != "create" {
		t.Fatalf("remote must create after the shortcut failure: %q", d.remote.operation)
	}
	remote := "/org/freedesktop/portal/desktop/session/test/remote"
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, session: remote})
	d.Step()
	d.RetryDesktop()
	d.Step()
	current := d.hotkeys.pending
	if d.hotkeys.operation != "create" || current == "" || current == retired {
		t.Fatalf("retry must start a fresh shortcut create: operation=%q", d.hotkeys.operation)
	}
	late := "/org/freedesktop/portal/desktop/session/test/late_shortcuts"
	w.events = append(w.events, portalEvent{kind: "response", token: retired, session: late})
	d.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: retired, session: late})
	d.Step()
	if n := countClosed(w, late); n != 1 {
		t.Fatalf("late shortcut session closed %d times, want 1", n)
	}
	if d.hotkeys.pending != current || d.hotkeys.operation != "create" || d.hotkeys.session != "" || w.binds != 0 {
		t.Fatalf("late response disturbed the current retry: pending=%q operation=%q session=%q binds=%d", d.hotkeys.pending, d.hotkeys.operation, d.hotkeys.session, w.binds)
	}
	if d.remote.session != remote || countClosed(w, remote) != 0 || d.client.phase != "ready" || w.closes != 0 {
		t.Fatal("late shortcut response disturbed the remote session")
	}
	_ = h
}

// A CreateSession that fails is reported per feature with the interface and the
// cause, not as a consent refusal.
func TestPortalCreateFailuresAreDiagnosed(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		event      func(token string) portalEvent
	}{
		{"response-2", "could not create org.freedesktop.portal.RemoteDesktop session (response 2)", func(token string) portalEvent { return portalEvent{kind: "response", token: token, code: 2} }},
		{"method-error", "could not create org.freedesktop.portal.RemoteDesktop session (the portal call failed)", func(token string) portalEvent { return portalEvent{kind: "failed", token: token} }},
	} {
		t.Run("remote-"+tc.name, func(t *testing.T) {
			d, w, _ := boundDesktop(t)
			defer d.Close()
			w.events = append(w.events, tc.event(d.remote.pending))
			d.Step()
			if d.remote.err == nil || !strings.HasPrefix(d.remote.err.Error(), tc.want) {
				t.Fatalf("remote create error = %v, want prefix %q", d.remote.err, tc.want)
			}
			if s := d.DesktopStatus(); s.Message != d.remote.err.Error() {
				t.Fatalf("status lost the create diagnosis: %+v", s)
			}
		})
		t.Run("shortcuts-"+tc.name, func(t *testing.T) {
			d, w := desktopFixture(t)
			defer d.Close()
			d.hotkeys.NewHotkey("Ctrl+Alt+E")
			desktopCapabilities(d, w)
			w.events = append(w.events, portalEvent{kind: "registered"})
			d.Step()
			w.events = append(w.events, tc.event(d.hotkeys.pending))
			d.Step()
			want := strings.Replace(tc.want, portalRemoteDesktop, portalShortcuts, 1)
			if d.hotkeys.err == nil || !strings.HasPrefix(d.hotkeys.err.Error(), want) {
				t.Fatalf("shortcut create error = %v, want prefix %q", d.hotkeys.err, want)
			}
			if d.client.phase != "ready" || w.closes != 0 {
				t.Fatal("a create failure must stay feature-local")
			}
		})
	}
	// Refusals after the session exists keep the consent wording.
	d, w, _ := boundDesktop(t)
	defer d.Close()
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, session: "/org/freedesktop/portal/desktop/session/test/remote"})
	d.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, code: 1})
	d.Step()
	if d.remote.err == nil || !strings.Contains(d.remote.err.Error(), "refused") {
		t.Fatalf("select refusal wording changed: %v", d.remote.err)
	}
}
