package platform

import (
	"errors"
	"strings"
	"time"
)

var errPortalNoText = errors.New("no readable text is available from the Wayland clipboard; copy text and try again")
var errPortalPending = ErrClipboardPending
var errPortalNotAuthorized = errors.New("Wayland keyboard and clipboard access is not authorized")
var errPortalClipboardChanged = errors.New("the clipboard changed before the write completed")

const portalTextLimit = 32 * 1024 * 1024
const portalTextMIME = "text/plain;charset=utf-8"

type portalRemoteWire interface {
	portalWire
	CloseSession(string)
	CloseRequest(string)
	SelectDevices(session, token, restore string)
	RequestClipboard(session, token string)
	StartRemote(session, token string)
	ReadSelection(session, token, mime string)
	SetSelection(session, token string)
	WriteSelection(session, token string, serial uint32, data []byte)
	WriteDone(session string, serial uint32, success bool)
	CancelIO(string)
	Key(session, token string, keysym int, down bool)
}

type portalRemote struct {
	client                             *portalClient
	wire                               portalRemoteWire
	phase, session, pending, operation string
	wanted                             bool
	deadline                           time.Time
	err, warning                       error
	clip                               *portalClipboard
	keys                               map[string]*portalKeys
}

func newPortalRemote(p *portalClient, w portalRemoteWire) *portalRemote {
	r := &portalRemote{client: p, wire: w, phase: "idle", keys: make(map[string]*portalKeys)}
	r.clip = &portalClipboard{remote: r, mime: portalTextMIME, transfers: make(map[string]uint32)}
	previous := p.onEvent
	p.onEvent = func(e portalEvent) bool {
		if r.event(e) {
			return true
		}
		return previous != nil && previous(e)
	}
	return r
}
func (r *portalRemote) Authorize() {
	if r.phase == "idle" || r.phase == "failed" {
		r.wanted = true
		r.err = nil
		r.warning = nil
	}
}
func (r *portalRemote) stop(message string, clearToken bool) {
	if r.pending != "" && r.operation != "clipboard" {
		r.wire.CloseRequest(r.pending)
	}
	r.pending = ""
	r.operation = ""
	r.wanted = false
	r.err = errors.New(message)
	r.phase = "failed"
	r.clip.reset()
	for token, k := range r.keys {
		k.done = true
		k.err = r.err
		delete(r.keys, token)
	}
	if r.session != "" {
		r.wire.CloseSession(r.session)
		delete(r.client.sessions, portalRemoteDesktop)
		r.session = ""
	}
	if clearToken && r.client.tokens.dir != "" {
		if err := r.client.tokens.Save(""); err != nil {
			r.warning = errors.New("could not remove the saved Wayland permission token")
		}
	}
}
func (r *portalRemote) Close() { r.stop("Wayland input session was closed", false); r.phase = "closed" }
func (r *portalRemote) Step() {
	r.client.Step()
	if r.client.phase == "failed" || r.client.phase == "closed" {
		if r.phase != "failed" && r.phase != "closed" {
			r.stop("the Wayland portal connection was lost", false)
		}
		return
	}
	if r.pending != "" && !r.client.now().Before(r.deadline) {
		r.stop("Wayland input permission request timed out; try again", false)
		return
	}
	for token, k := range r.keys {
		if !r.client.now().Before(k.deadline) {
			r.finishKeys(token, k, errors.New("Wayland keyboard request timed out"))
		}
	}
	r.clip.tick()
	if !r.wanted || r.pending != "" || r.client.phase != "ready" {
		return
	}
	r.wanted = false
	r.session = r.client.sessions[portalRemoteDesktop]
	if r.session == "" {
		r.request("create")
		return
	}
	r.selectDevices()
}
func (r *portalRemote) request(operation string) {
	token, err := portalToken()
	if err != nil {
		r.stop("could not create a Wayland request identifier", false)
		return
	}
	r.pending = token
	r.operation = operation
	r.phase = operation
	r.deadline = r.client.now().Add(3 * time.Minute)
	switch operation {
	case "create":
		session, err := portalToken()
		if err != nil {
			r.stop("could not create a Wayland session identifier", false)
			return
		}
		r.deadline = r.client.now().Add(15 * time.Second)
		r.wire.CreateSession(portalRemoteDesktop, token, session)
	case "clipboard":
		r.wire.RequestClipboard(r.session, token)
	case "start":
		r.wire.StartRemote(r.session, token)
	}
}
func (r *portalRemote) selectDevices() {
	restore := ""
	if r.client.tokens.dir != "" {
		var err error
		restore, err = r.client.tokens.Load()
		if err != nil {
			r.stop("could not load the saved Wayland permission token", false)
			return
		}
		// Restore tokens are single-use. Never reuse one after an interrupted attempt.
		if restore != "" {
			if err = r.client.tokens.Save(""); err != nil {
				r.stop("could not consume the saved Wayland permission token", false)
				return
			}
		}
	}
	token, err := portalToken()
	if err != nil {
		r.stop("could not create a Wayland request identifier", false)
		return
	}
	r.pending = token
	r.operation = "select"
	r.phase = "select"
	r.deadline = r.client.now().Add(3 * time.Minute)
	r.wire.SelectDevices(r.session, token, restore)
}
func (r *portalRemote) event(e portalEvent) bool {
	if (e.kind == "response" || e.kind == "method" || e.kind == "failed") && r.pending != "" && e.token == r.pending {
		operation := r.operation
		r.pending = ""
		r.operation = ""
		if e.kind == "failed" || e.code != 0 {
			r.stop("Wayland input or clipboard permission was refused; try again", false)
			return true
		}
		switch operation {
		case "create":
			if !strings.HasPrefix(e.session, "/org/freedesktop/portal/desktop/session/") {
				r.stop("invalid Wayland input session", false)
				return true
			}
			r.session = e.session
			r.client.sessions[portalRemoteDesktop] = e.session
			r.selectDevices()
		case "select":
			r.request("clipboard")
		case "clipboard":
			r.request("start")
		case "start":
			if e.devices&1 == 0 || !e.clipboard {
				r.stop("Wayland keyboard and clipboard access was not granted", false)
				return true
			}
			if r.client.tokens.dir != "" {
				if err := r.client.tokens.Save(e.restore); err != nil {
					r.warning = errors.New("Wayland access works, but its permission token could not be saved")
				}
			}
			r.phase = "ready"
			r.err = nil
		}
		return true
	}
	if e.kind == "closed" && e.session == r.session && r.session != "" {
		r.stop("Wayland input permission was revoked; authorize it again", true)
		return true
	}
	if k, ok := r.keys[e.token]; ok && (e.kind == "method" || e.kind == "failed") {
		delete(r.keys, e.token)
		if e.kind == "failed" || e.code != 0 {
			r.finishKeys("", k, errors.New("could not send the Wayland copy/paste keys"))
		} else {
			k.index++
			if k.index == len(k.events) {
				k.done = true
			} else {
				r.sendKey(k)
			}
		}
		return true
	}
	return r.clip.event(e)
}

type portalKey struct {
	sym  int
	down bool
}
type portalKeys struct {
	events   []portalKey
	index    int
	done     bool
	err      error
	deadline time.Time
}

func (r *portalRemote) beginKeys(key int) *portalKeys {
	k := &portalKeys{}
	if r.phase != "ready" {
		k.done = true
		k.err = errPortalNotAuthorized
		return k
	}
	if len(r.keys) != 0 {
		k.done = true
		k.err = errors.New("another Wayland keyboard operation is still finishing; try again")
		return k
	}
	k.events = []portalKey{{0xffe3, true}, {key, true}, {key, false}, {0xffe3, false}}
	k.deadline = r.client.now().Add(5 * time.Second)
	r.sendKey(k)
	return k
}
func (r *portalRemote) sendKey(k *portalKeys) {
	token, err := portalToken()
	if err != nil {
		r.finishKeys("", k, errors.New("could not create a keyboard request identifier"))
		return
	}
	r.keys[token] = k
	e := k.events[k.index]
	r.wire.Key(r.session, token, e.sym, e.down)
}
func (r *portalRemote) finishKeys(token string, k *portalKeys, err error) {
	if token != "" {
		delete(r.keys, token)
	}
	k.err = err
	k.done = true
	// Always attempt releases. Closing the session also releases its virtual keys.
	for _, sym := range []int{k.events[1].sym, 0xffe3} {
		if t, e := portalToken(); e == nil {
			r.wire.Key(r.session, t, sym, false)
		}
	}
}
