package platform

// CapabilityState is the state of one desktop capability (#133 design §5.3).
// Each capability degrades on its own: one that is missing, refused or waiting
// leaves the others working.
type CapabilityState int

const (
	// CapabilityUnknown: not evaluated (no portal backend, or still probing).
	CapabilityUnknown CapabilityState = iota
	// CapabilityAvailable: working now.
	CapabilityAvailable
	// CapabilityPending: connecting, or a consent dialog is (about to be) open.
	CapabilityPending
	// CapabilityWaiting: temporarily unavailable (screen shield, reconnect
	// backoff). It comes back by itself; Retry cannot help.
	CapabilityWaiting
	// CapabilityRefused: the user or the portal refused it, or it failed.
	// Retry Wayland permissions asks again.
	CapabilityRefused
	// CapabilityMissing: this desktop does not provide it. Only a desktop
	// upgrade helps; the app uses the documented fallback.
	CapabilityMissing
)

func (s CapabilityState) String() string {
	switch s {
	case CapabilityAvailable:
		return "available"
	case CapabilityPending:
		return "pending"
	case CapabilityWaiting:
		return "waiting"
	case CapabilityRefused:
		return "refused"
	case CapabilityMissing:
		return "missing"
	}
	return "unknown"
}

// CapabilityStatus is one capability's line in the status surface.
type CapabilityStatus struct {
	State   CapabilityState
	Message string // empty when there is nothing to say beyond State
}

// Retryable reports whether Retry Wayland permissions can change this state.
func (c CapabilityStatus) Retryable() bool { return c.State == CapabilityRefused }

// DesktopStatus is optional Clipboard metadata, read only on the UI thread.
type DesktopStatus struct {
	Crostini bool // Cached at desktop initialization; status reads perform no detection.
	// PortalCapabilitiesMissing records confirmed absence of GlobalShortcuts,
	// RemoteDesktop keyboard support or Clipboard in this backend's evaluation.
	// Connection errors and pending probes are not evidence of absence.
	PortalCapabilitiesMissing bool
	Wayland                   bool
	// Manual means there is no hotkey: the user translates through the menu's
	// Translate clipboard action.
	Manual bool
	// ManualClipboard means clipboard access goes through the focused control
	// window (GTK) because the portal clipboard is missing or its session was
	// refused. With Manual alone the menu action still uses the portal clipboard.
	ManualClipboard bool
	Ready           bool
	Retry           bool
	// ChooseKeys offers "Choose shortcut keys…" (ConfigureShortcuts). Binding is the
	// shortcut state: "", "pending", "bound", "unbound" or "refused".
	ChooseKeys bool
	Binding    string
	Message    string
	// Per-capability state (Wayland only; zero elsewhere).
	Hotkey, Clipboard, Capture CapabilityStatus
}

// Capabilities lists the per-capability states in display order.
func (s DesktopStatus) Capabilities() []NamedCapability {
	return []NamedCapability{{"hotkey", s.Hotkey}, {"clipboard", s.Clipboard}, {"capture", s.Capture}}
}

// NamedCapability pairs a capability name with its status.
type NamedCapability struct {
	Name string
	CapabilityStatus
}

type DesktopSession interface {
	DesktopStatus() DesktopStatus
	RetryDesktop()
}

// ShortcutChooser is implemented by sessions whose portal can open its own
// shortcut chooser (GlobalShortcuts.ConfigureShortcuts).
type ShortcutChooser interface {
	ChooseShortcutKeys() error
}
