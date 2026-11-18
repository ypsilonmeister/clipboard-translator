package platform

import (
	"errors"
	"strings"
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
	manual                    bool // every portal capability is missing: full manual mode
	hotkeyMissing             bool // no GlobalShortcuts: the menu action is the only trigger
	clipboardMissing          bool // no RemoteDesktop keyboard or Clipboard: GTK clipboard via the control window
	probes                    map[string]bool
	manualClipboard           Clipboard
	showManual                func()
	open                      func() (portalDesktopWire, error)
	wire                      portalDesktopWire
	client                    *portalClient
	hotkeys                   *portalHotkeys
	remote                    *portalRemote
	tokens                    portalTokenStore
	err                       error
	now                       func() time.Time
	closed                    bool
	appID                     string
	retryRemote               bool
}

const (
	portalManualMessage      = "Manual mode: hotkey workflow unavailable; required desktop portals are missing. Copy text, choose Translate clipboard, then paste. GNOME 48+ supports the portal workflow."
	portalHotkeyMissing      = "No translation hotkey: this desktop has no GlobalShortcuts portal (GNOME 48+ provides it). Copy text, choose Translate clipboard, then paste."
	portalClipboardMissing   = "No background clipboard access: this desktop has no RemoteDesktop keyboard or Clipboard portal, so the clipboard is read and written through the app's control window."
	portalCaptureNoClipboard = "Selected text cannot be copied automatically without the clipboard portal; the hotkey translates the text already on the clipboard."
	portalCaptureNoKeyboard  = "Wayland keyboard access was not granted, so the hotkey translates the text already on the clipboard; use Retry Wayland permissions to allow it."
	portalHotkeyPending      = "Allow translation shortcuts in the desktop permission dialog..."
	portalHotkeyInactive     = "A translation shortcut is inactive; choose it in the desktop dialog with Retry Wayland permissions"
	portalClipboardPending   = "Allow keyboard and clipboard access in the desktop permission dialog..."
	portalConnectingMessage  = "Connecting to Wayland desktop portals..."
	portalReadyMessage       = "Wayland: release the hotkey modifiers after pressing; paste translations manually with Ctrl+V. Automatic paste is disabled."
	portalPartialReadySuffix = "Paste translations manually with Ctrl+V. Automatic paste is disabled."
)

// The capability probe waits for GlobalShortcuts, RemoteDesktop and Clipboard.
const portalCapabilityInterface = 3

func (d *portalDesktop) connect() {
	w, err := d.open()
	if err != nil {
		d.err = err
		return
	}
	d.err = nil
	d.manual = false
	d.hotkeyMissing = false
	d.clipboardMissing = false
	d.portalCapabilitiesMissing = false
	d.retryRemote = false
	d.wire = w
	d.probes = make(map[string]bool)
	p := &portalClient{wire: w, phase: "probing", now: d.now, tokens: d.tokens, deadline: d.now().Add(15 * time.Second)}
	p.onEvent = func(e portalEvent) bool {
		if e.kind != "capability" {
			return false
		}
		if p.phase != "probing" {
			return true
		}
		switch e.id {
		case portalShortcuts, portalRemoteDesktop, portalClipboardInterface:
		default:
			return true
		}
		if _, seen := d.probes[e.id]; seen {
			return true
		}
		// A probe that failed reports version 0, the same as an absent interface.
		d.probes[e.id] = e.code != 0 && (e.id != portalRemoteDesktop || e.devices&1 != 0)
		if len(d.probes) < portalCapabilityInterface {
			return true
		}
		d.evaluateCapabilities()
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

// evaluateCapabilities runs once all three probes answered. Each missing
// capability degrades on its own (#133 design §5.3); only when nothing at all
// is usable does the backend fall back to full manual mode without registering.
func (d *portalDesktop) evaluateCapabilities() {
	p := d.client
	d.hotkeyMissing = !d.probes[portalShortcuts]
	d.clipboardMissing = !d.probes[portalRemoteDesktop] || !d.probes[portalClipboardInterface]
	d.portalCapabilitiesMissing = d.hotkeyMissing || d.clipboardMissing
	d.hotkeys.missing = d.hotkeyMissing
	if d.hotkeyMissing && d.showManual != nil {
		// Without a hotkey the control window's menu is the only way in.
		d.showManual()
	}
	if d.hotkeyMissing && d.clipboardMissing {
		d.manual = true
		p.fail("Wayland requires GlobalShortcuts, RemoteDesktop keyboard and Clipboard portals; use GNOME 48+ with current desktop portal packages, then Retry Wayland permissions")
		return
	}
	p.phase = "registering"
	p.deadline = d.now().Add(15 * time.Second)
	id := d.appID
	if id == "" {
		id = waylandAppID
	}
	d.wire.Register(id)
}

func (d *portalDesktop) Step() {
	if d.closed || d.client == nil {
		return
	}
	d.hotkeys.Step()
	// Show consent dialogs in sequence, including when shortcut binding was
	// refused. Without the GlobalShortcuts portal there is nothing to wait for.
	settled := d.hotkeyMissing || (len(d.hotkeys.entries) > 0 && !d.hotkeys.dirty && d.hotkeys.pending == "")
	if !d.clipboardMissing && (d.remote.phase == "idle" || d.retryRemote) && settled && d.client.phase == "ready" {
		d.remote.Authorize()
		d.retryRemote = false
	}
	d.remote.Step()
}

func (d *portalDesktop) hotkeyStatus() CapabilityStatus {
	h := d.hotkeys
	switch {
	case d.hotkeyMissing:
		return CapabilityStatus{CapabilityMissing, portalHotkeyMissing}
	case h.err != nil:
		return CapabilityStatus{CapabilityRefused, h.err.Error()}
	case h.dirty || h.pending != "":
		return CapabilityStatus{CapabilityPending, portalHotkeyPending}
	}
	for _, e := range h.entries {
		if !e.closed && !e.active {
			return CapabilityStatus{CapabilityRefused, portalHotkeyInactive}
		}
	}
	return CapabilityStatus{State: CapabilityAvailable}
}

func (d *portalDesktop) clipboardStatus() CapabilityStatus {
	r := d.remote
	switch {
	case d.clipboardMissing:
		return CapabilityStatus{CapabilityMissing, portalClipboardMissing}
	case r.err != nil:
		return CapabilityStatus{CapabilityRefused, r.err.Error()}
	case r.reconnecting():
		// Temporarily unavailable (screen shield or reconnect backoff); Retry
		// cannot help, the remote feature reconnects by itself.
		return CapabilityStatus{CapabilityWaiting, r.WaitMessage()}
	case r.phase != "ready":
		return CapabilityStatus{CapabilityPending, portalClipboardPending}
	case r.warning != nil:
		return CapabilityStatus{CapabilityAvailable, r.warning.Error()}
	}
	return CapabilityStatus{State: CapabilityAvailable}
}

// Capture needs the portal clipboard plus a granted keyboard. While the
// clipboard is not available, capture follows it without a message of its own.
func (d *portalDesktop) captureStatus(clip CapabilityStatus) CapabilityStatus {
	switch {
	case clip.State == CapabilityMissing:
		return CapabilityStatus{CapabilityMissing, portalCaptureNoClipboard}
	case clip.State != CapabilityAvailable:
		return CapabilityStatus{State: clip.State}
	case !d.remote.keyboard:
		return CapabilityStatus{CapabilityRefused, portalCaptureNoKeyboard}
	}
	return CapabilityStatus{State: CapabilityAvailable}
}

func (d *portalDesktop) DesktopStatus() DesktopStatus {
	s := DesktopStatus{Crostini: desktopCrostini, Wayland: true, PortalCapabilitiesMissing: d.portalCapabilitiesMissing}
	all := func(c CapabilityStatus) { s.Hotkey, s.Clipboard, s.Capture = c, c, c }
	switch {
	case d.manual && !d.closed:
		s.Manual = true
		s.ManualClipboard = true
		s.Message = portalManualMessage
		s.Retry = true
		s.Hotkey = CapabilityStatus{CapabilityMissing, portalHotkeyMissing}
		s.Clipboard = CapabilityStatus{CapabilityMissing, portalClipboardMissing}
		s.Capture = CapabilityStatus{CapabilityMissing, portalCaptureNoClipboard}
		return s
	case d.closed:
		s.Message = "Wayland connection is closed"
		return s
	case d.err != nil:
		s.Message = d.err.Error()
		s.Retry = true
		all(CapabilityStatus{CapabilityRefused, s.Message})
		return s
	case d.client.err != nil:
		s.Message = d.client.err.Error()
		s.Retry = true
		all(CapabilityStatus{CapabilityRefused, s.Message})
		return s
	case d.client.phase != "ready":
		s.Message = portalConnectingMessage
		all(CapabilityStatus{CapabilityPending, s.Message})
		return s
	}
	s.Hotkey = d.hotkeyStatus()
	s.Clipboard = d.clipboardStatus()
	s.Capture = d.captureStatus(s.Clipboard)
	s.Manual = s.Hotkey.State == CapabilityMissing
	// Missing and refused both leave no authorized portal session, so clipboard
	// access goes through the focused control window (design memo §5.3).
	s.ManualClipboard = clipboardFallbackState(s.Clipboard.State)
	s.Ready = s.Hotkey.State == CapabilityAvailable && s.Clipboard.State == CapabilityAvailable && s.Capture.State == CapabilityAvailable
	var missing []string
	for _, c := range s.Capabilities() {
		if c.Retryable() {
			s.Retry = true
		}
		// Capture has no message while it only follows the clipboard.
		if c.Message == "" {
			continue
		}
		switch {
		case c.State == CapabilityMissing:
			missing = append(missing, c.Message)
		case c.State != CapabilityAvailable && s.Message == "":
			// The first capability that needs something from the user leads.
			s.Message = c.Message
		}
	}
	switch {
	case s.Ready:
		s.Message = portalReadyMessage
		if s.Clipboard.Message != "" {
			s.Message += " " + s.Clipboard.Message
		}
	case s.Message == "" && len(missing) > 0:
		// Only permanent gaps remain: the rest works in its degraded mode.
		s.Message = strings.Join(append(missing, portalPartialReadySuffix), " ")
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
	if d.hotkeys.pending == "" && !d.hotkeyMissing {
		for _, h := range d.hotkeys.entries {
			if h.err != nil {
				d.hotkeys.dirty = true
			}
		}
		if d.hotkeys.err != nil {
			d.hotkeys.dirty = true
		}
	}
	if d.clipboardMissing {
		return // Retry cannot add a portal; only a desktop upgrade can.
	}
	if d.remote.phase == "failed" {
		d.retryRemote = true
	}
	if d.remote.phase == "ready" && !d.remote.keyboard {
		// The restore token would bring back the same grant without keyboard;
		// forget it so the portal asks again.
		if d.remote.client.tokens.dir != "" {
			if err := d.remote.client.tokens.Save(""); err != nil {
				d.remote.warning = errors.New("could not reset the saved Wayland permission token")
			}
		}
		d.remote.teardown(errPortalNotAuthorized)
		d.remote.phase = "idle"
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
		if d.remote.reconnecting() {
			return errors.New(d.remote.WaitMessage())
		}
		return errors.New("Wayland keyboard and clipboard permission is pending; allow access in the desktop dialog")
	}
	return nil
}
func (d *portalDesktop) Read() (string, error) {
	if d.useManualClipboard() {
		return d.manualClipboard.Read()
	}
	if err := d.access(); err != nil {
		return "", err
	}
	return d.remote.clip.Read()
}
func (d *portalDesktop) Write(text string) error {
	if d.useManualClipboard() {
		return d.manualClipboard.Write(text)
	}
	if err := d.access(); err != nil {
		return err
	}
	return d.remote.clip.Write(text)
}
func (d *portalDesktop) ReplaceIfUnchanged(old, text string) (bool, error) {
	if d.useManualClipboard() {
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

// clipboardFallbackState reports whether a clipboard capability state leaves
// no authorized portal session, so the focused GTK clipboard must be used.
func clipboardFallbackState(state CapabilityState) bool {
	return state == CapabilityMissing || state == CapabilityRefused
}

// clipboardFallback reports whether clipboard access must go through the
// focused control window: the portal clipboard is missing, or its session was
// refused (design memo §5.3: the capability needs an authorized session). While
// it is pending or waiting the portal path stays in charge (lazy
// authorization), so the manual action uses the portal when it can.
func (d *portalDesktop) clipboardFallback() bool {
	switch {
	case d.closed:
		return false
	case d.manual || d.clipboardMissing:
		return true
	case d.client == nil || d.remote == nil:
		return false
	}
	return clipboardFallbackState(d.DesktopStatus().Clipboard.State)
}

func (d *portalDesktop) useManualClipboard() bool {
	return d.manualClipboard != nil && d.clipboardFallback()
}

// beginCapture picks the selection capture for the current capabilities. With
// no portal clipboard or no granted keyboard, the hotkey translates the text
// already on the clipboard and says so (FallbackNoCopy), the #161 fallback.
func (d *portalDesktop) beginCapture() CaptureTask {
	if d.remote == nil {
		return nil
	}
	if d.useManualClipboard() || (!d.clipboardFallback() && d.remote.phase == "ready" && !d.remote.keyboard) {
		return &portalClipboardCapture{cb: d, present: d.PresentControl, now: d.now}
	}
	return beginPortalCapture(d.remote, d.hotkeys.AllReleased)
}

// PresentControl runs on the UI thread before manual clipboard reads.
func (d *portalDesktop) PresentControl() {
	if d.showManual != nil && d.clipboardFallback() {
		d.showManual()
	}
}
