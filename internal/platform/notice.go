package platform

import (
	"log"
	"time"
)

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
// app hands it the remembered automatic-request marker (config.json
// "mac_notifications") and a save function that runs on the UI thread.
type NotificationTray interface {
	NoticeTray
	SetNotificationMemory(stored string, save func(string) error)
	// NotificationStatus is the cached permission. Reading it also starts an
	// asynchronous refresh unless the cache is fresh (the Settings page).
	NotificationStatus() AuthStatus
	// NotificationStatusPending reports a refresh still in flight, so the
	// page can read the state once more when it lands.
	NotificationStatusPending() bool
	// RecheckNotificationStatus starts a read now, however recent the last
	// one: the page regained focus, and the user may have just changed the
	// permission in System Settings. NotificationStatusPending then stays
	// true until that read lands, so the page asks once more.
	RecheckNotificationStatus()
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

// Values of config.json "mac_notifications". It is a monotonic marker of the
// one automatic request, independent of what the system reports later: once
// it is not empty it never goes back, so a grant followed by a reset (an
// update, a re-signed build) does not re-arm the automatic request. Any other
// non-empty value (earlier development builds wrote "authorized"/"denied")
// counts as asked.
const (
	StoredNeverAsked = ""
	StoredAsked      = "asked"
)

// decideNotice is the whole permission policy for one notice:
//   - post only when the system allows notifications (never as a way to prompt);
//   - status-only notices and an unavailable center never post or ask;
//   - at most one automatic request ever (Issue #67, option D), and only for a
//     hotkey outcome while the system says "not determined" and the marker
//     says the app never asked. The marker becomes "asked" before the request,
//     so an ignored or dismissed prompt never comes back on its own, and no
//     later system answer clears it. The triggering notice is not replayed.
func decideNotice(status AuthStatus, available bool, stored string, o NoticeOptions) (post, request bool, newStored string) {
	if !available || o.Kind == NoticeStatusOnly {
		return false, false, stored
	}
	if status.allowed() {
		return true, false, stored
	}
	if status == AuthNotDetermined && o.Origin == OriginHotkey && stored == StoredNeverAsked {
		return false, true, StoredAsked
	}
	return false, false, stored
}

// Timing of status refreshes. The native read is asynchronous; these keep the
// UI thread from waiting and the log from filling up.
const (
	// statusFreshFor: a status read this recently needs no new read (Settings
	// page, menu, skipped notices).
	statusFreshFor = 2 * time.Second
	// refreshTimeout: a read with no answer by then counts as failed.
	refreshTimeout = 5 * time.Second
	// actionWait: the longest the tray item waits for a fresh status before
	// acting on the cached one.
	actionWait = 2 * time.Second
)

// refreshBackoff is the wait before each retry of a failed status read; after
// the last one the center is treated as unavailable for the rest of the run
// (log and menu-bar tooltip only), with one log line.
var refreshBackoff = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}

// noticeBridge is the native notification center (macOS). Status is a cached
// snapshot; Refresh and Request are asynchronous and never prompt except
// Request. Post never prompts.
type noticeBridge interface {
	Available() bool
	Status() AuthStatus
	// Refresh starts reading the status and returns its ticket (> 0), or 0 if
	// the read could not even start.
	Refresh() int64
	// Applied is the newest ticket whose answer Status reflects. An answer
	// to an older ticket that arrives late never overwrites a newer one.
	Applied() int64
	// Request asks the system once; false if a request is already in flight.
	// The in-flight bit clears only after the answer is in Status.
	Request() bool
	Post(id, title, body string, sound bool)
	// Failures returns how many asynchronous operations failed so far and the
	// last one's description ("" if none).
	Failures() (count int, last string)
}

// notifier applies the policy over a bridge. All methods run on the UI thread.
type notifier struct {
	bridge       noticeBridge
	stored       string
	save         func(string) error
	logf         func(format string, args ...any)
	now          func() time.Time
	openSettings func() // System Settings > Notifications
	loggedSkip   bool
	failures     int
	lastStatus   AuthStatus

	// Status refreshes: at most one in flight; requests meanwhile coalesce
	// into one more read after it.
	ticket       int64     // in-flight read, 0 if none
	ticketSince  time.Time // when it started
	again        bool      // another read was asked for meanwhile
	freshAt      time.Time // when the last read that started here landed
	failedReads  int       // consecutive failed reads
	retryAt      time.Time // no read before this (backoff)
	broken       bool      // reads keep failing: notices stay in the log/tooltip
	actionWanted bool      // the tray item waits for a fresh status
	actionTicket int64     // the first read started after the click
	actionBy     time.Time // act on the cached status after this
}

func newNotifier(b noticeBridge) *notifier {
	n := &notifier{bridge: b, logf: log.Printf, now: time.Now, lastStatus: AuthUnknown}
	if !b.Available() {
		n.logf("macOS notifications are unavailable (not running from the installed app bundle); messages go to the log and the menu-bar tooltip.")
	}
	return n
}

func (n *notifier) setMemory(stored string, save func(string) error) {
	n.stored, n.save = stored, save
	n.sync()
}

func (n *notifier) usable() bool { return n.bridge.Available() && !n.broken }

func (n *notifier) status() AuthStatus {
	if !n.usable() {
		return AuthUnavailable
	}
	return n.bridge.Status()
}

// settingsStatus is the Settings page's view: the cached status, and a fresh
// read in the background unless the cache is recent.
func (n *notifier) settingsStatus() AuthStatus {
	n.refresh(statusFreshFor)
	return n.status()
}

// recheck is the Settings page regaining focus: a read that landed within
// statusFreshFor may predate a change the user just made in System Settings,
// so start a new one (coalesced with one in flight, bounded by the backoff).
func (n *notifier) recheck() { n.refresh(0) }

// pending reports a status read in flight.
func (n *notifier) pending() bool { return n.ticket != 0 }

// markAsked records the automatic-request marker; false if it could not be
// written. The marker only ever goes from empty to "asked".
func (n *notifier) markAsked() bool {
	if n.stored != StoredNeverAsked {
		return true
	}
	if n.save != nil {
		if err := n.save(StoredAsked); err != nil {
			n.logf("could not remember the notification permission request: %v", err)
			return false
		}
	}
	n.stored = StoredAsked
	return true
}

// refresh asks for a status read unless one that landed within maxAge (0:
// always) is good enough. Reads coalesce: while one is in flight or waiting
// for its backoff, a new request only makes sure one more read follows.
func (n *notifier) refresh(maxAge time.Duration) {
	if !n.usable() {
		return
	}
	now := n.now()
	if maxAge > 0 && !n.freshAt.IsZero() && now.Sub(n.freshAt) < maxAge {
		return
	}
	if n.ticket != 0 || now.Before(n.retryAt) {
		n.again = true
		return
	}
	n.startRead()
}

func (n *notifier) startRead() {
	n.again = false
	t := n.bridge.Refresh()
	if t == 0 {
		n.readFailed()
		return
	}
	n.ticket, n.ticketSince = t, n.now()
	if n.actionWanted && n.actionTicket == 0 {
		n.actionTicket = t
	}
}

// readFailed schedules a bounded retry, or gives up quietly for this run.
func (n *notifier) readFailed() {
	n.ticket = 0
	n.failedReads++
	if n.failedReads > len(refreshBackoff) {
		n.broken, n.again = true, false
		n.logf("macOS notification status could not be read after %d attempts; messages go to the log and the menu-bar tooltip until the app restarts.", n.failedReads)
		return
	}
	n.retryAt = n.now().Add(refreshBackoff[n.failedReads-1])
	n.again = true
}

// notice posts a notification when the policy allows it. The log line and the
// tray status are the caller's job and happen in every case.
func (n *notifier) notice(title, msg string, isError bool, o NoticeOptions) {
	if o.ID == "" {
		o.ID = NoticeIDMisc
	}
	status := n.status()
	post, request, stored := decideNotice(status, n.usable(), n.stored, o)
	switch {
	case post:
		n.bridge.Post(o.ID, title, msg, isError)
	case request:
		// A failed write could not keep the "at most once" promise: do not ask.
		if stored != StoredAsked || !n.markAsked() {
			return
		}
		if n.bridge.Request() {
			n.logf("asking macOS for permission to show notifications (first hotkey notice)")
		}
	case o.Kind == NoticeNotify && n.usable():
		// The cached status may be old (turned on in System Settings): read
		// it again so the next notice uses the current answer.
		n.refresh(statusFreshFor)
		if !n.loggedSkip {
			n.loggedSkip = true
			n.logf("macOS notifications are not allowed (%s); messages go to the log and the menu-bar tooltip.", status)
		}
	}
}

// sync advances status reads, logs status changes and new asynchronous
// failures, and runs a tray-item action once its status is fresh.
// PumpMessages calls it.
func (n *notifier) sync() {
	if !n.bridge.Available() {
		return
	}
	now := n.now()
	if n.ticket != 0 {
		if n.bridge.Applied() >= n.ticket {
			n.ticket, n.failedReads, n.retryAt, n.freshAt = 0, 0, time.Time{}, now
		} else if now.Sub(n.ticketSince) >= refreshTimeout {
			n.readFailed()
		}
	}
	if count, last := n.bridge.Failures(); count > n.failures {
		// Accepted by the center is not the same as delivered; this only reports
		// what the system refused. A refused post may mean the permission
		// changed: read it again (coalesced, bounded by the backoff).
		n.logf("macOS notifications: %d operation(s) failed so far; last: %s", count, last)
		n.failures = count
		n.refresh(statusFreshFor)
	}
	if n.ticket == 0 && n.again && !n.broken && !now.Before(n.retryAt) {
		n.startRead()
	}
	if status := n.status(); status != n.lastStatus {
		if n.lastStatus != AuthUnknown || status != AuthNotDetermined {
			n.logf("macOS notification permission: %s", status)
		}
		n.lastStatus = status
	}
	if n.actionWanted {
		fresh := n.actionTicket != 0 && n.bridge.Applied() >= n.actionTicket
		if n.broken || fresh || !now.Before(n.actionBy) {
			n.act(fresh)
		}
	}
}

// explicitAction is the tray's "Notifications..." item. It does not act on
// the cached status (it may predate the answer to a prompt, or a change in
// System Settings): it starts a read and acts in sync once a read started
// after the click has landed, or after actionWait. Clicks while one waits
// are the same request. Only a landed post-click read may lead to a
// request; on timeout the item opens System Settings.
func (n *notifier) explicitAction() {
	if !n.usable() || n.actionWanted {
		return
	}
	n.actionWanted, n.actionTicket, n.actionBy = true, 0, n.now().Add(actionWait)
	n.refresh(0)
	if n.broken {
		n.act(false)
	}
}

// act asks when a read that started after the click (fresh) says the system
// has never asked, otherwise opens System Settings (the only route after a
// denial). Without a fresh read a cached "not determined" may hide a grant
// or denial made since, so a timeout never requests: System Settings is the
// route that is right in every case.
func (n *notifier) act(fresh bool) {
	n.actionWanted, n.actionTicket = false, 0
	switch s := n.status(); {
	case s == AuthUnavailable:
	case s == AuthNotDetermined && fresh:
		n.markAsked() // best effort: the user asked, so ask anyway
		if !n.bridge.Request() {
			n.logf("a notification permission request is already open")
		}
	default:
		if s == AuthNotDetermined {
			n.logf("the notification permission could not be read in time; opening System Settings instead of asking")
		}
		if n.openSettings != nil {
			n.openSettings()
		}
	}
}

// menuLines is the tray menu's "Notifications..." item and its status line,
// next to the Accessibility lines.
func (n *notifier) menuLines(itemID int) []MenuItem {
	if n.broken {
		return []MenuItem{{Label: "Notifications unavailable (see log)", Disabled: true}}
	}
	s := n.status()
	if s == AuthUnavailable {
		return []MenuItem{{Label: "Notifications unavailable (not an installed app)", Disabled: true}}
	}
	return []MenuItem{
		{ID: itemID, Label: "Notifications...", Disabled: n.actionWanted},
		{Label: "Notifications allowed", Checked: s.allowed(), Disabled: true},
	}
}
