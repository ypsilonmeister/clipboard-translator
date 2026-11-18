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
	QueryScreenSaver()
}

// Reconnect schedule after an inhibited CreateSession or a Session.Closed
// (#133 design §2.1): 1 s, 5 s, 30 s, then growing to a 5 min cap.
var portalReconnectBackoff = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}

// A session that stayed up this long before closing restarts the backoff.
const portalReconnectStable = time.Minute

// Screen shield state as reported by org.gnome.ScreenSaver or
// org.freedesktop.ScreenSaver. Unknown (no service) never counts as inhibited.
const (
	shieldUnknown = iota
	shieldInactive
	shieldActive
	shieldPending
)

type portalRemote struct {
	client                             *portalClient
	wire                               portalRemoteWire
	phase, session, pending, operation string // phase: idle, create, select, clipboard, start, ready, waiting, failed, closed
	wanted                             bool
	keyboard                           bool // Start granted the keyboard device (needed for capture only)
	deadline                           time.Time
	shield                             int
	createSawShield, createRejected    bool      // shield history and deferred Response 2 for the current create
	retryAt, readySince                time.Time // waiting: next reconnect; ready: when Start succeeded
	attempt                            int       // index into portalReconnectBackoff
	err, warning                       error
	clip                               *portalClipboard
	keys                               map[string]*portalKeys
	retired                            map[string]bool // canceled CreateSession tokens
}

func newPortalRemote(p *portalClient, w portalRemoteWire) *portalRemote {
	r := &portalRemote{client: p, wire: w, phase: "idle", shield: shieldPending, keys: make(map[string]*portalKeys), retired: make(map[string]bool)}
	r.clip = &portalClipboard{remote: r, mime: portalTextMIME, transfers: make(map[string]uint32)}
	previous := p.onEvent
	p.onEvent = func(e portalEvent) bool {
		if r.event(e) {
			return true
		}
		return previous != nil && previous(e)
	}
	w.QueryScreenSaver()
	return r
}
func (r *portalRemote) Authorize() {
	if r.phase == "idle" || r.phase == "failed" {
		r.wanted = true
		r.err = nil
		r.warning = nil
		r.retryAt = time.Time{}
		r.attempt = 0
	}
}

// teardown drops the session and every request in flight. It never touches the
// restore token: the token is replaced only after a successful Start.
func (r *portalRemote) teardown(err error) {
	if r.pending != "" && r.operation != "clipboard" {
		r.wire.CloseRequest(r.pending)
		if r.operation == "create" {
			r.retired[r.pending] = true
		}
	}
	r.pending = ""
	r.operation = ""
	r.createRejected = false
	r.wanted = false
	r.keyboard = false
	r.clip.reset()
	for token, k := range r.keys {
		k.done = true
		k.err = err
		delete(r.keys, token)
	}
	if r.session != "" {
		r.wire.CloseSession(r.session)
		r.session = ""
	}
}
func (r *portalRemote) stop(message string) {
	r.err = errors.New(message)
	r.teardown(r.err)
	r.phase = "failed"
}

// wait parks the feature until the screen shield lifts or the next backoff
// step, whichever comes first. It is not a failure: no error, no Retry.
func (r *portalRemote) wait() {
	r.teardown(errors.New(r.WaitMessage()))
	r.err = nil
	r.phase = "waiting"
	if r.attempt >= len(portalReconnectBackoff) {
		r.attempt = len(portalReconnectBackoff) - 1
	}
	r.retryAt = r.client.now().Add(portalReconnectBackoff[r.attempt])
	r.attempt++
}

func (r *portalRemote) reconnecting() bool {
	return r.phase == "waiting" || (r.phase == "create" && !r.retryAt.IsZero())
}

// WaitMessage is the user-facing status during reconnect.
func (r *portalRemote) WaitMessage() string {
	if r.shield == shieldActive {
		return "Waiting for the screen to be unlocked to restore Wayland keyboard and clipboard access..."
	}
	return "Reconnecting Wayland keyboard and clipboard access..."
}
func (r *portalRemote) Close() { r.stop("Wayland input session was closed"); r.phase = "closed" }
func (r *portalRemote) Step() {
	r.client.Step()
	if r.client.phase == "failed" || r.client.phase == "closed" {
		if r.phase != "failed" && r.phase != "closed" {
			r.stop("the Wayland portal connection was lost")
		}
		return
	}
	if r.pending != "" && !r.client.now().Before(r.deadline) {
		r.stop("Wayland input permission request timed out; try again")
		return
	}
	if r.phase == "waiting" && r.client.phase == "ready" && !r.client.now().Before(r.retryAt) {
		r.wanted = true
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
	// portalRemote creates and owns its RemoteDesktop session; a refusal here
	// stops only the remote feature and leaves the shortcuts session alone.
	if r.session == "" {
		r.request("create")
		return
	}
	r.selectDevices()
}
func (r *portalRemote) request(operation string) {
	token, err := portalToken()
	if err != nil {
		r.stop("could not create a Wayland request identifier")
		return
	}
	r.pending = token
	r.operation = operation
	r.phase = operation
	r.deadline = r.client.now().Add(3 * time.Minute)
	switch operation {
	case "create":
		r.createSawShield = r.shield == shieldActive
		session, err := portalToken()
		if err != nil {
			r.stop("could not create a Wayland session identifier")
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
			r.stop("could not load the saved Wayland permission token")
			return
		}
		// Keep the token until Start returns a replacement so the next attempt
		// can offer it again. The portal may have consumed it and ask for consent.
	}
	token, err := portalToken()
	if err != nil {
		r.stop("could not create a Wayland request identifier")
		return
	}
	r.pending = token
	r.operation = "select"
	r.phase = "select"
	r.deadline = r.client.now().Add(3 * time.Minute)
	r.wire.SelectDevices(r.session, token, restore)
}

// A create's Response 2 is ambiguous until the initial GetActive finishes.
func (r *portalRemote) classifyCreateRejection() {
	if r.shield == shieldPending {
		r.createRejected = true
		return
	}
	if !r.createSawShield {
		r.stop(portalCreateError(portalRemoteDesktop, portalEvent{kind: "response", code: 2}))
		return
	}
	r.wait()
	if r.shield == shieldInactive {
		// The shield lifted before the inhibited request finished.
		r.attempt = 0
		r.retryAt = r.client.now()
	}
}

func (r *portalRemote) event(e portalEvent) bool {
	if (e.kind == "response" || e.kind == "failed") && r.retired[e.token] {
		delete(r.retired, e.token)
		closeLateSession(r.wire, e)
		return true
	}
	if (e.kind == "response" || e.kind == "method" || e.kind == "failed") && r.pending != "" && e.token == r.pending {
		operation := r.operation
		r.pending = ""
		r.operation = ""
		if operation == "create" && e.kind == "response" && e.code == 2 {
			r.classifyCreateRejection()
			return true
		}
		// Response 2 while the screen shield is up is GNOME's remote-access
		// inhibit (§0.2), not a refusal: wait for the shield to lift.
		if e.kind == "response" && e.code == 2 && r.shield == shieldActive {
			r.wait()
			return true
		}
		if operation == "create" && (e.kind == "failed" || e.code != 0) {
			r.stop(portalCreateError(portalRemoteDesktop, e))
			return true
		}
		if e.kind == "failed" || e.code != 0 {
			r.stop("Wayland input or clipboard permission was refused; try again")
			return true
		}
		switch operation {
		case "create":
			if !strings.HasPrefix(e.session, "/org/freedesktop/portal/desktop/session/") {
				r.stop("invalid Wayland input session")
				return true
			}
			r.session = e.session
			r.selectDevices()
		case "select":
			r.request("clipboard")
		case "clipboard":
			r.request("start")
		case "start":
			// Clipboard is the capability; the keyboard only adds selection
			// capture, so a grant without it still makes the clipboard usable.
			if !e.clipboard {
				r.stop("Wayland clipboard access was not granted")
				return true
			}
			r.keyboard = e.devices&1 != 0
			if e.restore != "" && r.client.tokens.dir != "" {
				if err := r.client.tokens.Save(e.restore); err != nil {
					r.warning = errors.New("Wayland access works, but its permission token could not be saved")
				}
			}
			r.phase = "ready"
			r.err = nil
			r.readySince = r.client.now()
		}
		return true
	}
	if e.kind == "closed" && e.session == r.session && r.session != "" {
		// GNOME closes started sessions when the screen shield comes up, so
		// Closed is not a revocation. Keep the token and reconnect; a real
		// revocation shows up when the restore asks again or Start fails.
		r.session = "" // The portal already closed it; do not send Session.Close.
		if r.phase == "ready" && r.client.now().Sub(r.readySince) >= portalReconnectStable {
			r.attempt = 0
		}
		r.wait()
		return true
	}
	if e.kind == "screensaver" {
		was := r.shield
		switch e.code {
		case 0:
			r.shield = shieldInactive
		case 1:
			r.shield = shieldActive
			if r.phase == "create" {
				r.createSawShield = true
			}
		case 2:
			r.shield = shieldUnknown
		}
		if r.createRejected {
			r.classifyCreateRejection()
		}
		if was == shieldActive && r.shield == shieldInactive && r.phase == "waiting" {
			r.attempt = 0
			r.retryAt = r.client.now()
		}
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
	if r.phase != "ready" || !r.keyboard {
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
