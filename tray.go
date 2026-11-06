package main

// firstAppCommand is the lowest ID an app-specific menu item may use. The
// built-in items (Start with Windows, Open log, Quit) use the IDs below it.
const firstAppCommand = 100

// MenuItem describes one entry of the app-specific part of the tray menu.
type MenuItem struct {
	ID        int // returned by NextCommand when the user picks the item
	Label     string
	Checked   bool
	Disabled  bool
	Separator bool
	Sub       []MenuItem // non-empty: a submenu (ID is ignored)
}

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
	// SetMenu installs the app-specific items shown above the built-in ones
	// (Start with Windows, Open log, Quit). build runs on the UI thread every
	// time the menu opens, so it can reflect the current state.
	SetMenu(build func() []MenuItem)
	// NextCommand returns the next item the user picked from the SetMenu items.
	NextCommand() (id int, ok bool)
	Close()
}

type noopTray struct{}

func (noopTray) QuitRequested() bool         { return false }
func (noopTray) SetStatus(string)            {}
func (noopTray) Notify(string, string, bool) {}
func (noopTray) SetMenu(func() []MenuItem)   {}
func (noopTray) NextCommand() (int, bool)    { return 0, false }
func (noopTray) Close()                      {}
