package main

// Tray is the notification-area (system tray) presence of the app.
// The Windows implementation lives in tray_windows.go; other platforms and
// failed initialisation use noopTray so the rest of the program never needs
// to check for nil.
type Tray interface {
	// QuitRequested reports whether the user chose "Quit" in the tray menu.
	QuitRequested() bool
	// SetStatus updates the tray tooltip.
	SetStatus(text string)
	// Notify shows a balloon notification.
	Notify(title, message string, isError bool)
	Close()
}

type noopTray struct{}

func (noopTray) QuitRequested() bool         { return false }
func (noopTray) SetStatus(string)            {}
func (noopTray) Notify(string, string, bool) {}
func (noopTray) Close()                      {}
