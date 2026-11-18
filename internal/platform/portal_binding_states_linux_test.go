package platform

import (
	"strings"
	"testing"
)

// boundFixture binds one hotkey and returns the session; reply sets its trigger.
func bindingFixture(t *testing.T, trigger string) (*portalHotkeys, *fakeShortcutWire, *portalHotkey, string) {
	t.Helper()
	r, w, _ := shortcutFixture()
	h, err := r.NewHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatal(err)
	}
	r.Step()
	session := answerShortcutCreate(t, r, w, "s1")
	if h.state != bindingPending || r.State() != bindingPending {
		t.Fatalf("a hotkey with BindShortcuts outstanding must be pending, got %q", h.state)
	}
	call := w.binds[len(w.binds)-1]
	w.events = append(w.events, portalEvent{kind: "response", token: call.token, bindings: []portalBinding{{id: h.id, trigger: trigger}}})
	r.Step()
	return r, w, h, session
}

func TestBindingStateBoundWithTrigger(t *testing.T) {
	r, _, h, _ := bindingFixture(t, "CTRL+ALT+e")
	if h.state != bindingBound || r.State() != bindingBound || !h.active || h.TriggerDescription() != "CTRL+ALT+e" {
		t.Fatalf("want bound, got %q active=%v", h.state, h.active)
	}
	if r.configureCalls != 0 {
		t.Fatal("a bound hotkey must not open the chooser by itself")
	}
}

// KDE after a closed dialog: Response 0 with an empty trigger. Re-sending
// BindShortcuts cannot help, so the state is unbound and no rebind is issued.
func TestBindingStateUnboundOnEmptyTriggerAndNoRebind(t *testing.T) {
	r, w, h, _ := bindingFixture(t, "")
	if h.state != bindingUnbound || r.State() != bindingUnbound || h.active {
		t.Fatalf("want unbound, got %q", h.state)
	}
	if !r.CanConfigure() {
		t.Fatal("the chooser must be available while unbound with a live session")
	}
	binds := len(w.binds)
	for i := 0; i < 3; i++ {
		r.Step()
	}
	if len(w.binds) != binds || r.dirty {
		t.Fatal("an unbound binding must not re-send BindShortcuts on its own")
	}
}

func TestBindingStateRefusedAndRetryRebinds(t *testing.T) {
	r, w, _ := shortcutFixture()
	h, _ := r.NewHotkey("Ctrl+Alt+E")
	r.Step()
	answerShortcutCreate(t, r, w, "s1")
	w.events = append(w.events, portalEvent{kind: "response", token: w.binds[0].token, code: 1})
	r.Step()
	if h.state != bindingRefused || r.State() != bindingRefused || r.err == nil {
		t.Fatalf("Response 1 must be refused, got %q err=%v", h.state, r.err)
	}
	if r.CanConfigure() {
		t.Fatal("a refused request has no live session to configure")
	}
	// Retry (dirty) re-sends BindShortcuts through a fresh session.
	r.dirty = true
	r.Step()
	answerShortcutCreate(t, r, w, "s2")
	if len(w.binds) != 2 || h.state != bindingPending {
		t.Fatalf("retry must rebind: binds=%d state=%q", len(w.binds), h.state)
	}
}

func TestConfigureShortcutsCallAndShortcutsChangedBinds(t *testing.T) {
	r, w, h, session := bindingFixture(t, "")
	if err := r.ConfigureShortcuts(); err != nil {
		t.Fatal(err)
	}
	if len(w.configures) != 1 || w.configures[0].session != session || w.configures[0].token == "" {
		t.Fatalf("ConfigureShortcuts must target the hotkey session: %+v", w.configures)
	}
	if !r.Configuring() {
		t.Fatal("chooser open must be reported")
	}
	w.events = append(w.events, portalEvent{kind: "shortcuts-changed", session: session, bindings: []portalBinding{{id: h.id, trigger: "CTRL+SHIFT+t"}}})
	r.Step()
	if h.state != bindingBound || !h.active || h.TriggerDescription() != "CTRL+SHIFT+t" || r.Configuring() || r.State() != bindingBound {
		t.Fatalf("ShortcutsChanged must flip unbound to bound: %q active=%v", h.state, h.active)
	}
	if len(w.binds) != 1 {
		t.Fatal("choosing keys must not re-send BindShortcuts")
	}
}

func TestShortcutsChangedClearingKeyReturnsToUnbound(t *testing.T) {
	r, w, h, session := bindingFixture(t, "CTRL+ALT+e")
	w.events = append(w.events, portalEvent{kind: "shortcuts-changed", session: session, bindings: []portalBinding{{id: h.id, trigger: ""}}})
	r.Step()
	if h.state != bindingUnbound || h.active || r.State() != bindingUnbound {
		t.Fatalf("a cleared key must become unbound, got %q", h.state)
	}
}

func TestConfigureShortcutsFailureFallsBackToSettingsText(t *testing.T) {
	r, w, _, _ := bindingFixture(t, "")
	if err := r.ConfigureShortcuts(); err != nil {
		t.Fatal(err)
	}
	w.events = append(w.events, portalEvent{kind: "failed", token: w.configures[0].token})
	r.Step()
	if !r.noConfigure || r.Configuring() || r.CanConfigure() {
		t.Fatal("a failed ConfigureShortcuts must mark the call unsupported")
	}
	if r.err != nil || r.session == "" || r.State() != bindingUnbound {
		t.Fatal("a failed chooser call must not tear down the session or the unbound state")
	}
	if err := r.ConfigureShortcuts(); err == nil || len(w.configures) != 1 {
		t.Fatal("must not call again once unsupported")
	}
}

func TestConfigureShortcutsRequiresSession(t *testing.T) {
	r, w, _ := shortcutFixture()
	r.NewHotkey("Ctrl+Alt+E")
	if err := r.ConfigureShortcuts(); err == nil || len(w.configures) != 0 {
		t.Fatal("no session yet: must not call")
	}
}

func TestShortcutSettingsPlacePerDesktop(t *testing.T) {
	for desktop, want := range map[string]string{"KDE": "System Settings → Shortcuts → Clipboard Translator", "ubuntu:GNOME": "Settings → Apps → Clipboard Translator → Global Shortcuts", "sway": "keyboard shortcut settings"} {
		t.Setenv("XDG_CURRENT_DESKTOP", desktop)
		if got := shortcutSettingsPlace(); !strings.Contains(got, want) {
			t.Errorf("%s: %q lacks %q", desktop, got, want)
		}
	}
}

// desktopUnbound drives a portalDesktop to ready with one unbound hotkey.
func desktopUnbound(t *testing.T, version uint32) (*portalDesktop, *fakeDesktopWire, *portalHotkey) {
	t.Helper()
	d, w := desktopFixture(t)
	h, err := d.hotkeys.NewHotkey("Ctrl+Alt+E")
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range []string{portalShortcuts, portalRemoteDesktop, portalClipboardInterface} {
		code := uint32(1)
		if iface == portalShortcuts {
			code = version
		}
		w.events = append(w.events, portalEvent{kind: "capability", id: iface, code: code, devices: 1})
	}
	d.Step()
	desktopSessions(d, w)
	w.events = append(w.events, portalEvent{kind: "response", token: d.hotkeys.pending, bindings: []portalBinding{{id: h.id}}})
	d.Step()
	return d, w, h
}

func TestDesktopStatusUnboundOffersChooserNotRetry(t *testing.T) {
	d, w, _ := desktopUnbound(t, 2)
	defer d.Close()
	s := d.DesktopStatus()
	if s.Binding != "unbound" || !s.ChooseKeys || s.Retry || s.Ready {
		t.Fatalf("unbound status wrong: %+v", s)
	}
	if !strings.Contains(s.Message, "Choose shortcut keys") {
		t.Fatalf("status must name the menu item: %q", s.Message)
	}
	if err := d.ChooseShortcutKeys(); err != nil || len(w.configures) != 1 {
		t.Fatalf("chooser not opened: %v %d", err, len(w.configures))
	}
	if s = d.DesktopStatus(); !strings.Contains(s.Message, "desktop shortcut dialog") {
		t.Fatalf("status while the chooser is open: %q", s.Message)
	}
}

func TestDesktopStatusOldFrontendNamesSettingsPlace(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	d, w, _ := desktopUnbound(t, 1) // GlobalShortcuts v1 has no ConfigureShortcuts
	defer d.Close()
	s := d.DesktopStatus()
	if s.ChooseKeys || s.Retry || !strings.Contains(s.Message, "System Settings → Shortcuts → Clipboard Translator") {
		t.Fatalf("v1 frontend status wrong: %+v", s)
	}
	if err := d.ChooseShortcutKeys(); err == nil || len(w.configures) != 0 {
		t.Fatal("v1 frontend must not receive ConfigureShortcuts")
	}
}

func TestDesktopStatusRefusedPendingAndBound(t *testing.T) {
	d, w := desktopFixture(t)
	defer d.Close()
	h, _ := d.hotkeys.NewHotkey("Ctrl+Alt+E")
	desktopCapabilities(d, w)
	desktopSessions(d, w)
	if s := d.DesktopStatus(); s.Binding != "pending" || !strings.Contains(s.Message, "Allow translation shortcuts") || s.ChooseKeys {
		t.Fatalf("pending status wrong: %+v", s)
	}
	w.events = append(w.events, portalEvent{kind: "response", token: d.hotkeys.pending, code: 1})
	d.Step()
	if s := d.DesktopStatus(); s.Binding != "refused" || !s.Retry || s.ChooseKeys || !strings.Contains(s.Message, "refused") {
		t.Fatalf("refused status wrong: %+v", s)
	}
	_ = h
	// Retry asks again; a keyed reply is bound.
	d.RetryDesktop()
	d.Step()
	desktopSessions(d, w)
	w.events = append(w.events, portalEvent{kind: "response", token: d.hotkeys.pending, bindings: []portalBinding{{id: h.id, trigger: "CTRL+ALT+e"}}})
	d.Step()
	if s := d.DesktopStatus(); s.Binding != "bound" || s.ChooseKeys {
		t.Fatalf("bound status wrong: %+v", s)
	}
}

// A RemoteDesktop failure (P1-1) must not disturb the binding state.
func TestChooseShortcutKeysWithoutHotkeysOrConnection(t *testing.T) {
	d := &portalDesktop{}
	if err := d.ChooseShortcutKeys(); err == nil {
		t.Fatal("no connection must be an error")
	}
}
