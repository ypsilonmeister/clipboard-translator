package platform

// DesktopStatus is optional Clipboard metadata, read only on the UI thread.
type DesktopStatus struct {
	// PortalWorkflowUnavailable is independent of the selected Linux display backend.
	PortalWorkflowUnavailable bool
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
