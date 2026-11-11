package platform

import (
	"errors"
	"time"
)

const portalClipboardInterface = "org.freedesktop.portal.Clipboard"

type portalDesktopWire interface {
	portalShortcutWire
	portalRemoteWire
	Probe(string)
}

// The wrapper stays stable across reconnects, including existing hotkey handles.
type portalDesktop struct {
	portalCapabilitiesMissing bool
	manual                    bool
	manualClipboard           Clipboard
	showManual                func()
	open                      func() (portalDesktopWire, error)
	wire                      portalDesktopWire
	client                    *portalClient
	hotkeys                   *portalHotkeys
	remote                    *portalRemote
	tokens                    portalTokenStore
	capabilities              map[string]bool
	err                       error
	now                       func() time.Time
	closed                    bool
	appID                     string
	retryRemote               bool
}

func (d *portalDesktop) connect() {
	w, err := d.open()
	if err != nil {
		d.err = err
		return
	}
	d.err = nil
	d.manual = false
	d.portalCapabilitiesMissing = false
	d.retryRemote = false
	d.wire = w
	d.capabilities = make(map[string]bool)
	p := &portalClient{wire: w, phase: "probing", now: d.now, sessions: make(map[string]string), pending: make(map[string]string), tokens: d.tokens, deadline: d.now().Add(15 * time.Second)}
	p.onEvent = func(e portalEvent) bool {
		if e.kind != "capability" {
			return false
		}
		if p.phase != "probing" || d.capabilities[e.id] {
			return true
		}
		if e.code == 0 || (e.id == portalRemoteDesktop && e.devices&1 == 0) {
			d.manual = true
			d.portalCapabilitiesMissing = true
			if d.showManual != nil {
				d.showManual()
			}
			p.fail("Wayland requires GlobalShortcuts, RemoteDesktop keyboard and Clipboard portals; use GNOME 48+ with current desktop portal packages, then Retry Wayland permissions")
			return true
		}
		switch e.id {
		case portalShortcuts, portalRemoteDesktop, portalClipboardInterface:
			d.capabilities[e.id] = true
		default:
			return true
		}
		if len(d.capabilities) == 3 {
			p.phase = "registering"
			p.deadline = d.now().Add(15 * time.Second)
			id := d.appID
			if id == "" {
				id = waylandAppID
			}
			w.Register(id)
		}
		return true
	}
	h := newPortalHotkeys(p, w)
	if d.hotkeys != nil {
		h.entries = d.hotkeys.entries
		for _, entry := range h.entries {
			entry.owner = h
		}
		h.clear()
		h.dirty = len(h.entries) > 0
	}
	d.client = p
	d.hotkeys = h
	d.remote = newPortalRemote(p, w)
	for _, iface := range []string{portalShortcuts, portalRemoteDesktop, portalClipboardInterface} {
		w.Probe(iface)
	}
}

func (d *portalDesktop) Step() {
	if d.closed || d.client == nil {
		return
	}
	d.hotkeys.Step()
	// Show consent dialogs in sequence, including when shortcut binding was refused.
	if len(d.hotkeys.entries) > 0 && (d.remote.phase == "idle" || d.retryRemote) && !d.hotkeys.dirty && d.hotkeys.pending == "" && d.client.phase == "ready" {
		d.remote.Authorize()
		d.retryRemote = false
	}
	d.remote.Step()
}
func (d *portalDesktop) DesktopStatus() DesktopStatus {
	s := DesktopStatus{Crostini: desktopCrostini, Wayland: true, PortalCapabilitiesMissing: d.portalCapabilitiesMissing}
	switch {
	case d.manual && !d.closed:
		s.Manual = true
		s.Message = "Manual mode: hotkey workflow unavailable; required desktop portals are missing. Copy text, choose Translate clipboard, then paste. GNOME 48+ supports the portal workflow."
		s.Retry = true
	case d.closed:
		s.Message = "Wayland connection is closed"
	case d.err != nil:
		s.Message = d.err.Error()
		s.Retry = true
	case d.client.err != nil:
		s.Message = d.client.err.Error()
		s.Retry = true
	case d.client.phase != "ready":
		s.Message = "Connecting to Wayland desktop portals..."
	case d.hotkeys.err != nil:
		s.Message = d.hotkeys.err.Error()
		s.Retry = true
	case d.hotkeys.dirty || d.hotkeys.pending != "":
		s.Message = "Allow translation shortcuts in the desktop permission dialog..."
	case d.remote.err != nil:
		s.Message = d.remote.err.Error()
		s.Retry = true
	case d.remote.phase != "ready":
		s.Message = "Allow keyboard and clipboard access in the desktop permission dialog..."
	default:
		s.Ready = true
		s.Message = "Wayland: release the hotkey modifiers after pressing; paste translations manually with Ctrl+V. Automatic paste is disabled."
		for _, h := range d.hotkeys.entries {
			if !h.closed && !h.active {
				s.Ready = false
				s.Retry = true
				s.Message = "A translation shortcut is inactive; choose it in the desktop dialog with Retry Wayland permissions"
				break
			}
		}
		if d.remote.warning != nil {
			s.Message += " " + d.remote.warning.Error()
		}
	}
	return s
}
func (d *portalDesktop) RetryDesktop() {
	if d.closed {
		return
	}
	if d.client == nil || d.client.phase == "failed" || d.err != nil {
		if d.client != nil {
			d.client.Close()
		}
		d.connect()
		return
	}
	if d.hotkeys.pending == "" {
		for _, h := range d.hotkeys.entries {
			if h.err != nil {
				d.hotkeys.dirty = true
			}
		}
		if d.hotkeys.err != nil {
			d.hotkeys.dirty = true
		}
	}
	if d.remote.phase == "failed" {
		d.retryRemote = true
	}
}
func (d *portalDesktop) access() error {
	d.Step()
	if d.closed || d.remote == nil {
		return errors.New("Wayland is disconnected; use Retry Wayland permissions")
	}
	if d.client.err != nil {
		return d.client.err
	}
	if d.remote.phase != "ready" {
		if d.remote.err != nil {
			return d.remote.err
		}
		return errors.New("Wayland keyboard and clipboard permission is pending; allow access in the desktop dialog")
	}
	return nil
}
func (d *portalDesktop) Read() (string, error) {
	if d.manual && !d.closed && d.manualClipboard != nil {
		return d.manualClipboard.Read()
	}
	if err := d.access(); err != nil {
		return "", err
	}
	return d.remote.clip.Read()
}
func (d *portalDesktop) Write(text string) error {
	if d.manual && !d.closed && d.manualClipboard != nil {
		return d.manualClipboard.Write(text)
	}
	if err := d.access(); err != nil {
		return err
	}
	return d.remote.clip.Write(text)
}
func (d *portalDesktop) ReplaceIfUnchanged(old, text string) (bool, error) {
	if d.manual && !d.closed && d.manualClipboard != nil {
		return d.manualClipboard.ReplaceIfUnchanged(old, text)
	}
	if err := d.access(); err != nil {
		return false, err
	}
	return d.remote.clip.ReplaceIfUnchanged(old, text)
}
func (d *portalDesktop) Close() error {
	if !d.closed {
		d.closed = true
		if d.remote != nil {
			d.remote.Close()
		}
		if d.client != nil {
			d.client.Close()
		}
	}
	return nil
}

func (d *portalDesktop) CancelPendingRead() {
	if c, ok := d.manualClipboard.(interface{ CancelPendingRead() }); ok {
		c.CancelPendingRead()
	}
}

// PresentControl runs on the UI thread before manual clipboard reads.
func (d *portalDesktop) PresentControl() {
	if d.manual && d.showManual != nil {
		d.showManual()
	}
}
