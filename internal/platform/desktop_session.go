package platform

// DesktopStatus is optional Clipboard metadata, read only on the UI thread.
type DesktopStatus struct {
	// PortalCapabilitiesMissing records confirmed absence of GlobalShortcuts,
	// RemoteDesktop keyboard support or Clipboard in this backend's evaluation.
	// Connection errors and pending probes are not evidence of absence.
	PortalCapabilitiesMissing bool
	Wayland                   bool
	Manual                    bool
	Ready                     bool
	Retry                     bool
	Message                   string
}

type DesktopSession interface {
	DesktopStatus() DesktopStatus
	RetryDesktop()
}
