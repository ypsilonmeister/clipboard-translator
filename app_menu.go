package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"
)

// Command IDs of the app-specific tray menu items.
const (
	cmdCheckUpdate      = firstAppCommand + 1
	cmdDoUpdate         = firstAppCommand + 2
	cmdOpenConfig       = firstAppCommand + 3
	cmdPlamoLicense     = firstAppCommand + 4
	cmdCancel           = firstAppCommand + 5
	cmdAutoPaste        = firstAppCommand + 6
	cmdHistoryClear     = firstAppCommand + 7
	cmdSettings         = firstAppCommand + 8
	cmdNotifyOnComplete = firstAppCommand + 9
	cmdUnloadModel      = firstAppCommand + 10
	cmdHotkeyBase       = firstAppCommand + 100 // + index into hotkeyChoices
	cmdBackendBase      = firstAppCommand + 200 // + index into availableBackends
	cmdHistoryBase      = firstAppCommand + 300 // + index into history (newest first)
	cmdLanguageBase     = firstAppCommand + 400 // + index into languagePairChoices
)

func backendLabel(name string) string {
	switch name {
	case "cpu":
		return "CPU"
	case "cuda":
		return "GPU (CUDA)"
	case "vulkan":
		return "GPU (Vulkan)"
	case "metal":
		return "GPU (Metal)"
	}
	return name
}

func versionLabel() string {
	if versionParts(version) == nil {
		return "Clipboard Translator (development build)"
	}
	return "Clipboard Translator " + version
}

// buildMenu is the app-specific part of the tray menu. It runs each time the
// menu opens, so checkmarks and the update entry reflect the current state.
func (a *app) buildMenu() []MenuItem {
	var items []MenuItem
	if a.translating {
		items = append(items, MenuItem{ID: cmdCancel, Label: "Cancel translation"}, MenuItem{Separator: true})
	}
	items = append(items, MenuItem{ID: cmdUnloadModel, Label: "Unload model", Disabled: !a.canUnloadModel()})
	var recent []MenuItem
	for i, e := range a.hist.Items() {
		recent = append(recent, MenuItem{ID: cmdHistoryBase + i, Label: historyLabel(e)})
	}
	if len(recent) == 0 {
		recent = []MenuItem{{Label: "(nothing yet)", Disabled: true}}
	} else {
		recent = append(recent, MenuItem{Separator: true}, MenuItem{ID: cmdHistoryClear, Label: "Clear history"})
	}
	items = append(items, MenuItem{Label: "Recent translations (click to copy)", Sub: recent})
	items = append(items, MenuItem{ID: cmdSettings, Label: "Settings..."})
	items = append(items, MenuItem{ID: cmdAutoPaste, Label: "Paste automatically after translating", Checked: a.conf.AutoPaste})
	items = append(items, MenuItem{ID: cmdNotifyOnComplete, Label: "Show notification when translation finishes", Checked: a.conf.NotifyOnComplete})

	var hotkeys []MenuItem
	none := a.hasNoHotkey()
	for i, h := range hotkeyChoices(a.conf.Hotkey) {
		hotkeys = append(hotkeys, MenuItem{ID: cmdHotkeyBase + i, Label: h, Checked: !none && strings.EqualFold(h, a.conf.Hotkey)})
	}
	items = append(items, MenuItem{Label: "Hotkey", Sub: hotkeys})

	if t := a.conf.Profiles[0].Target; t != "" {
		// The first profile has no pair to pick; it is edited in config.json.
		items = append(items, MenuItem{Label: "Languages: first hotkey translates " + a.conf.Profiles[0].label() + " (config.json)", Disabled: true})
	} else {
		var pairs []MenuItem
		for i, p := range languagePairChoices(a.conf.Languages) {
			pairs = append(pairs, MenuItem{ID: cmdLanguageBase + i, Label: pairLabel(p), Checked: samePair(p, a.conf.Languages)})
		}
		items = append(items, MenuItem{Label: "Languages", Sub: pairs})
	}

	// Only offer a choice when more than one build is installed.
	if backends := availableBackends(a.dir); len(backends) > 1 {
		cur := a.runningBackend() // after a CPU fallback, not the configured one
		var sub []MenuItem
		for i, b := range backends {
			sub = append(sub, MenuItem{ID: cmdBackendBase + i, Label: backendLabel(b), Checked: b == cur})
		}
		items = append(items, MenuItem{Label: "Translation engine", Sub: sub})
	}

	items = append(items, MenuItem{Separator: true})
	switch {
	case a.checking:
		items = append(items, MenuItem{Label: "Checking for updates...", Disabled: true})
	case a.latest.Tag != "" && isNewerVersion(a.latest.Tag, version):
		items = append(items, MenuItem{ID: cmdDoUpdate, Label: "Update to " + a.latest.Tag + "..."})
	default:
		items = append(items, MenuItem{ID: cmdCheckUpdate, Label: "Check for updates"})
	}
	items = append(items,
		MenuItem{ID: cmdOpenConfig, Label: "Open config.json"},
		MenuItem{Separator: true},
		MenuItem{ID: cmdPlamoLicense, Label: "Built with PLaMo (license)"},
		MenuItem{Label: versionLabel(), Disabled: true},
	)
	return items
}

// handleCommand runs one app-specific menu command. It runs on the UI thread.
func (a *app) handleCommand(id int) {
	switch {
	case id >= cmdLanguageBase:
		if choices := languagePairChoices(a.conf.Languages); id-cmdLanguageBase < len(choices) {
			a.changeLanguages(choices[id-cmdLanguageBase])
		}
	case id >= cmdHistoryBase:
		a.copyFromHistory(id - cmdHistoryBase)
	case id >= cmdHotkeyBase && id < cmdBackendBase:
		if choices := hotkeyChoices(a.conf.Hotkey); id-cmdHotkeyBase < len(choices) {
			a.changeHotkey(choices[id-cmdHotkeyBase])
		}
	case id >= cmdBackendBase:
		if backends := availableBackends(a.dir); id-cmdBackendBase < len(backends) {
			a.switchBackend(backends[id-cmdBackendBase])
		}
	case id == cmdUnloadModel:
		a.unloadModel()
	case id == cmdCancel:
		if a.translating {
			log.Print("translation cancelled from the menu")
			a.abortTranslation()
			a.setTransientStatus("Clipboard Translator - translation cancelled")
		}
	case id == cmdAutoPaste:
		a.ensureRevisions()
		a.conf.AutoPaste = !a.conf.AutoPaste
		a.confRevision++
		a.autoPasteRev = a.confRevision
		a.saveConfig(map[string]any{"auto_paste": a.conf.AutoPaste})
		if a.conf.AutoPaste {
			a.tray.Notify("Clipboard Translator", "The translation will be pasted into the window you selected the text in.", false)
		}
	case id == cmdNotifyOnComplete:
		a.ensureRevisions()
		a.conf.NotifyOnComplete = !a.conf.NotifyOnComplete
		a.confRevision++
		a.notifyOnCompleteRev = a.confRevision
		a.saveConfig(map[string]any{"notify_on_complete": a.conf.NotifyOnComplete})
	case id == cmdSettings:
		a.openSettings()
	case id == cmdHistoryClear:
		a.hist.Clear()
	case id == cmdCheckUpdate:
		a.startUpdateCheck()
	case id == cmdDoUpdate:
		a.startUpdate()
	case id == cmdOpenConfig:
		openTarget(filepath.Join(a.dir, "config.json"))
	case id == cmdPlamoLicense:
		openTarget(plamoLicenseURL)
	}
}

// copyFromHistory puts the translation of history entry i (newest first) on the
// clipboard again.
func (a *app) copyFromHistory(i int) {
	items := a.hist.Items()
	if i < 0 || i >= len(items) || a.cb == nil {
		return
	}
	if err := a.cb.Write(items[i].Translated); err != nil {
		log.Printf("could not copy from history: %v", err)
		a.tray.Notify("Clipboard Translator", "Could not copy to the clipboard: "+err.Error(), true)
		return
	}
	a.tray.Notify("Clipboard Translator", "Copied. Paste with "+pasteShortcut()+".", false)
}

// saveConfig writes changed keys to config.json and tells the user if that fails.
func (a *app) saveConfig(set map[string]any) {
	if err := updateConfigFile(a.dir, set); err != nil {
		log.Printf("could not save config.json: %v", err)
		a.tray.Notify("Clipboard Translator", "The change works now but could not be saved to config.json: "+err.Error(), true)
	}
}

// changeLanguages switches the language pair. Nothing needs restarting: the
// languages are sent with every request. A translation already running keeps its
// own direction.
func (a *app) changeLanguages(pair []string) {
	if samePair(pair, a.conf.Languages) || a.conf.Profiles[0].Target != "" {
		return
	}
	a.ensureRevisions()
	a.conf.Languages = append([]string(nil), pair...)
	a.conf.Profiles[0].Languages = append([]string(nil), pair...)
	a.confRevision++
	a.profilesRev = a.confRevision
	if a.conf.explicitProfiles {
		a.saveConfig(map[string]any{"profiles": a.conf.Profiles})
	} else {
		a.saveConfig(map[string]any{"languages": a.conf.Languages})
	}
	log.Printf("languages changed to %s/%s", pair[0], pair[1])
	a.tray.Notify("Clipboard Translator", "Languages: "+pairLabel(pair)+".", false)
}

// changeHotkey re-registers the hotkey. If the new one is taken, the old one
// is restored and nothing is saved.
func (a *app) changeHotkey(spec string) {
	none := a.hasNoHotkey()
	if !none && strings.EqualFold(spec, a.conf.Hotkey) {
		return // with no hotkey registered, picking the configured one is a retry
	}
	// A profile whose hotkey is not registered (over the OS limit, or taken when
	// the app started) does not hold its key with the OS, so registering succeeds
	// but the saved file would fail loadConfig at the next start. Check before
	// anything is closed or written.
	if i, dup := a.conf.otherProfileWithHotkey(spec); dup {
		log.Printf("hotkey %s: already used by profiles[%d]", spec, i)
		msg := fmt.Sprintf("%s is already used by another hotkey (%s). Keeping %s.", spec, a.conf.Profiles[i].Hotkey, a.conf.Hotkey)
		if none {
			msg = fmt.Sprintf("%s is already used by another hotkey (%s). Pick a different one.", spec, a.conf.Profiles[i].Hotkey)
		}
		a.tray.Notify("Clipboard Translator", msg, true)
		return
	}
	_ = a.hotkey.Close()
	nh, err := a.newHotkey(spec)
	if err != nil {
		log.Printf("hotkey %s: %v", spec, err)
		if none {
			// Nothing to restore: stay without a hotkey and keep the menu usable.
			a.hotkey = noHotkey{}
			a.tray.Notify("Clipboard Translator", fmt.Sprintf("Could not use %s (already taken?). No hotkey is active; pick another one from the menu.", spec), true)
			return
		}
		old, err2 := a.newHotkey(a.conf.Hotkey)
		if err2 != nil {
			a.failed(fmt.Errorf("Could not register %s (%v) and could not restore %s: %v", spec, err, a.conf.Hotkey, err2))
			a.tray.Notify("Clipboard Translator", "The hotkey is not registered. Pick one from the menu or restart the app.", true)
			a.hotkey = noHotkey{}
			a.updateReadyTip()
			a.tray.SetStatus(a.readyTip)
			return
		}
		a.hotkey = old
		a.tray.Notify("Clipboard Translator", fmt.Sprintf("Could not use %s (already taken?). Keeping %s.", spec, a.conf.Hotkey), true)
		return
	}
	a.ensureRevisions()
	a.hotkey = nh
	a.conf.Hotkey = spec
	a.conf.Profiles[0].Hotkey = spec
	a.confRevision++
	a.profilesRev = a.confRevision
	a.updateReadyTip()
	a.tray.SetStatus(a.readyTip)
	if a.conf.explicitProfiles {
		a.saveConfig(map[string]any{"profiles": a.conf.Profiles})
	} else {
		a.saveConfig(map[string]any{"hotkey": spec})
	}
	log.Printf("hotkey changed to %s", spec)
	a.tray.Notify("Clipboard Translator", "Hotkey is now "+spec+".", false)
}

// noHotkey stands in when no hotkey could be registered at all.
type noHotkey struct{}

// noHotkeyTip is the tooltip while no hotkey is registered.
const noHotkeyTip = "Clipboard Translator - no hotkey (choose one in the tray menu)"

// hasNoHotkey reports whether the app is running without a registered hotkey.
func (a *app) hasNoHotkey() bool {
	_, none := a.hotkey.(noHotkey)
	return none
}

func (noHotkey) Pressed() (bool, error) { return false, nil }
func (noHotkey) Close() error           { return nil }

// switchBackend restarts the server with another installed llama.cpp build. If
// that does not come up, the previous settings are restored. What counts as
// "another" build is decided by the server that runs, not by config.json: after
// a CPU fallback, choosing the configured GPU tries it again.
func (a *app) switchBackend(name string) {
	defer a.beginServerWork()()
	if name == a.runningBackend() {
		if name != currentBackend(a.conf.ServerFile) {
			a.keepRunningBackend(name)
		}
		return
	}
	// When the CPU fallback is what runs, the preferred engine has already
	// failed once; going back straight to the CPU saves loading the model on it
	// again just to see it fail.
	wasFallback := a.onFallback()
	newConf := a.conf
	newConf.ServerFile, newConf.GPULayers = backendSettings(name)
	paths, err := resolvePaths(a.dir, newConf)
	if err != nil {
		a.tray.Notify("Clipboard Translator", "Could not switch the engine: "+err.Error(), true)
		return
	}
	log.Printf("switching engine to %s", name)
	a.abortTranslation() // the server it was talking to is about to go away
	a.modelUnloaded = false
	a.updateReadyTip()
	a.sup.Stop()
	// Start the chosen build itself, without the CPU fallback of startServer:
	// silently running on the CPU would make the menu check mark a lie.
	err = a.sup.start(paths.server, newConf.GPULayers, "switching engine")
	if err == nil {
		a.conf, a.paths = newConf, paths
		a.activeBackend = name
		a.cancelTransientStatus()
		a.tray.SetStatus(a.readyTip)
		a.saveConfig(map[string]any{"server_file": a.conf.ServerFile, "gpu_layers": a.conf.GPULayers})
		a.tray.Notify("Clipboard Translator", "Translation engine: "+backendLabel(name), false)
		return
	}
	if errors.Is(err, errQuit) {
		return // the main loop sees the quit request
	}
	log.Printf("engine %s failed: %v; going back", name, err)
	restore := a.startServer
	if wasFallback {
		restore = a.startFallback
	}
	if err := restore(); err != nil {
		if errors.Is(err, errQuit) {
			return // the main loop sees the quit request
		}
		// Neither build runs, so nothing is monitoring a server. Say so and
		// leave it to the main loop, which sees the supervisor is idle and
		// retries through the bounded restart policy (or quits cleanly).
		log.Printf("could not restore the previous engine: %v", err)
		a.cancelTransientStatus()
		a.tray.SetStatus("Clipboard Translator - server stopped, restarting...")
		a.tray.Notify("Clipboard Translator", "Could not switch to "+backendLabel(name)+", and the previous engine did not start either. Trying to recover the translation server...", true)
		return
	}
	a.cancelTransientStatus()
	a.tray.SetStatus(a.readyTip)
	a.tray.Notify("Clipboard Translator", "Could not switch to "+backendLabel(name)+"; keeping the previous engine.", true)
}

// keepRunningBackend makes the engine that runs now, the CPU fallback, the
// configured one, so the next start does not try the failed engine first.
// Nothing is restarted.
func (a *app) keepRunningBackend(name string) {
	conf := a.conf
	conf.ServerFile, conf.GPULayers = backendSettings(name)
	paths, err := resolvePaths(a.dir, conf)
	if err != nil {
		a.tray.Notify("Clipboard Translator", "Could not switch the engine: "+err.Error(), true)
		return
	}
	a.conf, a.paths = conf, paths
	a.activeBackend = name
	a.saveConfig(map[string]any{"server_file": a.conf.ServerFile, "gpu_layers": a.conf.GPULayers})
	log.Printf("engine %s (already running) is now the configured one", name)
	a.tray.Notify("Clipboard Translator", "Translation engine: "+backendLabel(name), false)
}

type updateResult struct {
	info releaseInfo
	err  error
}

// startUpdateCheck asks GitHub for the latest release in the background. The
// answer comes back on a.updates and is handled by the main loop.
func (a *app) startUpdateCheck() {
	if a.checking {
		return
	}
	if versionParts(version) == nil {
		a.tray.Notify("Clipboard Translator", "This is a development build; it has no version to compare.", false)
		return
	}
	a.checking = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		info, err := fetchLatest(ctx, latestReleaseAPI)
		a.updates <- updateResult{info, err}
	}()
}

func (a *app) onUpdateResult(u updateResult) {
	a.checking = false
	switch {
	case u.err != nil:
		log.Printf("update check failed: %v", u.err)
		a.tray.Notify("Clipboard Translator", "Could not check for updates: "+u.err.Error(), true)
	case isNewerVersion(u.info.Tag, version):
		a.latest = u.info
		log.Printf("update available: %s (running %s)", u.info.Tag, version)
		a.tray.Notify("Clipboard Translator", u.info.Tag+" is available. Open the tray menu and choose Update.", false)
	default:
		a.latest = releaseInfo{}
		a.tray.Notify("Clipboard Translator", "You have the latest version ("+version+").", false)
	}
}

// startUpdate updates the way this copy was installed: through install.ps1, by
// pointing at Scoop, or by opening the releases page for a hand-extracted zip.
func (a *app) startUpdate() {
	tag := a.latest.Tag
	switch detectInstallKind(a.dir, recordedInstallLocation()) {
	case kindInstaller:
		if !askYesNo("Update to " + tag + "?\n\nThis downloads the new version, closes Clipboard Translator and starts it again.") {
			return
		}
		if err := runInstaller(a.dir, tag); err != nil {
			a.tray.Notify("Clipboard Translator", "Could not start the update: "+err.Error(), true)
		}
	case kindScoop:
		showInfo("This copy was installed with Scoop. To update, run this in a terminal:\n\nscoop update clipboard-translator")
	default:
		if askYesNo("This copy was not installed with the installer, so it cannot update itself.\n\nOpen the releases page to download " + tag + "?") {
			openTarget(releasesPage)
		}
	}
}
