package platform

import "testing"

// P1-4 (#133): an open BindShortcuts request is never closed because settings
// changed or the app is shutting down. Changes queue until the Response and
// then bind only if the open hotkey set (ids, triggers, descriptions) changed.

// openBind registers specs and steps until BindShortcuts is outstanding.
func openBind(t *testing.T, specs ...string) (*portalHotkeys, *fakeShortcutWire, []*portalHotkey) {
	t.Helper()
	r, w, _ := shortcutFixture()
	var hs []*portalHotkey
	for _, s := range specs {
		h, err := r.NewHotkey(s)
		if err != nil {
			t.Fatal(err)
		}
		hs = append(hs, h)
	}
	r.Step()
	answerShortcutCreate(t, r, w, "s1")
	if r.operation != "bind" || len(w.binds) != 1 {
		t.Fatalf("BindShortcuts not outstanding: operation=%q binds=%d", r.operation, len(w.binds))
	}
	return r, w, hs
}

// resave mimics app.closeHotkeys + registerStartupHotkey: every handle is
// closed and the profiles are registered again in order.
func resave(t *testing.T, r *portalHotkeys, old []*portalHotkey, specs ...string) []*portalHotkey {
	t.Helper()
	for _, h := range old {
		h.Close()
	}
	var hs []*portalHotkey
	for _, s := range specs {
		h, err := r.NewHotkey(s)
		if err != nil {
			t.Fatal(err)
		}
		hs = append(hs, h)
	}
	return hs
}

func respond(r *portalHotkeys, w *fakeShortcutWire, token string, triggers map[string]string) {
	var b []portalBinding
	for id, trig := range triggers {
		b = append(b, portalBinding{id: id, trigger: trig})
	}
	w.events = append(w.events, portalEvent{kind: "response", token: token, bindings: b})
	r.Step()
}

func TestRebindQueueSettingsChangeKeepsOpenRequest(t *testing.T) {
	r, w, hs := openBind(t, "Ctrl+Alt+E", "Ctrl+Alt+K")
	open := r.pending
	session := r.session
	// A changed profile set (profile_2 gets a new key) while the dialog is open.
	resave(t, r, hs, "Ctrl+Alt+E", "Ctrl+Alt+Q")
	for i := 0; i < 3; i++ {
		r.Step()
	}
	if len(w.closedRequests) != 0 {
		t.Fatalf("settings change closed the open BindShortcuts request: %v", w.closedRequests)
	}
	if len(w.closedSessions) != 0 || r.session != session || r.pending != open {
		t.Fatalf("settings change retired the session under the open dialog: closed=%v session=%q pending=%q", w.closedSessions, r.session, r.pending)
	}
	if len(w.requests) != 1 || len(w.binds) != 1 {
		t.Fatalf("rebind was not queued: requests=%d binds=%d", len(w.requests), len(w.binds))
	}
	if !r.dirty || r.State() != bindingPending {
		t.Fatalf("queued change must stay pending: dirty=%v state=%q", r.dirty, r.State())
	}
}

func TestRebindQueueAppliesAfterResponse(t *testing.T) {
	r, w, hs := openBind(t, "Ctrl+Alt+E")
	open := r.pending
	old := r.session
	// Two edits while the dialog is open coalesce into one queued set.
	hs = resave(t, r, hs, "Ctrl+Alt+E", "Ctrl+Alt+K")
	r.Step()
	hs = resave(t, r, hs, "Ctrl+Alt+E", "Ctrl+Alt+K", "Ctrl+Alt+Q")
	r.Step()
	if len(w.requests) != 1 || len(w.binds) != 1 || len(w.closedRequests) != 0 {
		t.Fatal("edits must queue behind the open dialog")
	}
	// The dialog answers for the set it showed; that result is obsolete now.
	respond(r, w, open, map[string]string{"profile_1": "CTRL+ALT+e"})
	for _, h := range hs {
		if h.active || h.actual != "" {
			t.Fatalf("obsolete response activated %s", h.id)
		}
	}
	if len(w.closedSessions) != 1 || w.closedSessions[0] != old {
		t.Fatalf("old session must be retired only after its dialog answered: %v", w.closedSessions)
	}
	if len(w.requests) != 2 || r.operation != "create" {
		t.Fatalf("queued rebind did not start after the response: requests=%d op=%q", len(w.requests), r.operation)
	}
	answerShortcutCreate(t, r, w, "s2")
	if len(w.binds) != 2 {
		t.Fatalf("queued set bound %d times, want once", len(w.binds)-1)
	}
	got := w.binds[1].bindings
	if len(got) != 3 || got[0].id != "profile_1" || got[1].id != "profile_2" || got[2].id != "profile_3" || got[2].trigger != "CTRL+ALT+q" {
		t.Fatalf("queued rebind must carry the latest profile set with stable ids: %+v", got)
	}
	finishShortcutBind(r, w)
	for _, h := range hs {
		if !h.active || h.state != bindingBound || h.actual != "Actual "+h.id {
			t.Fatalf("%s not bound after queued rebind: state=%q actual=%q", h.id, h.state, h.actual)
		}
	}
	if len(w.closedRequests) != 0 {
		t.Fatalf("no request may ever be closed on this path: %v", w.closedRequests)
	}
}

// A save that does not touch the hotkey set (e.g. a target-language change
// closes and reopens the same hotkeys) must not rebind: the open request's
// Response applies to the reopened handles.
func TestRebindQueueUnchangedSetUsesOpenResponse(t *testing.T) {
	r, w, hs := openBind(t, "Ctrl+Alt+E", "Ctrl+Alt+K")
	open := r.pending
	hs = resave(t, r, hs, "Ctrl+Alt+E", "Ctrl+Alt+K")
	r.Step()
	respond(r, w, open, map[string]string{"profile_1": "CTRL+ALT+e", "profile_2": ""})
	if len(w.requests) != 1 || len(w.binds) != 1 || len(w.closedSessions) != 0 || len(w.closedRequests) != 0 {
		t.Fatalf("unchanged set was rebound: requests=%d binds=%d closedSessions=%v closedRequests=%v", len(w.requests), len(w.binds), w.closedSessions, w.closedRequests)
	}
	if r.dirty || len(r.entries) != 2 {
		t.Fatalf("queued change not settled: dirty=%v entries=%d", r.dirty, len(r.entries))
	}
	if hs[0].state != bindingBound || !hs[0].active || hs[0].actual != "CTRL+ALT+e" {
		t.Fatalf("reopened profile_1 not bound from the open response: %+v", hs[0])
	}
	// KDE's rejected dialog: unbound stays unbound (no useless rebind), chooser offered.
	if hs[1].state != bindingUnbound || hs[1].active || !r.CanConfigure() {
		t.Fatalf("reopened profile_2 must be unbound with the chooser available: state=%q", hs[1].state)
	}
	w.events = append(w.events, portalEvent{kind: "activated", session: r.session, id: hs[0].id})
	if p, err := hs[0].Pressed(); !p || err != nil {
		t.Fatalf("reopened handle does not fire: %v %v", p, err)
	}
}

// An unchanged save reuses every ID, including a portal key update delivered
// on the same tick as the save.
func TestRebindQueueOnlyBindingChangesReachThePortal(t *testing.T) {
	r, w, hs := openBind(t, "Ctrl+Alt+E", "Ctrl+Alt+K")
	respond(r, w, r.pending, map[string]string{"profile_1": "CTRL+ALT+e"})
	session := r.session
	hs = resave(t, r, hs, "Ctrl+Alt+E", "Ctrl+Alt+K")
	w.events = append(w.events,
		portalEvent{kind: "shortcuts-changed", session: session, bindings: []portalBinding{{id: "profile_2", trigger: "CTRL+ALT+j"}}},
		portalEvent{kind: "activated", session: session, id: "profile_1"},
	)
	r.Step()
	if r.session != session || len(w.requests) != 1 || len(w.binds) != 1 || len(w.closedSessions) != 0 {
		t.Fatalf("unchanged save touched the portal: requests=%d binds=%d closedSessions=%v", len(w.requests), len(w.binds), w.closedSessions)
	}
	if hs[0].actual != "CTRL+ALT+e" || !hs[0].active || hs[1].actual != "CTRL+ALT+j" || !hs[1].active || r.State() != bindingBound {
		t.Fatalf("reopened handles lost the portal outcome: %q/%v %q/%v state=%q", hs[0].actual, hs[0].active, hs[1].actual, hs[1].active, r.State())
	}

	if hs[0].fired {
		t.Fatal("activation received during the settings save was replayed")
	}

	// A changed key for profile_2 requires a replacement session. KDE treats
	// that payload as the complete set, so profile_1 keeps its existing ID.
	hs = resave(t, r, hs, "Ctrl+Alt+E", "Ctrl+Alt+Q")
	r.Step()
	if len(w.closedSessions) != 1 || len(w.requests) != 2 {
		t.Fatalf("changed set not rebound: closedSessions=%v requests=%d", w.closedSessions, len(w.requests))
	}
	answerShortcutCreate(t, r, w, "s2")
	got := w.binds[len(w.binds)-1].bindings
	if len(got) != 2 || got[0] != (portalBinding{id: "profile_1", description: "Translate with profile 1", trigger: "CTRL+ALT+e"}) || got[1].id != "profile_2" || got[1].trigger != "CTRL+ALT+q" {
		t.Fatalf("rebind must keep unchanged ids stable: %+v", got)
	}

	// Removing a profile is also a change (its key must be released).
	finishShortcutBind(r, w)
	resave(t, r, hs, "Ctrl+Alt+E")
	r.Step()
	if len(w.closedSessions) != 2 || len(w.requests) != 3 {
		t.Fatalf("removal not rebound: closedSessions=%v requests=%d", w.closedSessions, len(w.requests))
	}
	answerShortcutCreate(t, r, w, "s3")
	got = w.binds[len(w.binds)-1].bindings
	if len(got) != 1 || got[0].id != "profile_1" {
		t.Fatalf("replacement still includes the removed ID: %+v", got)
	}
}

// Shutdown with the dialog open: closing every hotkey and the connection must
// not close the request or the session explicitly; dropping the private peer
// is the only action (memo §1 fix 3: "On shutdown, just drop the connection").
func TestRebindQueueShutdownDropsConnectionOnly(t *testing.T) {
	r, w, hs := openBind(t, "Ctrl+Alt+E", "Ctrl+Alt+K")
	resave(t, r, hs)
	r.Step()
	if len(w.closedRequests) != 0 || len(w.closedSessions) != 0 {
		t.Fatalf("closing hotkeys at shutdown disturbed the dialog: requests=%v sessions=%v", w.closedRequests, w.closedSessions)
	}
	r.client.Close()
	r.Step()
	if len(w.closedRequests) != 0 || len(w.closedSessions) != 0 || w.closes != 1 {
		t.Fatalf("shutdown must only drop the connection: requests=%v sessions=%v closes=%d", w.closedRequests, w.closedSessions, w.closes)
	}
}

// The same through portalDesktop.Close, the CloseDesktop path.
func TestRebindQueueDesktopCloseLeavesBindRequest(t *testing.T) {
	d, w := desktopFixture(t)
	h, err := d.hotkeys.NewHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatal(err)
	}
	desktopCapabilities(d, w)
	desktopSessions(d, w)
	if d.hotkeys.operation != "bind" {
		t.Fatalf("BindShortcuts not outstanding: %q", d.hotkeys.operation)
	}
	bind := d.hotkeys.pending
	h.Close()
	d.Step()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for _, token := range w.closedRequests {
		if token == bind {
			t.Fatal("shutdown closed the open BindShortcuts request")
		}
	}
	for _, s := range w.closedSessions {
		if s == "/org/freedesktop/portal/desktop/session/test/shortcuts" {
			t.Fatal("shutdown closed the shortcut session under the open dialog")
		}
	}
	if w.closes != 1 {
		t.Fatalf("shutdown must drop the connection once, got %d", w.closes)
	}
}

// A refusal of the open dialog is not overridden by the queued change: the
// app does not re-prompt on its own, Retry does.
func TestRebindQueueRefusalDoesNotRePrompt(t *testing.T) {
	r, w, hs := openBind(t, "Ctrl+Alt+E")
	open := r.pending
	hs = resave(t, r, hs, "Ctrl+Alt+K")
	r.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: open, code: 1})
	r.Step()
	r.Step()
	if len(w.requests) != 1 || len(w.binds) != 1 {
		t.Fatalf("refusal was followed by an unrequested prompt: requests=%d binds=%d", len(w.requests), len(w.binds))
	}
	if hs[0].state != bindingRefused || r.State() != bindingRefused {
		t.Fatalf("queued handle must report the refusal: %q", hs[0].state)
	}
}
