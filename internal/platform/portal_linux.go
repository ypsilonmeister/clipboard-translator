package platform

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const waylandAppID = "io.github.ypsilonmeister.ClipboardTranslator"
const portalShortcuts = "org.freedesktop.portal.GlobalShortcuts"
const portalRemoteDesktop = "org.freedesktop.portal.RemoteDesktop"

// The wire and its client belong to the UI thread. Calls enqueue work; Poll never waits.
type portalWire interface {
	Register(string)
	CreateSession(iface, token, sessionToken string)
	Poll() (portalEvent, bool)
	Close()
}

type portalEvent struct {
	kind             string // registered, response, closed, failed
	token            string
	session          string
	code             uint32
	id               string
	bindings         []portalBinding
	devices          uint32
	clipboard, owner bool
	restore, mime    string
	serial           uint32
	data             []byte
}

type portalClient struct {
	tokens   portalTokenStore // used by the RemoteDesktop authorization stage (W3)
	wire     portalWire
	phase    string            // registering, creating, ready, failed, closed
	sessions map[string]string // interface -> session object path (not yet authorized for input)
	pending  map[string]string // request token -> interface
	deadline time.Time
	now      func() time.Time
	err      error
	onEvent  func(portalEvent) bool
}

func startPortalClient(w portalWire, appID string, now func() time.Time) *portalClient {
	p := &portalClient{wire: w, phase: "registering", now: now, sessions: make(map[string]string), pending: make(map[string]string)}
	p.deadline = now().Add(15 * time.Second)
	w.Register(appID)
	return p
}

func portalToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ct_" + hex.EncodeToString(b[:]), nil
}

func (p *portalClient) fail(message string) {
	p.err = errors.New(message)
	p.phase = "failed"
	p.sessions = make(map[string]string)
	p.pending = make(map[string]string)
	p.wire.Close() // Losing the private D-Bus peer also closes pending requests and sessions.
}

// Step is called while pumping the GTK main context. Bound the work per tick.
func (p *portalClient) Step() {
	if p.phase == "closed" || p.phase == "failed" {
		return
	}
	for n := 0; n < 64; n++ {
		e, ok := p.wire.Poll()
		if !ok {
			break
		}
		if p.onEvent != nil && p.onEvent(e) {
			continue
		}
		switch e.kind {
		case "registered":
			if p.phase != "registering" {
				continue
			}
			p.phase = "creating"
			p.deadline = p.now().Add(15 * time.Second)
			for _, iface := range []string{portalShortcuts, portalRemoteDesktop} {
				request, err := portalToken()
				if err != nil {
					p.fail("could not create a portal request identifier")
					return
				}
				session, err := portalToken()
				if err != nil {
					p.fail("could not create a portal session identifier")
					return
				}
				p.pending[request] = iface
				p.wire.CreateSession(iface, request, session)
			}
		case "response":
			iface, ok := p.pending[e.token]
			if !ok {
				continue
			}
			delete(p.pending, e.token)
			if e.code != 0 || !strings.HasPrefix(e.session, "/org/freedesktop/portal/desktop/session/") {
				p.fail(fmt.Sprintf("could not create %s session (response %d); check desktop portal permissions and try again", iface, e.code))
				return
			}
			p.sessions[iface] = e.session
			if len(p.pending) == 0 {
				p.phase = "ready"
			}
		case "closed":
			relevant := p.phase == "creating"
			for _, session := range p.sessions {
				relevant = relevant || session == e.session
			}
			if relevant {
				p.fail("a Wayland portal session was closed; reconnect to use translation shortcuts")
				return
			}
		case "failed":
			// A retired extension request may complete after it was canceled.
			if e.token != "" {
				if _, ok := p.pending[e.token]; !ok {
					continue
				}
			}
			p.fail("the Wayland portal connection failed; check that the desktop portal service is running and try again")
			return
		}
	}
	if p.phase != "ready" && !p.now().Before(p.deadline) {
		p.fail("the Wayland portal did not respond in time; try connecting again")
	}
}

func (p *portalClient) Close() {
	if p.phase != "closed" && p.phase != "failed" {
		p.wire.Close()
	}
	p.phase = "closed"
	p.sessions = make(map[string]string)
	p.pending = make(map[string]string)
}
