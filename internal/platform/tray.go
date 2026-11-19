package platform

// FirstAppCommand is the lowest ID an app-specific menu item may use. The
// built-in items (Start with Windows, Open log, Quit) use the IDs below it.
const FirstAppCommand = 100

// MenuItem describes one entry of the app-specific part of the tray menu.
type MenuItem struct {
	ID        int // returned by NextCommand when the user picks the item
	Label     string
	Checked   bool
	Disabled  bool
	Separator bool
	Sub       []MenuItem // non-empty: a submenu (ID is ignored)
}

// TrayState represents the working state shown on the tray icon.
type TrayState int

const (
	TrayIdle TrayState = iota
	TrayBusy
	TrayDone
	TrayProblem
)

// TrayReady and TrayWorking are aliases for TrayDone and TrayBusy.
const (
	TrayReady   = TrayDone
	TrayWorking = TrayBusy
)

func (s TrayState) String() string {
	switch s {
	case TrayBusy:
		return "busy"
	case TrayDone:
		return "ready"
	case TrayProblem:
		return "problem"
	default:
		return "idle"
	}
}

// Tray is the notification-area (system tray) presence of the app.
// Implementations live in tray_windows.go and desktop_linux.go. Unsupported
// platforms and failed Windows initialisation use noopTray so callers never need
// to check for nil.
type Tray interface {
	// QuitRequested reports whether the user chose "Quit" in the tray menu.
	QuitRequested() bool
	// SetStatus updates the tray tooltip.
	SetStatus(text string)
	// SetState updates the tray icon to reflect the working state.
	SetState(state TrayState)
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

// CapabilityTray is implemented by trays whose control window can show one
// line per desktop capability (Linux). Others never see the call.
type CapabilityTray interface {
	SetCapabilities(lines []string)
}

type noopTray struct{}

func (noopTray) QuitRequested() bool         { return false }
func (noopTray) SetStatus(string)            {}
func (noopTray) SetState(TrayState)          {}
func (noopTray) Notify(string, string, bool) {}
func (noopTray) SetMenu(func() []MenuItem)   {}
func (noopTray) NextCommand() (int, bool)    { return 0, false }
func (noopTray) Close()                      {}
