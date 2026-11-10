package app

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
	"github.com/ypsilonmeister/clipboard-translator/internal/update"
)

// This file is the app's side of the settings page (internal/settings): it
// answers the page's requests on the UI thread, where the hotkeys and the
// config belong.

const settingsIdle = 5 * time.Minute

func (a *app) ensureRevisions() {
	if a.confRevision == 0 {
		a.confRevision = 1
	}
	if a.profilesRev == 0 {
		a.profilesRev = 1
	}
	if a.autoPasteRev == 0 {
		a.autoPasteRev = 1
	}
	if a.notifyOnCompleteRev == 0 {
		a.notifyOnCompleteRev = 1
	}
	if a.idleUnloadRev == 0 {
		a.idleUnloadRev = 1
	}
	if a.maxCharsRev == 0 {
		a.maxCharsRev = 1
	}
}

// openSettings starts the settings server if it is not running and opens the
// page in the browser. The server closes itself when the page is gone.
func (a *app) openSettings() {
	if a.settings == nil || !a.settings.Running() {
		idle := a.settingsIdle
		if idle <= 0 {
			idle = settingsIdle
		}
		s, err := settings.Start(a.askSettings, a.dl, idle)
		if err != nil {
			log.Printf("settings page: %v", err)
			a.tray.Notify("Clipboard Translator", "Could not start the settings server: "+err.Error()+". You can open config.json from the tray (Open config.json), edit the hotkey there and restart the app.", true)
			return
		}
		a.settings = s
	}
	if err := a.openURL(a.settings.URL()); err != nil {
		log.Printf("open settings browser: %v", err)
		a.tray.Notify("Clipboard Translator", "Could not open Settings in the browser. You can open config.json from the tray (Open config.json), edit the hotkey there and restart the app.", true)
		return
	}
}

func (a *app) closeSettings() {
	if a.settings != nil {
		a.settings.Close()
	}
}

// askSettings runs on a server goroutine: it hands the request to the main loop
// and waits for the answer, giving up at the request's deadline.
func (a *app) askSettings(req settings.Request) settings.Reply {
	select {
	case a.settingsReqs <- req:
	default:
		return settings.Reply{Err: settings.ErrBusy}
	}
	timer := time.NewTimer(time.Until(req.Deadline))
	defer timer.Stop()
	select {
	case rep := <-req.Reply:
		return rep
	case <-timer.C:
		return settings.Reply{Err: settings.ErrBusy}
	}
}

// handleSettingsRequest runs on the UI thread. A request whose deadline passed
// while the loop was busy is dropped: the page was already told it failed.
func (a *app) handleSettingsRequest(req settings.Request) {
	a.settingsServing = true
	defer func() { a.settingsServing = false }()
	if time.Now().After(req.Deadline) {
		req.Reply <- settings.Reply{Err: settings.ErrBusy}
		return
	}
	switch req.Action {
	case "use_model":
		a.startModelSwitch(req)
		return
	case "delete_model":
		a.handleDeleteModelRequest(req)
		return
	case "engine":
		a.startEngineSwitch(req)
		return
	}
	if req.Save == nil {
		req.Reply <- settings.Reply{State: a.settingsState()}
		return
	}
	a.settingsNotice = ""
	warnings, err := a.applySettings(*req.Save)
	req.Reply <- settings.Reply{State: a.settingsState(), Warnings: warnings, Err: err}
}

// settingsState is what the page shows now.
func (a *app) settingsState() settings.State {
	a.ensureRevisions()
	st := settings.State{
		Revision:       a.confRevision,
		Languages:      settings.LanguageOptions(),
		MaxHotkeys:     a.hotkeyLimit,
		WinKey:         settings.WinKeyName(runtime.GOOS),
		Inactive:       a.inactiveHotkeys(),
		NoActiveHotkey: !a.anyHotkeyActive(),
	}
	for i := range a.conf.Profiles {
		actual := a.hotkeyLabel(i)
		if actual != "" && actual != a.conf.Profiles[i].Hotkey {
			if st.ActualHotkeys == nil {
				st.ActualHotkeys = make([]string, len(a.conf.Profiles))
			}
			st.ActualHotkeys[i] = actual
		}
	}
	st.Guide = a.quickstartGuide()
	st.BaseRevision = a.confRevision
	st.AutoPaste = a.conf.AutoPaste
	st.NotifyOnComplete = a.conf.NotifyOnComplete
	st.MaxChars = a.conf.MaxChars
	st.IdleUnloadMinutes = a.conf.IdleUnloadMinutes
	st.LicenseURL = update.PlamoLicenseURL
	st.Notice = a.settingsNotice
	a.fillModelsAndEngines(&st)
	for _, p := range a.conf.Profiles {
		f, err := settings.FormFromProfile(p, runtime.GOOS)
		if err != nil {
			log.Printf("settings page: profile %q: %v", p.Hotkey, err)
			continue
		}
		st.Profiles = append(st.Profiles, f)
	}
	return st
}

// inactiveHotkeys names the hotkeys that are configured but not registered.
func (a *app) inactiveHotkeys() []string {
	var out []string
	if a.hasNoHotkey() && len(a.conf.Profiles) > 0 {
		p := a.conf.Profiles[0]
		out = append(out, fmt.Sprintf("%s (%s): could not be registered (another program may be using it)", p.Hotkey, p.Label()))
	}
	for _, e := range a.extras {
		if e.hk == nil {
			p := a.conf.Profiles[e.profile]
			out = append(out, fmt.Sprintf("%s (%s): %s", p.Hotkey, p.Label(), e.why))
		}
	}
	return out
}

// closeHotkeys releases every registered hotkey.
func (a *app) closeHotkeys() {
	if a.hotkey != nil {
		_ = a.hotkey.Close()
	}
	for _, e := range a.extras {
		if e.hk != nil {
			_ = e.hk.Close()
		}
	}
	a.extras = nil
}

func (a *app) anyHotkeyActive() bool {
	if a.hotkey != nil && !a.hasNoHotkey() {
		return true
	}
	for _, e := range a.extras {
		if e.hk != nil {
			return true
		}
	}
	return false
}

func sameProfiles(a, b []config.Profile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Hotkey != b[i].Hotkey || a[i].Target != b[i].Target || a[i].OutputMode() != b[i].OutputMode() || a[i].InputMode() != b[i].InputMode() || len(a[i].Languages) != len(b[i].Languages) {
			return false
		}
		for j := range a[i].Languages {
			if a[i].Languages[j] != b[i].Languages[j] {
				return false
			}
		}
	}
	return true
}

// applySettings validates the page's settings, registers the hotkeys again,
// saves config.json and reports what is not active. It returns an error, and
// leaves everything as it was, when the settings are invalid or none of the new
// hotkeys can be registered. An unchanged save retries when no hotkey is active.
func (a *app) applySettings(form settings.Form) (warnings []string, err error) {
	a.ensureRevisions()
	hasField := func(field string) bool {
		if len(form.Changed) == 0 {
			return true
		}
		for _, v := range form.Changed {
			if v == field {
				return true
			}
		}
		return false
	}

	if form.BaseRevision > 0 {
		if len(form.Changed) == 0 && form.BaseRevision < a.confRevision {
			return nil, settings.ErrConflict
		}
		if hasField("profiles") && form.BaseRevision < a.profilesRev {
			return nil, settings.ErrConflict
		}
		if hasField("auto_paste") && form.BaseRevision < a.autoPasteRev {
			return nil, settings.ErrConflict
		}
		if hasField("notify_on_complete") && form.BaseRevision < a.notifyOnCompleteRev {
			return nil, settings.ErrConflict
		}
		if hasField("idle_unload_minutes") && form.BaseRevision < a.idleUnloadRev {
			return nil, settings.ErrConflict
		}
		if hasField("max_chars") && form.BaseRevision < a.maxCharsRev {
			return nil, settings.ErrConflict
		}
	}

	cand, err := form.ToConfig(a.conf)
	if err != nil {
		return nil, err
	}
	sameProf := sameProfiles(cand.Profiles, a.conf.Profiles)
	// Let Save recover unchanged hotkeys after another program releases them.
	retryHotkeys := hasField("profiles") && !a.anyHotkeyActive() && sameProf
	if !retryHotkeys && sameProf && cand.AutoPaste == a.conf.AutoPaste && cand.NotifyOnComplete == a.conf.NotifyOnComplete && cand.MaxChars == a.conf.MaxChars && cand.IdleUnloadMinutes == a.conf.IdleUnloadMinutes {
		return a.inactiveHotkeys(), nil
	}

	changedProfiles := hasField("profiles") && !sameProf
	changedAutoPaste := hasField("auto_paste") && cand.AutoPaste != a.conf.AutoPaste
	changedNotifyOnComplete := hasField("notify_on_complete") && cand.NotifyOnComplete != a.conf.NotifyOnComplete
	changedIdleUnload := hasField("idle_unload_minutes") && cand.IdleUnloadMinutes != a.conf.IdleUnloadMinutes
	changedMaxChars := hasField("max_chars") && cand.MaxChars != a.conf.MaxChars

	if retryHotkeys && !changedAutoPaste && !changedNotifyOnComplete && !changedIdleUnload && !changedMaxChars {
		// Pure retry: re-register unchanged hotkeys without touching revisions, config.json, or .bak.
		a.closeHotkeys()
		a.registerStartupHotkey()
		if !a.anyHotkeyActive() {
			a.refreshStatus()
			return nil, errors.New("None of the hotkeys could be registered (another program may be using them), so the previous settings were kept. Choose another hotkey and click Save.")
		}
		a.refreshStatus()
		return a.inactiveHotkeys(), nil
	}

	if retryHotkeys {
		a.closeHotkeys()
		a.registerStartupHotkey()
		if !a.anyHotkeyActive() {
			a.refreshStatus()
			return nil, errors.New("None of the hotkeys could be registered (another program may be using them), so the previous settings were kept. Choose another hotkey and click Save.")
		}
	}

	if !changedProfiles {
		// No hotkey changes: leave the registrations alone. Settings writes
		// profiles even for legacy files; loadConfig prefers that key.
		a.conf.AutoPaste, a.conf.NotifyOnComplete, a.conf.MaxChars = cand.AutoPaste, cand.NotifyOnComplete, cand.MaxChars
		a.conf.IdleUnloadMinutes = cand.IdleUnloadMinutes
		a.conf.ExplicitProfiles = cand.ExplicitProfiles
	} else {
		old := a.conf
		a.closeHotkeys()
		a.conf = cand
		a.registerStartupHotkey()
		if !a.anyHotkeyActive() {
			// Nothing works: do not leave the user without a hotkey because of a typo.
			a.closeHotkeys()
			a.conf = old
			a.registerStartupHotkey()
			a.refreshStatus()
			return nil, errors.New("None of the hotkeys could be registered (another program may be using them), so the previous settings were kept. Choose another hotkey and click Save.")
		}
	}

	a.confRevision++
	if changedIdleUnload {
		a.idleUnloadRev = a.confRevision
		a.resetIdleTimer()
	}
	if changedProfiles {
		a.profilesRev = a.confRevision
	}
	if changedAutoPaste {
		a.autoPasteRev = a.confRevision
	}
	if changedNotifyOnComplete {
		a.notifyOnCompleteRev = a.confRevision
	}
	if changedMaxChars {
		a.maxCharsRev = a.confRevision
	}

	set := map[string]any{"profiles": a.conf.Profiles, "hotkey": a.conf.Profiles[0].Hotkey, "auto_paste": a.conf.AutoPaste, "notify_on_complete": a.conf.NotifyOnComplete, "max_chars": a.conf.MaxChars, "idle_unload_minutes": a.conf.IdleUnloadMinutes}
	if a.conf.Profiles[0].Languages != nil {
		set["languages"] = a.conf.Profiles[0].Languages
	}
	if err := config.UpdateFile(a.dir, set); err != nil {
		log.Printf("could not save config.json: %v", err)
		warnings = append(warnings, "The settings work now but could not be saved to config.json: "+err.Error())
	}
	a.refreshStatus()
	warnings = append(warnings, a.inactiveHotkeys()...)
	log.Printf("settings applied: %d hotkey(s)", len(a.conf.Profiles))
	return warnings, nil
}

// refreshStatus shows the idle tooltip again, unless a translation is running.
func (a *app) refreshStatus() {
	if !a.translating {
		a.cancelTransientStatus()
		a.tray.SetStatus(a.readyTip)
	}
}
