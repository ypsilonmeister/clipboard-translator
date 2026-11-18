package platform

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	kind             string
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

// portalClient owns only the private D-Bus peer and Registry.Register. Each
// feature (portalHotkeys, portalRemote) creates and owns its own portal session,
// so a refused or inhibited RemoteDesktop session cannot take the shortcuts down.
// Only connection-level failures (lost peer, failed Register, timeout before
// registration) fail the client and therefore every feature.
type portalClient struct {
	tokens   portalTokenStore // used by the RemoteDesktop authorization stage (W3)
	wire     portalWire
	phase    string // probing, registering, ready, failed, closed
	deadline time.Time
	now      func() time.Time
	err      error
	onEvent  func(portalEvent) bool
}

func startPortalClient(w portalWire, appID string, now func() time.Time) *portalClient {
	p := &portalClient{wire: w, phase: "registering", now: now}
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
	p.wire.Close() // Losing the private D-Bus peer also closes pending requests and sessions.
}

// Step is called while pumping the GTK main context. Bound the work per tick.
// Feature handlers (onEvent) see every event first; whatever they do not claim
// is either connection-level or stale.
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
			if p.phase == "registering" {
				p.phase = "ready"
			}
		case "failed":
			// A token names a feature request. Unclaimed, it is a retired or
			// canceled request completing late, never a connection failure.
			if e.token != "" {
				continue
			}
			p.fail("the Wayland portal connection failed; check that the desktop portal service is running and try again")
			return
		}
		// Responses and Session.Closed for sessions no feature owns are stale.
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
}
