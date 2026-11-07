package main

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"time"
)

// This file is the app's side of the settings page (settings_server.go): it
// answers the page's requests on the UI thread, where the hotkeys and the
// config belong.

const settingsIdle = 5 * time.Minute

// openSettings starts the settings server if it is not running and opens the
// page in the browser. The server closes itself when the page is gone.
func (a *app) openSettings() {
	if a.settings == nil || !a.settings.Running() {
		s, err := startSettingsServer(a.askSettings, a.dl, settingsIdle)
		if err != nil {
			log.Printf("settings page: %v", err)
			a.tray.Notify("Clipboard Translator", "Could not open the settings page: "+err.Error(), true)
			return
		}
		a.settings = s
	}
	a.openURL(a.settings.URL())
}

func (a *app) closeSettings() {
	if a.settings != nil {
		a.settings.Close()
	}
}

// askSettings runs on a server goroutine: it hands the request to the main loop
// and waits for the answer, giving up at the request's deadline.
func (a *app) askSettings(req settingsRequest) settingsReply {
	select {
	case a.settingsReqs <- req:
	default:
		return settingsReply{err: errSettingsBusy}
	}
	timer := time.NewTimer(time.Until(req.deadline))
	defer timer.Stop()
	select {
	case rep := <-req.reply:
		return rep
	case <-timer.C:
		return settingsReply{err: errSettingsBusy}
	}
}

// handleSettingsRequest runs on the UI thread. A request whose deadline passed
// while the loop was busy is dropped: the page was already told it failed.
func (a *app) handleSettingsRequest(req settingsRequest) {
	if time.Now().After(req.deadline) {
		req.reply <- settingsReply{err: errSettingsBusy}
		return
	}
	switch req.action {
	case "use_model":
		a.startModelSwitch(req)
		return
	case "engine":
		a.startEngineSwitch(req)
		return
	}
	if req.save == nil {
		req.reply <- settingsReply{state: a.settingsState()}
		return
	}
	a.settingsNotice = ""
	warnings, err := a.applySettings(*req.save)
	req.reply <- settingsReply{state: a.settingsState(), warnings: warnings, err: err}
}

// settingsState is what the page shows now.
func (a *app) settingsState() settingsState {
	st := settingsState{
		Languages:  languageOptions(),
		MaxHotkeys: a.hotkeyLimit,
		WinKey:     winKeyName(runtime.GOOS),
		Inactive:   a.inactiveHotkeys(),
	}
	st.AutoPaste = a.conf.AutoPaste
	st.MaxChars = a.conf.MaxChars
	st.LicenseURL = plamoLicenseURL
	st.Notice = a.settingsNotice
	a.fillModelsAndEngines(&st)
	for _, p := range a.conf.Profiles {
		f, err := formFromProfile(p, runtime.GOOS)
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
		out = append(out, fmt.Sprintf("%s (%s): could not be registered (another program may be using it)", p.Hotkey, p.label()))
	}
	for _, e := range a.extras {
		if e.hk == nil {
			p := a.conf.Profiles[e.profile]
			out = append(out, fmt.Sprintf("%s (%s): %s", p.Hotkey, p.label(), e.why))
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

func sameProfiles(a, b []Profile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Hotkey != b[i].Hotkey || a[i].Target != b[i].Target || len(a[i].Languages) != len(b[i].Languages) {
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
// hotkeys can be registered. A save that changes nothing does nothing.
func (a *app) applySettings(form settingsForm) (warnings []string, err error) {
	cand, err := form.toConfig(a.conf)
	if err != nil {
		return nil, err
	}
	if sameProfiles(cand.Profiles, a.conf.Profiles) && cand.AutoPaste == a.conf.AutoPaste && cand.MaxChars == a.conf.MaxChars {
		return a.inactiveHotkeys(), nil
	}
	if sameProfiles(cand.Profiles, a.conf.Profiles) {
		// No hotkey changes: leave the registrations alone.
		a.conf.AutoPaste, a.conf.MaxChars = cand.AutoPaste, cand.MaxChars
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
			return nil, errors.New("None of the hotkeys could be registered (another program may be using them), so the previous settings were kept.")
		}
	}
	set := map[string]any{"profiles": cand.Profiles, "hotkey": cand.Profiles[0].Hotkey, "auto_paste": cand.AutoPaste, "max_chars": cand.MaxChars}
	if cand.Profiles[0].Languages != nil {
		set["languages"] = cand.Profiles[0].Languages
	}
	if err := updateConfigFile(a.dir, set); err != nil {
		log.Printf("could not save config.json: %v", err)
		warnings = append(warnings, "The settings work now but could not be saved to config.json: "+err.Error())
	}
	a.refreshStatus()
	warnings = append(warnings, a.inactiveHotkeys()...)
	log.Printf("settings applied: %d hotkey(s)", len(cand.Profiles))
	return warnings, nil
}

// refreshStatus shows the idle tooltip again, unless a translation is running.
func (a *app) refreshStatus() {
	if !a.translating {
		a.tray.SetStatus(a.readyTip)
	}
}
