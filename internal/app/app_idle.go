package app

import "time"

// All lifecycle state and timer checks belong to the UI thread.
func (a *app) resetIdleTimer() { a.idleSince = a.timeNow() }

func (a *app) beginServerWork() func() {
	a.serverWork++
	a.resetIdleTimer()
	return func() { a.serverWork--; a.resetIdleTimer() }
}

func (a *app) serverStarted() {
	a.modelUnloaded = false
	a.updateReadyTip()
	a.resetIdleTimer()
}

func (a *app) busy() bool {
	return a.translating || a.pendingRead != nil || a.pendingResult != nil || a.pendingHistory != nil || a.serverWork > 0 || (a.sup != nil && a.sup.Starting()) || a.settingsServing
}

func (a *app) canUnloadModel() bool {
	return a.sup != nil && a.sup.Running() && !a.busy()
}

func (a *app) unloadModel() {
	if !a.canUnloadModel() {
		return
	}
	a.sup.Stop() // Stop reaps the child and clears Exited, so this is never a crash.
	a.modelUnloaded = true
	a.cancelTransientStatus()
	a.updateReadyTip()
	a.tray.SetStatus(a.readyTip)
}

func (a *app) tickIdleUnload() {
	if a.conf.IdleUnloadMinutes == 0 || !a.canUnloadModel() {
		return
	}
	if a.idleSince.IsZero() {
		a.resetIdleTimer()
		return
	}
	if a.timeNow().Sub(a.idleSince) >= time.Duration(a.conf.IdleUnloadMinutes)*time.Minute {
		a.unloadModel()
	}
}
