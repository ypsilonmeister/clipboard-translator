package platform

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/hotkeyspec"
)

type portalBinding struct{ id, description, trigger string }

// bindingState is one shortcut's lifecycle with the portal.
//
//	pending: BindShortcuts is outstanding (the desktop dialog may be open).
//	bound:   Response 0 (or ShortcutsChanged) with a non-empty trigger.
//	unbound: Response 0 with an empty trigger (KDE after a closed dialog, or the
//	         user cleared the key). Re-sending BindShortcuts cannot help; only
//	         ConfigureShortcuts or the desktop settings can assign a key.
//	refused: the user cancelled (Response 1) or the request failed (2). Retry
//	         re-sends BindShortcuts.
type bindingState string

const (
	bindingPending bindingState = "pending"
	bindingBound   bindingState = "bound"
	bindingUnbound bindingState = "unbound"
	bindingRefused bindingState = "refused"
)

const configureTimeout = 2 * time.Minute

type portalShortcutWire interface {
	portalWire
	BindShortcuts(session, token string, bindings []portalBinding)
	// ConfigureShortcuts opens the desktop's own key chooser (GlobalShortcuts v2).
	// It has no Response; a failed call comes back as a "failed" event carrying token.
	ConfigureShortcuts(session, token, parentWindow, activationToken string)
	CloseSession(string)
	CloseRequest(string)
}

// Registration is local until Step, so all profile hotkeys enter one BindShortcuts call.
// portalHotkeys creates and owns its GlobalShortcuts session: a refusal or close
// fails only the shortcuts, and a settings save closes/recreates only this session.
type portalHotkeys struct {
	client                      *portalClient
	wire                        portalShortcutWire
	entries                     []*portalHotkey
	dirty                       bool
	session, pending, operation string
	deadline                    time.Time
	err                         error
	retired                     map[string]bool // canceled CreateSession tokens
	// ConfigureShortcuts bookkeeping. configToken is the wire call's failure token;
	// configuring is true while the desktop chooser may be open (no signal says it
	// closed, so it also expires). noConfigure records a frontend without the call.
	configToken    string
	configuring    bool
	configDeadline time.Time
	noConfigure    bool
	configureCalls int
	// requested is the per-ID snapshot sent to the live session. The portal's
	// last outcome survives a settings save that recreates equivalent handles.
	requested []portalBinding
	outcome   map[string]portalOutcome
}

type portalOutcome struct {
	actual string
	state  bindingState
	err    error
}

type portalHotkey struct {
	owner                       *portalHotkeys
	id, preferred, actual       string
	closed, fired, active, held bool
	err                         error
	state                       bindingState
}

func portalPreferredTrigger(spec string) (string, error) {
	h, err := hotkeyspec.Parse(spec, true)
	if err != nil {
		return "", err
	}
	var parts []string
	if h.Ctrl {
		parts = append(parts, "CTRL")
	}
	if h.Alt {
		parts = append(parts, "ALT")
	}
	if h.Shift {
		parts = append(parts, "SHIFT")
	}
	if h.Win {
		parts = append(parts, "LOGO")
	}
	key := h.Key
	if len(key) == 1 {
		key = strings.ToLower(key)
	}
	return strings.Join(append(parts, key), "+"), nil
}

func newPortalHotkeys(p *portalClient, w portalShortcutWire) *portalHotkeys {
	r := &portalHotkeys{client: p, wire: w, retired: make(map[string]bool)}
	previous := p.onEvent
	p.onEvent = func(e portalEvent) bool {
		if r.event(e) {
			return true
		}
		return previous != nil && previous(e)
	}
	return r
}

func (r *portalHotkeys) NewHotkey(spec string) (*portalHotkey, error) {
	preferred, err := portalPreferredTrigger(spec)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, h := range r.entries {
		if !h.closed {
			if h.preferred == preferred {
				return nil, errors.New("this hotkey is already configured")
			}
			used[h.id] = true
		}
	}
	if len(used) >= maxHotkeys {
		return nil, fmt.Errorf("at most %d hotkeys can be registered", maxHotkeys)
	}
	id := ""
	for i := 1; i <= maxHotkeys; i++ {
		candidate := fmt.Sprintf("profile_%d", i)
		if !used[candidate] {
			id = candidate
			break
		}
	}
	h := &portalHotkey{owner: r, id: id, preferred: preferred, state: bindingPending}
	r.entries = append(r.entries, h)
	r.dirty = true
	return h, nil
}

func (h *portalHotkey) TriggerDescription() string { return h.actual }
func (h *portalHotkey) Pressed() (bool, error) {
	if h.closed {
		return false, nil
	}
	h.owner.Step()
	if h.owner.client.err != nil {
		return false, h.owner.client.err
	}
	if h.owner.err != nil {
		return false, h.owner.err
	}
	if h.err != nil {
		return false, h.err
	}
	fired := h.fired && h.active && !h.owner.dirty
	h.fired = false
	return fired, nil
}
func (h *portalHotkey) Close() error {
	if !h.closed {
		h.closed = true
		h.active = false
		h.fired = false
		h.actual = ""
		h.owner.dirty = true
	}
	return nil
}

func (r *portalHotkeys) clear() {
	r.configuring = false
	kept := r.entries[:0]
	for _, h := range r.entries {
		h.active = false
		h.fired = false
		h.held = false
		h.actual = ""
		h.err = nil
		h.state = bindingPending
		if !h.closed {
			kept = append(kept, h)
		}
	}
	r.entries = kept
}
func (r *portalHotkeys) retire() {
	if r.session != "" {
		r.wire.CloseSession(r.session)
		r.session = ""
	}
	r.requested = nil
	r.outcome = nil
}

// wanted is the binding set for the open hotkeys, as BindShortcuts would send it.
func (r *portalHotkeys) wanted() []portalBinding {
	var bindings []portalBinding
	for _, h := range r.entries {
		if !h.closed {
			bindings = append(bindings, portalBinding{id: h.id, description: fmt.Sprintf("Translate with profile %d", len(bindings)+1), trigger: h.preferred})
		}
	}
	return bindings
}

// unchanged compares by ID so a settings save that leaves the portal
// bindings alone can keep the live session and the portal's chosen keys.
func (r *portalHotkeys) unchanged() bool {
	want := r.wanted()
	if r.session == "" || len(want) != len(r.requested) {
		return false
	}
	have := make(map[string]portalBinding, len(r.requested))
	for _, b := range r.requested {
		have[b.id] = b
	}
	for _, b := range want {
		if have[b.id] != b {
			return false
		}
	}
	return true
}

// adopt accepts the open hotkeys as the session's set without rebinding: closed
// handles are dropped and the queued change is settled.
func (r *portalHotkeys) adopt() {
	kept := r.entries[:0]
	for _, h := range r.entries {
		if !h.closed {
			kept = append(kept, h)
		}
	}
	r.entries = kept
	r.dirty = false
}

// restore gives reopened handles the portal's last outcome for their id.
func (r *portalHotkeys) restore() {
	for _, h := range r.entries {
		o, ok := r.outcome[h.id]
		if !ok {
			continue
		}
		h.setOutcome(o)
	}
}

func (r *portalHotkeys) fail(message string) {
	r.clear()
	r.err = errors.New(message)
	if r.pending != "" {
		r.wire.CloseRequest(r.pending)
		if r.operation == "create" {
			r.retired[r.pending] = true
		}
	}
	r.pending = ""
	r.operation = ""
	r.dirty = false
	r.retire()
}

func (r *portalHotkeys) Step() {
	// Invalidate old activations before draining events when a profile set changed.
	// Leave the dialog open and let the latest settings wait behind its response.
	// Closing the request on KDE can persist a rejected, keyless shortcut.
	if r.dirty {
		for _, h := range r.entries {
			h.active = false
			h.fired = false
			h.actual = ""
		}
	}
	r.client.Step()
	if r.client.phase == "failed" || r.client.phase == "closed" {
		r.clear()
		return
	}
	if r.pending != "" && !r.client.now().Before(r.deadline) {
		r.fail("shortcut permission request timed out; retry the hotkey settings")
		return
	}
	if !r.dirty || r.pending != "" || r.client.phase != "ready" {
		return
	}
	if r.unchanged() {
		r.adopt()
		r.restore()
		return
	}
	// BindShortcuts is single-use per session. KDE also removes shortcuts
	// omitted from a replacement bind, so keep the complete desired set.
	r.clear()
	r.err = nil
	r.dirty = false
	if len(r.entries) == 0 {
		r.retire()
		return
	}
	r.retire()
	request, err := portalToken()
	if err != nil {
		r.fail("could not create a shortcut request identifier")
		return
	}
	session, err := portalToken()
	if err != nil {
		r.fail("could not create a shortcut session identifier")
		return
	}
	r.pending = request
	r.operation = "create"
	r.deadline = r.client.now().Add(15 * time.Second)
	r.wire.CreateSession(portalShortcuts, request, session)
}
func (r *portalHotkeys) bind() {
	token, err := portalToken()
	if err != nil {
		r.fail("could not create a shortcut request identifier")
		return
	}
	bindings := r.wanted()
	r.requested = bindings
	r.outcome = make(map[string]portalOutcome, len(bindings))
	r.pending = token
	r.operation = "bind"
	r.deadline = r.client.now().Add(3 * time.Minute)
	r.wire.BindShortcuts(r.session, token, bindings)
}

func (r *portalHotkeys) event(e portalEvent) bool {
	switch e.kind {
	case "closed":
		if e.session == r.session && r.session != "" {
			r.session = ""
			r.fail("the shortcut session was closed; retry the hotkey settings")
			return true
		}
	case "response", "failed":
		if e.kind == "failed" && r.configToken != "" && e.token == r.configToken {
			// The frontend has no ConfigureShortcuts (or the call failed): fall back
			// to naming the desktop settings page.
			r.configToken = ""
			r.configuring = false
			r.noConfigure = true
			return true
		}
		if r.retired[e.token] {
			delete(r.retired, e.token)
			closeLateSession(r.wire, e)
			return true
		}
		if r.pending == "" || e.token != r.pending {
			return false
		}
		operation := r.operation
		r.pending = ""
		r.operation = ""
		if operation == "create" && (e.kind == "failed" || e.code != 0) {
			r.fail(portalCreateError(portalShortcuts, e))
			return true
		}
		if e.kind == "failed" || e.code != 0 {
			r.fail("shortcut registration was refused")
			for _, h := range r.entries {
				h.state = bindingRefused
			}
			return true
		}
		if operation == "create" {
			if !strings.HasPrefix(e.session, "/org/freedesktop/portal/desktop/session/") {
				r.fail("the portal returned an invalid shortcut session")
				return true
			}
			r.session = e.session
			// If settings changed while creating, bind the current complete profile set.
			r.clear()
			r.dirty = false
			if len(r.entries) == 0 {
				r.retire()
			} else {
				r.bind()
			}
			return true
		}
		// A changed set is reconciled in Step. A no-op save can adopt
		// the response immediately, including any empty triggers.
		if r.dirty {
			if !r.unchanged() {
				return true
			}
			r.adopt()
		}
		r.apply(e.bindings)
		return true
	case "activated", "deactivated", "shortcuts-changed":
		if e.session != r.session || r.pending != "" {
			return true
		}
		if e.kind == "shortcuts-changed" {
			r.configuring = false
			for _, b := range e.bindings {
				if _, ok := r.outcome[b.id]; !ok {
					continue
				}
				o := portalOutcome{actual: b.trigger, state: bindingBound}
				if b.trigger == "" {
					o.state = bindingUnbound
					o.err = errors.New("the portal disabled this shortcut")
				}
				r.outcome[b.id] = o
				if !r.dirty {
					for _, h := range r.entries {
						if h.id == b.id {
							h.setOutcome(o)
						}
					}
				}
			}
			return true
		}
		if r.dirty {
			return true
		}
		for _, h := range r.entries {
			if h.id == e.id && !h.closed && h.active {
				if e.kind == "activated" {
					if !h.held {
						h.fired = true
					}
					h.held = true
				} else {
					h.held = false
				}
			}
		}
		return true
	}
	return false
}
func (h *portalHotkey) setOutcome(o portalOutcome) {
	h.actual, h.state, h.err = o.actual, o.state, o.err
	h.active = o.state == bindingBound
	h.fired, h.held = false, false
}

func (r *portalHotkeys) apply(bindings []portalBinding) {
	for _, h := range r.entries {
		o := portalOutcome{
			state: bindingUnbound,
			err:   errors.New("the portal did not register this hotkey; choose a shortcut in desktop settings"),
		}
		for _, b := range bindings {
			if b.id == h.id && b.trigger != "" {
				o = portalOutcome{actual: b.trigger, state: bindingBound}
				break
			}
		}
		r.outcome[h.id] = o
		h.setOutcome(o)
	}
}

// State reports the aggregate binding state: refused beats pending beats unbound,
// and bound means every open hotkey has a trigger. It is "" with no hotkeys.
func (r *portalHotkeys) State() bindingState {
	state := bindingState("")
	for _, h := range r.entries {
		if h.closed {
			continue
		}
		switch {
		case h.state == bindingRefused:
			return bindingRefused
		case h.state == bindingPending || h.state == "":
			return bindingPending
		case h.state == bindingUnbound:
			state = bindingUnbound
		case state == "":
			state = bindingBound
		}
	}
	return state
}

// CanConfigure reports whether the chooser can be opened now: a live session, no
// request outstanding, no profile change waiting to be bound, and a frontend that
// has the call.
func (r *portalHotkeys) CanConfigure() bool {
	return r.session != "" && r.pending == "" && !r.dirty && !r.noConfigure && r.err == nil && len(r.entries) > 0
}

// Configuring reports that the desktop chooser was opened and no ShortcutsChanged
// has arrived yet.
func (r *portalHotkeys) Configuring() bool {
	return r.configuring && r.client.now().Before(r.configDeadline)
}

// ConfigureShortcuts asks the portal to open its own key chooser for this session.
// The result arrives as ShortcutsChanged, which flips unbound bindings to bound.
func (r *portalHotkeys) ConfigureShortcuts() error {
	if r.noConfigure {
		return errors.New("this desktop portal cannot open its shortcut chooser; " + shortcutSettingsPlace())
	}
	if !r.CanConfigure() {
		return errors.New("the shortcut session is not ready")
	}
	token, err := portalToken()
	if err != nil {
		return errors.New("could not create a shortcut request identifier")
	}
	r.configToken = token
	r.configuring = true
	r.configDeadline = r.client.now().Add(configureTimeout)
	r.configureCalls++
	// parent_window and activation_token are empty until xdg-foreign handles exist (P2-2).
	r.wire.ConfigureShortcuts(r.session, token, "", "")
	return nil
}

// shortcutSettingsPlace names where the user can assign keys when the portal
// cannot open its chooser.
func shortcutSettingsPlace() string {
	desktop := strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP"))
	switch {
	case strings.Contains(desktop, "KDE"):
		return "assign keys in System Settings → Shortcuts → Clipboard Translator"
	case strings.Contains(desktop, "GNOME"):
		return "assign keys in Settings → Apps → Clipboard Translator → Global Shortcuts"
	}
	return "assign keys in your desktop's keyboard shortcut settings for Clipboard Translator"
}

// Deactivated describes the shortcut, not physical modifier state. Capture adds
// a release grace period; users must release the hotkey's modifiers on Wayland.
func (r *portalHotkeys) AllReleased() bool {
	for _, h := range r.entries {
		if !h.closed && h.held {
			return false
		}
	}
	return true
}

// portalCreateError names the interface whose CreateSession failed, so a session
// that could not be created is not reported as a consent refusal.
func portalCreateError(iface string, e portalEvent) string {
	if e.kind == "failed" {
		return fmt.Sprintf("could not create %s session (the portal call failed); check that the desktop portal service is running and try again", iface)
	}
	return fmt.Sprintf("could not create %s session (response %d); check desktop portal permissions and try again", iface, e.code)
}

// closeLateSession handles the answer to a CreateSession request the feature
// already canceled. Request.Close does not stop a success that was already
// queued, and the private peer outlives one feature's failure, so a session
// created that late must be closed here or it lingers until disconnect.
func closeLateSession(w interface{ CloseSession(string) }, e portalEvent) {
	if e.kind == "response" && e.code == 0 && strings.HasPrefix(e.session, "/org/freedesktop/portal/desktop/session/") {
		w.CloseSession(e.session)
	}
}
