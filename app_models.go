package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/llamaserver"
	"github.com/ypsilonmeister/clipboard-translator/internal/models"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
)

// Model and engine choice for the settings page. Everything here runs on the UI
// thread. Switching restarts llama-server, which blocks the main loop for as
// long as the model takes to load, so the page's request is answered first (with
// the busy state) and the page then polls: while the server starts, the
// supervisor's ui callback calls serviceSettingsWhileBusy, which answers state
// requests only.

// isModelActive reports whether e is currently the active model in use by the app.
// It checks models.ForFile(a.conf.ModelFile) and also checks if the resolved path
// of the configured model file (a.paths.model / a.sup.Model) points to the same
// file as the catalog entry (via os.SameFile and case-insensitive cleaned paths).
func (a *app) isModelActive(e models.Entry) bool {
	if active, known := models.ForFile(a.conf.ModelFile); known && strings.EqualFold(active.Key, e.Key) {
		return true
	}
	catalogPath := filepath.Clean(e.Path(a.dir))
	catInfo, catErr := os.Stat(catalogPath)

	activePaths := []string{a.paths.Model}
	if a.sup != nil && a.sup.Running() && a.sup.Model != "" {
		activePaths = append(activePaths, a.sup.Model)
	}
	// If a.paths.model is not resolved yet, resolve from a.conf.ModelFile.
	if a.paths.Model == "" && a.conf.ModelFile != "" {
		if resolved, err := config.PathWithin(a.dir, a.conf.ModelFile); err == nil {
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
func (a *app) fillModelsAndEngines(st *settings.State) {
	_, known := models.ForFile(a.conf.ModelFile)
	// One snapshot of the download for the whole state: reading it per model
	// could show "downloading" in one place and "done" in another.
	d := a.dl.State()
	for _, e := range models.Catalog {
		installed := e.Installed(a.dir)
		hasFile, fileBytes := e.FileInfo(a.dir)
		partial, partialBytes := e.PartialInfo(a.dir)
		isActive := a.isModelActive(e)
		isDownloading := d.Key == e.Key && d.Running()

		canDelete := (installed || hasFile) && !isActive && !isDownloading
		canDeletePartial := partial && !isDownloading
		var deleteBlocked string
		if isActive {
			deleteBlocked = "active"
		} else if isDownloading {
			deleteBlocked = "downloading"
		}

		st.Models = append(st.Models, settings.ModelState{
			Key:               e.Key,
			Name:              e.Name,
			Note:              e.Note,
			Size:              e.SizeLabel,
			Installed:         installed,
			TemplateInstalled: e.TemplateInstalled(a.dir),
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
	cfg := config.CurrentBackend(a.conf.ServerFile)
	st.ConfiguredEngine = cfg
	st.RunningEngine = cur
	for _, b := range config.AvailableBackends(a.dir) {
		st.Engines = append(st.Engines, settings.EngineState{Name: b, Label: backendLabel(b), Active: b == cur})
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
			if time.Now().After(req.Deadline) || req.Action != "" || req.Save != nil {
				req.Reply <- settings.Reply{Err: settings.ErrBusy}
				continue
			}
			st := a.settingsState()
			st.Busy = label
			req.Reply <- settings.Reply{State: st}
		default:
			return
		}
	}
}

// startModelSwitch handles "use this model" from the page.
func (a *app) startModelSwitch(req settings.Request) {
	e, err := a.checkModelSwitch(req.Arg)
	if err != nil {
		req.Reply <- settings.Reply{State: a.settingsState(), Err: err}
		return
	}
	if cur, ok := models.ForFile(a.conf.ModelFile); ok && cur.Key == e.Key {
		req.Reply <- settings.Reply{State: a.settingsState()}
		return
	}
	a.settingsNotice = ""
	st := a.settingsState()
	st.Busy = "switching to " + e.Name
	req.Reply <- settings.Reply{State: st} // answer first: what follows blocks the loop
	if err := a.switchModel(e); err != nil && !errors.Is(err, llamaserver.ErrQuit) {
		a.settingsNotice = err.Error()
	}
}

func (a *app) checkModelSwitch(key string) (models.Entry, error) {
	e, ok := models.ByKey(key)
	if !ok {
		return e, fmt.Errorf("unknown model %q", key)
	}
	switch {
	case a.dl.Downloading(key):
		return e, fmt.Errorf("%s is still being downloaded", e.Name)
	case !e.Installed(a.dir):
		return e, fmt.Errorf("%s is not installed; download it first", e.Name)
	}
	if models.IsCustomTemplate(a.conf.TemplateFile) {
		if err := config.CheckFile(a.paths.Template); err != nil {
			return e, fmt.Errorf("the configured chat template is missing (%v); check template_file in config.json", err)
		}
	} else if !e.TemplateInstalled(a.dir) {
		return e, errors.New("the chat template is missing or incomplete; retry this model's download in Settings to fetch it")
	}
	return e, nil
}

// switchModel restarts the server with another installed model. If that does
// not come up, the previous model is put back before the server is started
// again, because the main loop's recovery reads the model from the supervisor.
// config.json changes only once the new model works.
func (a *app) switchModel(e models.Entry) error {
	defer a.beginServerWork()()
	oldModel := a.sup.Model
	oldTemplate := a.sup.Template
	wasFallback := a.onFallback()
	newConf := a.conf
	newConf.ModelFile = "models/" + e.File
	saveMap := map[string]any{"model_file": newConf.ModelFile}
	if !models.IsCustomTemplate(a.conf.TemplateFile) {
		tplPath, ok := e.InstalledTemplatePath(a.dir)
		if ok && tplPath == filepath.Join(a.dir, "models", "chat_template.jinja") {
			newConf.TemplateFile = "models/chat_template.jinja"
		} else {
			newConf.TemplateFile = "models/" + e.TemplateFile()
		}
		saveMap["template_file"] = newConf.TemplateFile
	}
	paths, err := config.ResolvePaths(a.dir, newConf)
	if err != nil {
		return err
	}
	if !models.IsCustomTemplate(newConf.TemplateFile) && e.TemplateSha256 != "" {
		if !models.VerifyFileSha256(paths.Template, e.TemplateSha256) {
			return fmt.Errorf("chat template %s does not match expected SHA-256 for %s", filepath.Base(paths.Template), e.Name)
		}
	}
	log.Printf("switching model to %s", e.Name)
	a.abortTranslation() // the server it was talking to is about to go away
	a.modelUnloaded = false
	a.updateReadyTip()
	a.sup.Stop()
	a.sup.Model = paths.Model
	a.sup.Template = paths.Template
	bin := a.paths.Server
	layers := a.conf.GPULayers
	label := "loading " + e.Name
	if wasFallback {
		bin = a.paths.Fallback
		layers = 0
		label = "loading " + e.Name + " (CPU)"
	}
	// The chosen engine itself, without the CPU fallback of startServer: silently
	// running on the CPU would hide that the model does not fit on the GPU.
	err = a.sup.Start(bin, layers, label)
	if err == nil {
		a.conf, a.paths = newConf, paths
		a.saveConfig(saveMap)
		a.refreshStatus()
		a.tray.Notify("Clipboard Translator", "Model: "+e.Name, false)
		return nil
	}
	if errors.Is(err, llamaserver.ErrQuit) {
		return err // the main loop sees the quit request
	}
	log.Printf("model %s failed: %v; going back", e.Name, err)
	a.sup.Model = oldModel
	a.sup.Template = oldTemplate
	restore := a.startServer
	if wasFallback {
		restore = a.startFallback
	}
	msg := fmt.Sprintf("Could not load %s (%v). It may need more memory than this PC has. The previous model is back in use; a smaller one may work.", e.Name, err)
	if rerr := restore(); rerr != nil {
		if errors.Is(rerr, llamaserver.ErrQuit) {
			return rerr
		}
		// Neither works: the loop notices the idle supervisor and recovers.
		log.Printf("could not restore the previous model: %v", rerr)
		a.cancelTransientStatus()
		a.tray.SetStatus("Clipboard Translator - server stopped, restarting...")
		msg = "The translation server stopped. Restarting it..."
	} else {
		a.refreshStatus()
	}
	a.tray.Notify("Clipboard Translator", msg, true)
	return errors.New(msg)
}

// startEngineSwitch handles "use this engine" from the page.
func (a *app) startEngineSwitch(req settings.Request) {
	found := false
	for _, b := range config.AvailableBackends(a.dir) {
		found = found || b == req.Arg
	}
	if !found {
		req.Reply <- settings.Reply{State: a.settingsState(), Err: fmt.Errorf("the %q engine is not installed", req.Arg)}
		return
	}
	if a.runningBackend() == req.Arg {
		a.switchBackend(req.Arg) // at most makes a running fallback the configured engine; nothing restarts
		req.Reply <- settings.Reply{State: a.settingsState()}
		return
	}
	a.settingsNotice = ""
	st := a.settingsState()
	st.Busy = "switching to " + backendLabel(req.Arg)
	req.Reply <- settings.Reply{State: st}
	a.switchBackend(req.Arg)
	// Ask the server that runs: after a failed retry of the configured GPU from
	// the CPU fallback, config.json still names that GPU.
	if !a.sup.Running() || a.runningBackend() != req.Arg {
		a.settingsNotice = "The engine could not be switched, so the previous one is still in use. translator.log has the reason."
	}
}

// onDownloadFinished tells the user, on the tray, how a model download ended.
func (a *app) onDownloadFinished(st models.DownloadState) {
	e, _ := models.ByKey(st.Key)
	switch st.Phase {
	case "done":
		a.tray.Notify("Clipboard Translator", e.Name+" is downloaded. Open Settings... to use it.", false)
	case "failed":
		a.tray.Notify("Clipboard Translator", "The download of "+e.Name+" failed: "+st.Error, true)
	}
}

// handleDeleteModelRequest handles "delete_model" from the page on the UI thread.
func (a *app) handleDeleteModelRequest(req settings.Request) {
	if req.Kind != "model" && req.Kind != "partial" {
		req.Reply <- settings.Reply{State: a.settingsState(), Err: errors.New("kind must be 'model' or 'partial'")}
		return
	}

	e, ok := models.ByKey(req.Arg)
	if !ok {
		req.Reply <- settings.Reply{State: a.settingsState(), Err: fmt.Errorf("unknown model %q", req.Arg)}
		return
	}

	// Refuse the active model ONLY when deleting the model file.
	// Partial files can be removed even for the active model.
	if req.Kind == "model" && a.isModelActive(e) {
		req.Reply <- settings.Reply{State: a.settingsState(), Err: fmt.Errorf("%s is currently in use and cannot be deleted", e.Name)}
		return
	}

	// Refuse a model whose download is running or about to start.
	if a.dl != nil {
		release, err := a.dl.ReserveDelete(e.Key)
		if err != nil {
			req.Reply <- settings.Reply{State: a.settingsState(), Err: err}
			return
		}
		defer release()
	}

	targetRel := filepath.Join("models", e.File)
	if req.Kind == "partial" {
		targetRel += ".partial"
	}

	targetPath, err := config.PathWithin(a.dir, targetRel)
	if err != nil {
		req.Reply <- settings.Reply{State: a.settingsState(), Err: err}
		return
	}

	// Lstat first and refuse directories and symlinks. Non-symlink reparse points
	// (OneDrive placeholders, CompactOS) must not be refused (isRemovableFile).
	info, errStat := os.Lstat(targetPath)
	if errStat != nil && !errors.Is(errStat, os.ErrNotExist) {
		req.Reply <- settings.Reply{State: a.settingsState(), Err: sanitizeFileErr(targetRel, errStat)}
		return
	}
	if errStat == nil && !models.IsRemovableFile(info) {
		req.Reply <- settings.Reply{State: a.settingsState(), Err: fmt.Errorf("refusing to delete %s: not a regular file", filepath.ToSlash(targetRel))}
		return
	}

	var freed int64
	var deleted bool
	if errStat == nil {
		if err := os.Remove(targetPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				req.Reply <- settings.Reply{State: a.settingsState(), Err: sanitizeFileErr(targetRel, err)}
				return
			}
			// Another caller removed it before os.Remove: freed stays 0, deleted stays false.
		} else {
			freed = info.Size()
			deleted = true
		}
	}

	if req.Kind == "partial" && a.dl != nil {
		a.dl.ClearKey(e.Key)
	}

	var notice string
	label := strings.ToUpper(e.Key)
	if req.Kind == "partial" {
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
	req.Reply <- settings.Reply{State: st, Notice: notice}
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
