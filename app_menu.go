package main

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/models"
)

// Command IDs of the app-specific tray menu items.
const (
	cmdCheckUpdate  = firstAppCommand + 1
	cmdDoUpdate     = firstAppCommand + 2
	cmdOpenConfig   = firstAppCommand + 3
	cmdPlamoLicense = firstAppCommand + 4
	cmdCancel       = firstAppCommand + 5
	cmdHistoryClear = firstAppCommand + 7
	cmdSettings     = firstAppCommand + 8
	cmdUnloadModel  = firstAppCommand + 10
	cmdQuickstart   = firstAppCommand + 11
	cmdModelBase    = firstAppCommand + 100 // + index into models.Catalog
	cmdHistoryBase  = firstAppCommand + 300 // + index into history (newest first)
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
// menu opens, so availability and the update entry reflect the current state.
func (a *app) buildMenu() []MenuItem {
	items := []MenuItem{{ID: cmdSettings, Label: "Settings..."}, {ID: cmdQuickstart, Label: "Quick start"}}
	if a.translating {
		items = append(items, MenuItem{ID: cmdCancel, Label: "Cancel translation"}, MenuItem{Separator: true})
	}
	items = append(items, MenuItem{ID: cmdUnloadModel, Label: "Unload model", Disabled: !a.canUnloadModel()})
	var modelSub []MenuItem
	modelBusy := a.busy()
	for i, m := range models.Catalog {
		if !m.Installed(a.dir) {
			continue
		}
		modelSub = append(modelSub, MenuItem{
			ID:       cmdModelBase + i,
			Label:    m.Name + " (" + m.SizeLabel + ")",
			Checked:  a.isModelActive(m),
			Disabled: modelBusy,
		})
	}
	if len(modelSub) >= 2 {
		items = append(items, MenuItem{Label: "Translation model", Sub: modelSub})
	}
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
	case id >= cmdHistoryBase:
		a.copyFromHistory(id - cmdHistoryBase)
	case id >= cmdModelBase && id < cmdModelBase+len(models.Catalog):
		a.switchModelFromMenu(id - cmdModelBase)
	case id == cmdUnloadModel:
		a.unloadModel()
	case id == cmdCancel:
		if a.translating {
			log.Print("translation cancelled from the menu")
			a.abortTranslation()
			a.setTransientStatus("Clipboard Translator - translation cancelled")
		}
	case id == cmdSettings:
		a.openSettings()
	case id == cmdQuickstart:
		a.showQuickstart()
	case id == cmdHistoryClear:
		a.hist.Clear()
	case id == cmdCheckUpdate:
		a.startUpdateCheck()
	case id == cmdDoUpdate:
		a.startUpdate()
	case id == cmdOpenConfig:
		a.openConfigFile()
	case id == cmdPlamoLicense:
		if err := a.openURL(plamoLicenseURL); err != nil {
			log.Printf("could not open license URL: %v", err)
			a.tray.Notify("Clipboard Translator", "Could not open "+plamoLicenseURL+": "+err.Error(), true)
		}
	}
}

// openConfigFile opens config.json in the user's associated JSON editor, or
// with a fallback editor on Windows. If both fail, it notifies the user how to
// edit the file manually.
func (a *app) openConfigFile() {
	path := filepath.Join(a.dir, "config.json")
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if err := a.openURL(path); err != nil {
		if a.fallbackEditor != nil && a.fallbackEditor(path) == nil {
			log.Printf("opened config.json with fallback editor after opener failed: %v", err)
			return
		}
		log.Printf("could not open config.json: %v", err)
		a.tray.Notify("Clipboard Translator", "Could not open config.json ("+path+"). You can open it manually in any text editor, edit the hotkey, and restart the app.", true)
	}
}

// switchModelFromMenu switches to models.Catalog[idx] from the tray menu.
func (a *app) switchModelFromMenu(idx int) {
	if a.busy() {
		return
	}
	if idx < 0 || idx >= len(models.Catalog) {
		return
	}
	target := models.Catalog[idx]
	if a.isModelActive(target) {
		return
	}
	e, err := a.checkModelSwitch(target.Key)
	if err != nil {
		log.Printf("could not switch model: %v", err)
		a.tray.Notify("Clipboard Translator", err.Error(), true)
		return
	}
	_ = a.switchModel(e)
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

// noHotkey stands in when no hotkey could be registered at all.
type noHotkey struct{}

// noHotkeyTip is the tooltip while no hotkey is registered.
const noHotkeyTip = "Clipboard Translator - no hotkey (open Settings... from the tray menu to choose another hotkey)"

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
	// silently running on the CPU would misrepresent the active engine in Settings.
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
	ask := a.askYesNo
	if ask == nil {
		ask = askYesNo
	}
	switch detectInstallKind(a.dir, recordedInstallLocation()) {
	case kindInstaller:
		if !ask("Update to " + tag + "?\n\nThis downloads the new version, closes Clipboard Translator and starts it again.") {
			return
		}
		if err := runInstaller(a.dir, tag); err != nil {
			a.tray.Notify("Clipboard Translator", "Could not start the update: "+err.Error(), true)
		}
	case kindScoop:
		showInfo("This copy was installed with Scoop. To update, run this in a terminal:\n\nscoop update clipboard-translator")
	default:
		if ask("This copy was not installed with the installer, so it cannot update itself.\n\nOpen the releases page to download " + tag + "?") {
			if err := a.openURL(releasesPage); err != nil {
				log.Printf("could not open releases page: %v", err)
				a.tray.Notify("Clipboard Translator", "Could not open "+releasesPage+": "+err.Error(), true)
			}
		}
	}
}
