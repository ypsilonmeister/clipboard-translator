package platform

// DesktopStatus is optional Clipboard metadata, read only on the UI thread.
type DesktopStatus struct {
	Crostini bool // Cached at desktop initialization; status reads perform no detection.
	// PortalCapabilitiesMissing records confirmed absence of GlobalShortcuts,
	// RemoteDesktop keyboard support or Clipboard in this backend's evaluation.
	// Connection errors and pending probes are not evidence of absence.
	PortalCapabilitiesMissing bool
	Wayland                   bool
	Manual                    bool
	Ready                     bool
	Retry                     bool
	// ChooseKeys offers "Choose shortcut keys…" (ConfigureShortcuts). Binding is the
	// shortcut state: "", "pending", "bound", "unbound" or "refused".
	ChooseKeys bool
	Binding    string
	Message    string
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
