//go:build !windows

package platform

// NewCursorHint returns a no-op on desktops without the indicator, including
// Wayland, where the portals do not expose global pointer coordinates.
func NewCursorHint(enabled bool) CursorHint { return noopCursorHint{} }
