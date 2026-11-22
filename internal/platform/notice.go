package platform

import "log"

// Notices carry optional metadata (Issue #67). Only the macOS tray reads it:
// Windows and Linux keep showing every plain Notify as before.

// NoticeKind says whether a notice may become a system notification.
type NoticeKind int

const (
	// NoticeNotify may become a notification (the default, as for plain Notify).
	NoticeNotify NoticeKind = iota
	// NoticeStatusOnly only goes to the log and the tray status: the user is
	// already looking at the result, or the line would repeat at every launch.
	NoticeStatusOnly
)

// NoticeOrigin says what caused a notice.
type NoticeOrigin int

const (
	// OriginOther is everything that is not a hotkey outcome: startup,
	// background work, menu clicks, the Settings page.
	OriginOther NoticeOrigin = iota
	// OriginHotkey is the outcome of a hotkey press. Only such a notice may
	// cause the one automatic permission request on macOS.
	OriginHotkey
)

// NoticeOptions is the metadata of one notice.
type NoticeOptions struct {
	Kind   NoticeKind
	Origin NoticeOrigin
	// ID groups notices of one kind: a new notice with the same ID replaces the
	// older one in Notification Center. Empty means NoticeIDMisc.
	ID string
}

// Notice IDs, one per kind of notice, so Notification Center does not fill up.
const (
	NoticeIDCompletion = "completion"     // "Translation ready"
	NoticeIDHotkey     = "hotkey-outcome" // skips and failures of a hotkey press
	NoticeIDServer     = "server"         // "stopped, restarting" is replaced by "back"
	NoticeIDEngine     = "engine"         // model/engine switches and the GPU fallback
	NoticeIDDownload   = "download"
	NoticeIDUpdate     = "update"
	NoticeIDStartup    = "startup"
	NoticeIDGuide      = "guide"
	NoticeIDMisc       = "misc"
)

// NoticeTray is implemented by trays that use the notice metadata (macOS).
// Plain Notify on such a tray means NoticeOptions{} (notify, other origin, misc).
type NoticeTray interface {
	NotifyWith(title, message string, isError bool, o NoticeOptions)
}

// NotificationTray is a NoticeTray that asks the system for permission. The
// app hands it the remembered state (config.json "mac_notifications") and a
// save function that runs on the UI thread.
type NotificationTray interface {
	NoticeTray
	SetNotificationMemory(stored string, save func(string) error)
	NotificationStatus() AuthStatus
}

// AuthStatus is the system's notification permission for this app.
type AuthStatus int

const (
	AuthUnavailable   AuthStatus = iota // no notification center (unbundled binary, other OS)
	AuthUnknown                         // not read from the system yet
	AuthNotDetermined                   // never asked
	AuthDenied
	AuthAuthorized
	AuthProvisional // delivered quietly to Notification Center; counts as allowed
)

// String is the value of the Settings page's "notifications" field.
func (s AuthStatus) String() string {
	switch s {
	case AuthUnknown:
		return "unknown"
	case AuthNotDetermined:
		return "not_asked"
	case AuthDenied:
		return "denied"
	case AuthAuthorized, AuthProvisional:
		return "allowed"
	default:
		return "unavailable"
	}
}

func (s AuthStatus) allowed() bool { return s == AuthAuthorized || s == AuthProvisional }

// Values of config.json "mac_notifications".
const (
	StoredNeverAsked = ""
	StoredAsked      = "asked"
	StoredAuthorized = "authorized"
	StoredDenied     = "denied"
)

// decideNotice is the whole permission policy for one notice:
//   - post only when the system allows notifications (never as a way to prompt);
//   - status-only notices and an unavailable center never post or ask;
//   - at most one automatic request, and only for a hotkey outcome while the
//     system says "not determined" and the app never asked before, or only saw
//     "authorized" (permission reset by an update). The stored value becomes
//     "asked" before the request, so an ignored or dismissed prompt never comes
//     back on its own. The notice that triggered it is not replayed.
func decideNotice(status AuthStatus, available bool, stored string, o NoticeOptions) (post, request bool, newStored string) {
	newStored = stored
	if !available || o.Kind == NoticeStatusOnly {
		return false, false, stored
	}
	if status.allowed() {
		return true, false, stored
	}
	if status == AuthNotDetermined && o.Origin == OriginHotkey && (stored == StoredNeverAsked || stored == StoredAuthorized) {
		return false, true, StoredAsked
	}
	return false, false, stored
}

// storedForStatus is the remembered state after the system reported status.
// Only definite answers are recorded; "asked" stays until one arrives.
func storedForStatus(status AuthStatus, stored string) string {
	switch {
	case status.allowed():
		return StoredAuthorized
	case status == AuthDenied:
		return StoredDenied
	}
	return stored
}

// noticeBridge is the native notification center (macOS). Status is a cached
// snapshot; Refresh and Request are asynchronous and never prompt except
// Request. Post never prompts.
type noticeBridge interface {
	Available() bool
	Status() AuthStatus
	Refresh()
	// Request asks the system once; false if a request is already in flight.
	Request() bool
	Post(id, title, body string, sound bool)
	// Failures returns how many asynchronous operations failed so far and the
	// last one's description ("" if none).
	Failures() (count int, last string)
}

// notifier applies the policy over a bridge. All methods run on the UI thread.
type notifier struct {
	bridge     noticeBridge
	stored     string
	save       func(string) error
	logf       func(format string, args ...any)
	loggedSkip bool
	failures   int
	lastStatus AuthStatus
}

func newNotifier(b noticeBridge) *notifier {
	n := &notifier{bridge: b, logf: log.Printf, lastStatus: AuthUnknown}
	if !b.Available() {
		n.logf("macOS notifications are unavailable (not running from the installed app bundle); messages go to the log and the menu-bar tooltip.")
	}
	return n
}

func (n *notifier) setMemory(stored string, save func(string) error) {
	n.stored, n.save = stored, save
	n.sync()
}

func (n *notifier) status() AuthStatus {
	if !n.bridge.Available() {
		return AuthUnavailable
	}
	return n.bridge.Status()
}

// remember saves a new stored value; false if it could not be written.
func (n *notifier) remember(v string) bool {
	if v == n.stored {
		return true
	}
	if n.save != nil {
		if err := n.save(v); err != nil {
			n.logf("could not remember the notification permission state %q: %v", v, err)
			return false
		}
	}
	n.stored = v
	return true
}

// notice posts a notification when the policy allows it. The log line and the
// tray status are the caller's job and happen in every case.
func (n *notifier) notice(title, msg string, isError bool, o NoticeOptions) {
	if o.ID == "" {
		o.ID = NoticeIDMisc
	}
	status := n.status()
	post, request, stored := decideNotice(status, n.bridge.Available(), n.stored, o)
	switch {
	case post:
		n.bridge.Post(o.ID, title, msg, isError)
	case request:
		// A failed write could not keep the "at most once" promise: do not ask.
		if !n.remember(stored) {
			return
		}
		if n.bridge.Request() {
			n.logf("asking macOS for permission to show notifications (first hotkey notice)")
		}
	case o.Kind == NoticeNotify && n.bridge.Available() && !n.loggedSkip:
		n.loggedSkip = true
		n.logf("macOS notifications are not allowed (%s); messages go to the log and the menu-bar tooltip.", status)
	}
}

// sync records definite system answers and logs new asynchronous failures.
// PumpMessages calls it.
func (n *notifier) sync() {
	if !n.bridge.Available() {
		return
	}
	status := n.bridge.Status()
	if status != n.lastStatus {
		if n.lastStatus != AuthUnknown || status != AuthNotDetermined {
			n.logf("macOS notification permission: %s", status)
		}
		n.lastStatus = status
	}
	if v := storedForStatus(status, n.stored); v != n.stored {
		n.remember(v)
	}
	if count, last := n.bridge.Failures(); count > n.failures {
		// Accepted by the center is not the same as delivered; this only reports
		// what the system refused.
		n.logf("macOS notifications: %d operation(s) failed so far; last: %s", count, last)
		n.failures = count
		n.bridge.Refresh()
	}
}

// explicitAction is the tray's "Notifications..." item: ask when the system
// has never asked, otherwise open System Settings (the only route after a
// denial). It reports whether System Settings should be opened.
func (n *notifier) explicitAction() (openSettings bool) {
	switch n.status() {
	case AuthUnavailable:
		return false
	case AuthNotDetermined:
		n.remember(StoredAsked) // best effort: the user asked, so ask anyway
		if !n.bridge.Request() {
			n.logf("a notification permission request is already open")
		}
		return false
	case AuthUnknown:
		n.bridge.Refresh()
		return true
	default:
		return true
	}
}

// menuLines is the tray menu's "Notifications..." item and its status line,
// next to the Accessibility lines.
func (n *notifier) menuLines(itemID int) []MenuItem {
	s := n.status()
	if s == AuthUnavailable {
		return []MenuItem{{Label: "Notifications unavailable (not an installed app)", Disabled: true}}
	}
	return []MenuItem{
		{ID: itemID, Label: "Notifications..."},
		{Label: "Notifications allowed", Checked: s.allowed(), Disabled: true},
	}
}
