package app

import (
	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// notify shows a notice with its macOS classification (Issue #67). Trays that
// do not read the metadata (Windows, Linux) get the plain Notify call they
// always got, so their behaviour does not change.
func (a *app) notify(title, msg string, isError bool, o platform.NoticeOptions) {
	if t, ok := a.tray.(platform.NoticeTray); ok {
		t.NotifyWith(title, msg, isError, o)
		return
	}
	a.tray.Notify(title, msg, isError)
}

// notice is the classification of a skip or failure of this result's job.
func (r result) notice() platform.NoticeOptions {
	return platform.NoticeOptions{Origin: r.origin, ID: platform.NoticeIDHotkey}
}

// switchNotice classifies a model/engine switch failure: the Settings page
// shows its own outcome, so a switch started there only updates the status;
// one started from the tray menu has nothing else to report it.
func (a *app) switchNotice() platform.NoticeOptions {
	o := platform.NoticeOptions{ID: platform.NoticeIDEngine}
	if a.fromSettings {
		o.Kind = platform.NoticeStatusOnly
	}
	return o
}

// engineSuccess classifies "Model: X" / "Translation engine: X": the checked
// menu entry and the Settings page already show the result (maintainer
// decision on Issue #67), so it is status only.
var engineSuccess = platform.NoticeOptions{Kind: platform.NoticeStatusOnly, ID: platform.NoticeIDEngine}

// setupNotificationMemory hands the macOS tray the remembered permission state
// and a way to save it. The save runs on the UI thread, like every tray call.
func (a *app) setupNotificationMemory() {
	t, ok := a.tray.(platform.NotificationTray)
	if !ok {
		return
	}
	t.SetNotificationMemory(a.conf.MacNotifications, func(v string) error {
		if err := config.UpdateFile(a.dir, map[string]any{"mac_notifications": v}); err != nil {
			return err
		}
		a.conf.MacNotifications = v
		return nil
	})
}

// notificationStatus is the Settings page's "notifications" field: "n/a" where
// the tray has no system permission to report (Windows, Linux). Reading it
// starts a background refresh on macOS; pending says one is still in flight.
func (a *app) notificationStatus() (status string, pending bool) {
	if t, ok := a.tray.(platform.NotificationTray); ok {
		s := t.NotificationStatus()
		return s.String(), t.NotificationStatusPending()
	}
	return "n/a", false
}

// recheckNotifications starts a fresh permission read on macOS (the Settings
// page regained focus); the state that follows reports it as pending.
func (a *app) recheckNotifications() {
	if t, ok := a.tray.(platform.NotificationTray); ok {
		t.RecheckNotificationStatus()
	}
}
