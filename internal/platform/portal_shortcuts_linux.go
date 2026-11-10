package platform

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/hotkeyspec"
)

type portalBinding struct{ id, description, trigger string }
type portalShortcutWire interface {
	portalWire
	BindShortcuts(session, token string, bindings []portalBinding)
	CloseSession(string)
	CloseRequest(string)
}

// Registration is local until Step, so all profile hotkeys enter one BindShortcuts call.
// A settings save closes/recreates only the shortcuts session: RemoteDesktop stays alive.
type portalHotkeys struct {
	client                      *portalClient
	wire                        portalShortcutWire
	entries                     []*portalHotkey
	dirty                       bool
	session, pending, operation string
	usedSession                 bool
	deadline                    time.Time
	err                         error
}

type portalHotkey struct {
	owner                       *portalHotkeys
	id, preferred, actual       string
	closed, fired, active, held bool
	err                         error
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
	r := &portalHotkeys{client: p, wire: w}
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
	h := &portalHotkey{owner: r, id: id, preferred: preferred}
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
	kept := r.entries[:0]
	for _, h := range r.entries {
		h.active = false
		h.fired = false
		h.held = false
		h.actual = ""
		h.err = nil
		if !h.closed {
			kept = append(kept, h)
		}
	}
	r.entries = kept
}
func (r *portalHotkeys) retire() {
	if r.session != "" {
		delete(r.client.sessions, portalShortcuts)
		r.wire.CloseSession(r.session)
		r.session = ""
	}
}
func (r *portalHotkeys) fail(message string) {
	r.clear()
	r.err = errors.New(message)
	if r.pending != "" {
		r.wire.CloseRequest(r.pending)
	}
	r.pending = ""
	r.operation = ""
	r.dirty = false
	r.retire()
}

func (r *portalHotkeys) Step() {
	// Invalidate old activations before draining events when a profile set changed.
	if r.dirty {
		if r.pending != "" && r.operation == "bind" {
			r.wire.CloseRequest(r.pending)
			r.pending = ""
			r.operation = ""
			r.retire()
		}
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
	r.clear()
	r.err = nil
	r.dirty = false
	if len(r.entries) == 0 {
		r.retire()
		return
	}
	if !r.usedSession {
		r.session = r.client.sessions[portalShortcuts]
		r.usedSession = true
		r.bind()
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
	var bindings []portalBinding
	for i, h := range r.entries {
		bindings = append(bindings, portalBinding{id: h.id, description: fmt.Sprintf("Translate with profile %d", i+1), trigger: h.preferred})
	}
	r.pending = token
	r.operation = "bind"
	r.deadline = r.client.now().Add(3 * time.Minute)
	r.wire.BindShortcuts(r.session, token, bindings)
}

func (r *portalHotkeys) event(e portalEvent) bool {
	switch e.kind {
	case "closed":
		if e.session == r.session {
			delete(r.client.sessions, portalShortcuts)
			r.session = ""
			r.fail("the shortcut session was closed; retry the hotkey settings")
			return true
		}
	case "response", "failed":
		if r.pending == "" || e.token != r.pending {
			return false
		}
		operation := r.operation
		r.pending = ""
		r.operation = ""
		if e.kind == "failed" || e.code != 0 {
			r.fail("shortcut registration was refused; retry the hotkey settings")
			return true
		}
		if operation == "create" {
			if !strings.HasPrefix(e.session, "/org/freedesktop/portal/desktop/session/") {
				r.fail("the portal returned an invalid shortcut session")
				return true
			}
			r.session = e.session
			r.client.sessions[portalShortcuts] = e.session
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
		// If settings changed while the dialog was open, discard its obsolete result.
		if r.dirty {
			return true
		}
		r.apply(e.bindings)
		return true
	case "activated", "deactivated", "shortcuts-changed":
		if e.session != r.session || r.dirty || r.pending != "" {
			return true
		}
		if e.kind == "shortcuts-changed" {
			for _, b := range e.bindings {
				for _, h := range r.entries {
					if h.id == b.id {
						h.actual = b.trigger
						h.active = b.trigger != ""
						h.fired = false
						h.held = false
						h.err = nil
						if !h.active {
							h.err = errors.New("the portal disabled this shortcut")
						}
					}
				}
			}
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
func (r *portalHotkeys) apply(bindings []portalBinding) {
	for _, h := range r.entries {
		h.active = false
		h.actual = ""
		h.err = errors.New("the portal did not register this hotkey; choose a shortcut in desktop settings")
		for _, b := range bindings {
			if b.id == h.id && b.trigger != "" {
				h.active = true
				h.actual = b.trigger
				h.err = nil
				break
			}
		}
	}
}
