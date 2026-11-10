package platform

import (
	"fmt"
	"testing"
	"time"
)

type fakeShortcutWire struct {
	fakePortalWire
	binds []struct {
		session, token string
		bindings       []portalBinding
	}
	closedSessions, closedRequests []string
}

func (w *fakeShortcutWire) BindShortcuts(session, token string, bindings []portalBinding) {
	w.binds = append(w.binds, struct {
		session, token string
		bindings       []portalBinding
	}{session, token, append([]portalBinding(nil), bindings...)})
}
func (w *fakeShortcutWire) CloseSession(s string) { w.closedSessions = append(w.closedSessions, s) }
func (w *fakeShortcutWire) CloseRequest(s string) { w.closedRequests = append(w.closedRequests, s) }
func shortcutFixture() (*portalHotkeys, *fakeShortcutWire, *time.Time) {
	now := time.Now()
	w := &fakeShortcutWire{}
	p := startPortalClient(w, waylandAppID, func() time.Time { return now })
	p.phase = "ready"
	p.sessions[portalShortcuts] = "/org/freedesktop/portal/desktop/session/test/initial"
	p.sessions[portalRemoteDesktop] = "/org/freedesktop/portal/desktop/session/test/remote"
	return newPortalHotkeys(p, w), w, &now
}
func finishShortcutBind(r *portalHotkeys, w *fakeShortcutWire) {
	call := w.binds[len(w.binds)-1]
	b := append([]portalBinding(nil), call.bindings...)
	for i := range b {
		b[i].trigger = "Actual " + b[i].id
	}
	w.events = append(w.events, portalEvent{kind: "response", token: call.token, bindings: b})
	r.Step()
}
func TestPortalShortcutConversionAndLimits(t *testing.T) {
	for _, tc := range []struct{ spec, want string }{{"Ctrl+Alt+E", "CTRL+ALT+e"}, {"Super+Shift+F12", "SHIFT+LOGO+F12"}, {"Ctrl+1", "CTRL+1"}} {
		got, err := portalPreferredTrigger(tc.spec)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %q %v", tc.spec, got, err)
		}
	}
	r, _, _ := shortcutFixture()
	if _, err := r.NewHotkey("E"); err == nil {
		t.Fatal("unmodified key accepted")
	}
	h, err := r.NewHotkey("Ctrl+E")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.NewHotkey("control+e"); err == nil {
		t.Fatal("duplicate accepted")
	}
	for i := 1; i < maxHotkeys; i++ {
		if _, err := r.NewHotkey(fmt.Sprintf("Ctrl+F%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.NewHotkey("Ctrl+F12"); err == nil {
		t.Fatal("limit not enforced")
	}
	h.Close()
	if _, err := r.NewHotkey("Ctrl+F12"); err != nil {
		t.Fatal(err)
	}
}
func TestPortalShortcutBatchAndActivation(t *testing.T) {
	r, w, _ := shortcutFixture()
	a, _ := r.NewHotkey("Ctrl+Alt+E")
	b, _ := r.NewHotkey("Ctrl+Alt+K")
	if len(w.binds) != 0 {
		t.Fatal("registration must be coalesced")
	}
	r.Step()
	if len(w.binds) != 1 || len(w.binds[0].bindings) != 2 {
		t.Fatal("profiles must be bound in one call")
	}
	finishShortcutBind(r, w)
	if a.TriggerDescription() != "Actual profile_1" || b.TriggerDescription() != "Actual profile_2" {
		t.Fatal("preferred trigger was used instead of actual portal description")
	}
	w.events = append(w.events, portalEvent{kind: "activated", session: r.session, id: b.id})
	if p, err := a.Pressed(); p || err != nil {
		t.Fatal("wrong profile fired")
	}
	if p, err := b.Pressed(); !p || err != nil {
		t.Fatalf("activation missing: %v %v", p, err)
	}
	w.events = append(w.events, portalEvent{kind: "activated", session: r.session, id: b.id})
	if p, _ := b.Pressed(); p {
		t.Fatal("held-key repeat fired")
	}
	w.events = append(w.events, portalEvent{kind: "deactivated", session: r.session, id: b.id}, portalEvent{kind: "activated", session: r.session, id: b.id})
	if p, _ := b.Pressed(); !p {
		t.Fatal("new press after release missing")
	}
	w.events = append(w.events, portalEvent{kind: "shortcuts-changed", session: r.session, bindings: []portalBinding{{id: a.id, trigger: "Desktop changed key"}}})
	r.Step()
	if a.actual != "Desktop changed key" || b.actual != "Actual profile_2" {
		t.Fatal("partial change erased another binding")
	}
}
func TestPortalShortcutRebindRetainsRemoteSession(t *testing.T) {
	r, w, _ := shortcutFixture()
	a, _ := r.NewHotkey("Ctrl+E")
	r.Step()
	finishShortcutBind(r, w)
	old := r.session
	remote := r.client.sessions[portalRemoteDesktop]
	a.Close()
	b, _ := r.NewHotkey("Ctrl+K")
	w.events = append(w.events, portalEvent{kind: "activated", session: old, id: b.id})
	r.Step()
	if len(w.closedSessions) != 1 || len(w.requests) != 1 || b.fired {
		t.Fatal("old binding was not invalidated before reuse")
	}
	q := w.requests[0]
	fresh := "/org/freedesktop/portal/desktop/session/test/new"
	w.events = append(w.events, portalEvent{kind: "closed", session: old}, portalEvent{kind: "response", token: q.token, session: fresh})
	r.Step()
	if len(w.binds) != 2 || w.binds[1].session != fresh || len(w.binds[1].bindings) != 1 {
		t.Fatal("full replacement set not rebound")
	}
	finishShortcutBind(r, w)
	w.events = append(w.events, portalEvent{kind: "activated", session: old, id: b.id})
	if fired, _ := b.Pressed(); fired {
		t.Fatal("stale session fired")
	}
	if r.client.sessions[portalRemoteDesktop] != remote || r.client.phase != "ready" || w.closes != 0 {
		t.Fatal("shortcut rebind interrupted remote session")
	}
	b.Close()
	r.Step()
	if len(w.closedSessions) != 2 || b.actual != "" {
		t.Fatal("last hotkey close did not retire session")
	}
}
func TestPortalShortcutRefusalTimeoutAndClose(t *testing.T) {
	for _, kind := range []string{"denied", "timeout", "closed", "connection"} {
		t.Run(kind, func(t *testing.T) {
			r, w, now := shortcutFixture()
			h, _ := r.NewHotkey("Ctrl+E")
			r.Step()
			switch kind {
			case "denied":
				w.events = append(w.events, portalEvent{kind: "response", token: r.pending, code: 1})
			case "timeout":
				*now = now.Add(4 * time.Minute)
			case "closed":
				w.events = append(w.events, portalEvent{kind: "closed", session: r.session})
			case "connection":
				w.events = append(w.events, portalEvent{kind: "failed"})
			}
			if p, err := h.Pressed(); p || err == nil || h.actual != "" {
				t.Fatalf("failure not reported: fired=%v err=%v actual=%q", p, err, h.actual)
			}
			if kind == "timeout" && len(w.closedRequests) != 1 {
				t.Fatal("timed-out dialog was not canceled")
			}
		})
	}
}
func TestPortalShortcutSubsetAndMidBindEdit(t *testing.T) {
	r, w, _ := shortcutFixture()
	a, _ := r.NewHotkey("Ctrl+E")
	b, _ := r.NewHotkey("Ctrl+K")
	r.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: r.pending, bindings: []portalBinding{{id: a.id, trigger: "Chosen key"}}})
	r.Step()
	if _, err := a.Pressed(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Pressed(); err == nil {
		t.Fatal("omitted profile must be inactive")
	}
	b.Close()
	r.Step()
	q := w.requests[len(w.requests)-1]
	w.events = append(w.events, portalEvent{kind: "response", token: q.token, session: "/org/freedesktop/portal/desktop/session/test/changed"})
	r.Step()
	pending := r.pending
	a.Close()
	c, _ := r.NewHotkey("Ctrl+Q")
	w.events = append(w.events, portalEvent{kind: "response", token: pending, bindings: []portalBinding{{id: c.id, trigger: "Obsolete"}}})
	r.Step()
	if c.actual != "" || c.active || len(w.requests) != 2 {
		t.Fatal("obsolete dialog response activated a changed profile")
	}
}

func TestPortalShortcutEditCancelsObsoleteDialog(t *testing.T) {
	r, w, _ := shortcutFixture()
	a, _ := r.NewHotkey("Ctrl+E")
	r.Step()
	obsolete := r.pending
	a.Close()
	_, err := r.NewHotkey("Ctrl+K")
	if err != nil {
		t.Fatal(err)
	}
	r.Step()
	if len(w.closedRequests) != 1 || w.closedRequests[0] != obsolete || len(w.requests) != 1 {
		t.Fatal("obsolete consent dialog was not canceled promptly")
	}
	w.events = append(w.events, portalEvent{kind: "failed", token: obsolete})
	r.Step()
	if r.client.phase != "ready" || r.client.err != nil {
		t.Fatal("late canceled call destroyed the shared portal connection")
	}
}
