package platform

// DesktopStatus is optional Clipboard metadata, read only on the UI thread.
type DesktopStatus struct {
	Wayland bool
	Ready   bool
	Retry   bool
	Message string
}

type DesktopSession interface {
	DesktopStatus() DesktopStatus
	RetryDesktop()
}
