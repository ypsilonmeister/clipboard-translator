package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Model and engine choice for the settings page. Everything here runs on the UI
// thread. Switching restarts llama-server, which blocks the main loop for as
// long as the model takes to load, so the page's request is answered first (with
// the busy state) and the page then polls: while the server starts, the
// supervisor's ui callback calls serviceSettingsWhileBusy, which answers state
// requests only.

// isModelActive reports whether e is currently the active model in use by the app.
// It checks modelForFile(a.conf.ModelFile) and also checks if the resolved path
// of the configured model file (a.paths.model / a.sup.model) points to the same
// file as the catalog entry (via os.SameFile and case-insensitive cleaned paths).
func (a *app) isModelActive(e modelEntry) bool {
	if active, known := modelForFile(a.conf.ModelFile); known && strings.EqualFold(active.Key, e.Key) {
		return true
	}
	catalogPath := filepath.Clean(e.path(a.dir))
	catInfo, catErr := os.Stat(catalogPath)

	activePaths := []string{a.paths.model}
	if a.sup != nil && a.sup.Running() && a.sup.model != "" {
		activePaths = append(activePaths, a.sup.model)
	}
	// If a.paths.model is not resolved yet, resolve from a.conf.ModelFile.
	if a.paths.model == "" && a.conf.ModelFile != "" {
		if resolved, err := pathWithin(a.dir, a.conf.ModelFile); err == nil {
			activePaths = append(activePaths, resolved)
		}
	}

	for _, p := range activePaths {
		if p == "" {
			continue
		}
		cleanP := filepath.Clean(p)
		if strings.EqualFold(cleanP, catalogPath) {
			return true
		}
		if catErr == nil {
			if pInfo, err := os.Stat(cleanP); err == nil && os.SameFile(pInfo, catInfo) {
				return true
			}
		}
	}
	return false
}

// fillModelsAndEngines adds what the model and engine sections show.
func (a *app) fillModelsAndEngines(st *settingsState) {
	_, known := modelForFile(a.conf.ModelFile)
	// One snapshot of the download for the whole state: reading it per model
	// could show "downloading" in one place and "done" in another.
	d := a.dl.State()
	template := templateInstalled(a.dir)
	for _, e := range modelCatalog {
		installed := e.installed(a.dir)
		hasFile, fileBytes := e.fileInfo(a.dir)
		partial, partialBytes := e.partialInfo(a.dir)
		isActive := a.isModelActive(e)
		isDownloading := d.Key == e.Key && d.running()

		canDelete := (installed || hasFile) && !isActive && !isDownloading
		canDeletePartial := partial && !isDownloading
		var deleteBlocked string
		if isActive {
			deleteBlocked = "active"
		} else if isDownloading {
			deleteBlocked = "downloading"
		}

		st.Models = append(st.Models, modelState{
			Key:               e.Key,
			Name:              e.Name,
			Note:              e.Note,
			Size:              e.SizeLabel,
			Installed:         installed,
			TemplateInstalled: template,
			Active:            isActive,
			Downloading:       isDownloading,
			Partial:           partial,
			PartialBytes:      partialBytes,
			HasFile:           hasFile,
			FileBytes:         fileBytes,
			CanDelete:         canDelete,
			CanDeletePartial:  canDeletePartial,
			DeleteBlocked:     deleteBlocked,
		})
	}
	if !known {
		st.CustomModel = a.conf.ModelFile
	}
	cur := a.runningBackend() // after a CPU fallback, not the configured one
	cfg := currentBackend(a.conf.ServerFile)
	st.ConfiguredEngine = cfg
	st.RunningEngine = cur
	for _, b := range availableBackends(a.dir) {
		st.Engines = append(st.Engines, engineState{Name: b, Label: backendLabel(b), Active: b == cur})
	}
	if d.Key != "" {
		st.Download = &d
	}
}

// serviceSettingsWhileBusy answers the page while the main loop is inside a
// slow call (loading a model, waiting to restart the server). It only serves
// "state" requests, marked busy: anything else would start another slow call
// inside this one, so it is refused.
func (a *app) serviceSettingsWhileBusy(label string) {
	for {
		select {
		case req := <-a.settingsReqs:
			if time.Now().After(req.deadline) || req.action != "" || req.save != nil {
				req.reply <- settingsReply{err: errSettingsBusy}
				continue
			}
			st := a.settingsState()
			st.Busy = label
			req.reply <- settingsReply{state: st}
		default:
			return
		}
	}
}

// startModelSwitch handles "use this model" from the page.
func (a *app) startModelSwitch(req settingsRequest) {
	e, err := a.checkModelSwitch(req.arg)
	if err != nil {
		req.reply <- settingsReply{state: a.settingsState(), err: err}
		return
	}
	if cur, ok := modelForFile(a.conf.ModelFile); ok && cur.Key == e.Key {
		req.reply <- settingsReply{state: a.settingsState()}
		return
	}
	a.settingsNotice = ""
	st := a.settingsState()
	st.Busy = "switching to " + e.Name
	req.reply <- settingsReply{state: st} // answer first: what follows blocks the loop
	if err := a.switchModel(e); err != nil && !errors.Is(err, errQuit) {
		a.settingsNotice = err.Error()
	}
}

func (a *app) checkModelSwitch(key string) (modelEntry, error) {
	e, ok := modelByKey(key)
	if !ok {
		return e, fmt.Errorf("unknown model %q", key)
	}
	switch {
	case a.dl.Downloading(key):
		return e, fmt.Errorf("%s is still being downloaded", e.Name)
	case !e.installed(a.dir):
		return e, fmt.Errorf("%s is not installed; download it first", e.Name)
	}
	if a.paths.template == filepath.Join(a.dir, "models", "chat_template.jinja") {
		if !templateInstalled(a.dir) {
			return e, errors.New("the chat template is missing or incomplete; retry this model's download in Settings to fetch it")
		}
	} else if err := checkFile(a.paths.template); err != nil {
		return e, fmt.Errorf("the configured chat template is missing (%v); check template_file in config.json", err)
	}
	return e, nil
}

// switchModel restarts the server with another installed model. If that does
// not come up, the previous model is put back before the server is started
// again, because the main loop's recovery reads the model from the supervisor.
// config.json changes only once the new model works.
func (a *app) switchModel(e modelEntry) error {
	defer a.beginServerWork()()
	oldModel := a.sup.model
	wasFallback := a.onFallback()
	newConf := a.conf
	newConf.ModelFile = "models/" + e.File
	paths, err := resolvePaths(a.dir, newConf)
	if err != nil {
		return err
	}
	log.Printf("switching model to %s", e.Name)
	a.abortTranslation() // the server it was talking to is about to go away
	a.modelUnloaded = false
	a.updateReadyTip()
	a.sup.Stop()
	a.sup.model = paths.model
	bin := a.paths.server
	layers := a.conf.GPULayers
	label := "loading " + e.Name
	if wasFallback {
		bin = a.paths.fallback
		layers = 0
		label = "loading " + e.Name + " (CPU)"
	}
	// The chosen engine itself, without the CPU fallback of startServer: silently
	// running on the CPU would hide that the model does not fit on the GPU.
	err = a.sup.start(bin, layers, label)
	if err == nil {
		a.conf, a.paths = newConf, paths
		a.saveConfig(map[string]any{"model_file": newConf.ModelFile})
		a.refreshStatus()
		a.tray.Notify("Clipboard Translator", "Model: "+e.Name, false)
		return nil
	}
	if errors.Is(err, errQuit) {
		return err // the main loop sees the quit request
	}
	log.Printf("model %s failed: %v; going back", e.Name, err)
	a.sup.model = oldModel
	restore := a.startServer
	if wasFallback {
		restore = a.startFallback
	}
	if rerr := restore(); rerr != nil {
		if errors.Is(rerr, errQuit) {
			return rerr
		}
		// Neither works: the loop notices the idle supervisor and recovers.
		log.Printf("could not restore the previous model: %v", rerr)
		a.cancelTransientStatus()
		a.tray.SetStatus("Clipboard Translator - server stopped, restarting...")
	} else {
		a.refreshStatus()
	}
	msg := fmt.Sprintf("Could not load %s (%v). It may need more memory than this PC has. The previous model is back in use; a smaller one may work.", e.Name, err)
	a.tray.Notify("Clipboard Translator", msg, true)
	return errors.New(msg)
}

// startEngineSwitch handles "use this engine" from the page.
func (a *app) startEngineSwitch(req settingsRequest) {
	found := false
	for _, b := range availableBackends(a.dir) {
		found = found || b == req.arg
	}
	if !found {
		req.reply <- settingsReply{state: a.settingsState(), err: fmt.Errorf("the %q engine is not installed", req.arg)}
		return
	}
	if a.runningBackend() == req.arg {
		a.switchBackend(req.arg) // at most makes a running fallback the configured engine; nothing restarts
		req.reply <- settingsReply{state: a.settingsState()}
		return
	}
	a.settingsNotice = ""
	st := a.settingsState()
	st.Busy = "switching to " + backendLabel(req.arg)
	req.reply <- settingsReply{state: st}
	a.switchBackend(req.arg)
	// Ask the server that runs: after a failed retry of the configured GPU from
	// the CPU fallback, config.json still names that GPU.
	if !a.sup.Running() || a.runningBackend() != req.arg {
		a.settingsNotice = "The engine could not be switched, so the previous one is still in use. translator.log has the reason."
	}
}

// onDownloadFinished tells the user, on the tray, how a model download ended.
func (a *app) onDownloadFinished(st downloadState) {
	e, _ := modelByKey(st.Key)
	switch st.Phase {
	case "done":
		a.tray.Notify("Clipboard Translator", e.Name+" is downloaded. Open Settings... to use it.", false)
	case "failed":
		a.tray.Notify("Clipboard Translator", "The download of "+e.Name+" failed: "+st.Error, true)
	}
}

// handleDeleteModelRequest handles "delete_model" from the page on the UI thread.
func (a *app) handleDeleteModelRequest(req settingsRequest) {
	if req.kind != "model" && req.kind != "partial" {
		req.reply <- settingsReply{state: a.settingsState(), err: errors.New("kind must be 'model' or 'partial'")}
		return
	}

	e, ok := modelByKey(req.arg)
	if !ok {
		req.reply <- settingsReply{state: a.settingsState(), err: fmt.Errorf("unknown model %q", req.arg)}
		return
	}

	// Refuse the active model ONLY when deleting the model file.
	// Partial files can be removed even for the active model.
	if req.kind == "model" && a.isModelActive(e) {
		req.reply <- settingsReply{state: a.settingsState(), err: fmt.Errorf("%s is currently in use and cannot be deleted", e.Name)}
		return
	}

	// Refuse a model whose download is running or about to start.
	if a.dl != nil {
		release, err := a.dl.ReserveDelete(e.Key)
		if err != nil {
			req.reply <- settingsReply{state: a.settingsState(), err: err}
			return
		}
		defer release()
	}

	targetRel := filepath.Join("models", e.File)
	if req.kind == "partial" {
		targetRel += ".partial"
	}

	targetPath, err := pathWithin(a.dir, targetRel)
	if err != nil {
		req.reply <- settingsReply{state: a.settingsState(), err: err}
		return
	}

	// Lstat first and refuse directories and symlinks. Non-symlink reparse points
	// (OneDrive placeholders, CompactOS) must not be refused (isRemovableFile).
	info, errStat := os.Lstat(targetPath)
	if errStat != nil && !errors.Is(errStat, os.ErrNotExist) {
		req.reply <- settingsReply{state: a.settingsState(), err: sanitizeFileErr(targetRel, errStat)}
		return
	}
	if errStat == nil && !isRemovableFile(info) {
		req.reply <- settingsReply{state: a.settingsState(), err: fmt.Errorf("refusing to delete %s: not a regular file", filepath.ToSlash(targetRel))}
		return
	}

	var freed int64
	var deleted bool
	if errStat == nil {
		if err := os.Remove(targetPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				req.reply <- settingsReply{state: a.settingsState(), err: sanitizeFileErr(targetRel, err)}
				return
			}
			// Another caller removed it before os.Remove: freed stays 0, deleted stays false.
		} else {
			freed = info.Size()
			deleted = true
		}
	}

	if req.kind == "partial" && a.dl != nil {
		a.dl.ClearKey(e.Key)
	}

	var notice string
	label := strings.ToUpper(e.Key)
	if req.kind == "partial" {
		if deleted {
			notice = fmt.Sprintf("Removed the partial download of %s (%s freed).", label, formatBytes(freed))
		} else {
			notice = fmt.Sprintf("Removed the partial download of %s (0 B freed).", label)
		}
	} else {
		if deleted {
			notice = fmt.Sprintf("Deleted %s (%s freed).", label, formatBytes(freed))
		} else {
			notice = fmt.Sprintf("Deleted %s (0 B freed).", label)
		}
	}

	// NOTE: Do not store delete result in a.settingsNotice (Item 3).
	// Return it only in settingsReply.notice.
	st := a.settingsState()
	req.reply <- settingsReply{state: st, notice: notice}
}

func formatBytes(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%d MB", (n+500000)/1000000)
	case n >= 1e3:
		return fmt.Sprintf("%d KB", (n+500)/1000)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func sanitizeFileErr(rel string, err error) error {
	rel = filepath.ToSlash(rel)
	var pe *os.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("could not remove %s: %v", rel, pe.Err)
	}
	return fmt.Errorf("could not remove %s: %v", rel, err)
}
