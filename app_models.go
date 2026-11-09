package main

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"time"
)

// Model and engine choice for the settings page. Everything here runs on the UI
// thread. Switching restarts llama-server, which blocks the main loop for as
// long as the model takes to load, so the page's request is answered first (with
// the busy state) and the page then polls: while the server starts, the
// supervisor's ui callback calls serviceSettingsWhileBusy, which answers state
// requests only.

// fillModelsAndEngines adds what the model and engine sections show.
func (a *app) fillModelsAndEngines(st *settingsState) {
	active, known := modelForFile(a.conf.ModelFile)
	// One snapshot of the download for the whole state: reading it per model
	// could show "downloading" in one place and "done" in another.
	d := a.dl.State()
	template := templateInstalled(a.dir)
	for _, e := range modelCatalog {
		st.Models = append(st.Models, modelState{
			Key: e.Key, Name: e.Name, Note: e.Note, Size: e.SizeLabel,
			Installed: e.installed(a.dir), Active: known && active.Key == e.Key, Downloading: d.Key == e.Key && d.running(),
			TemplateInstalled: template,
		})
	}
	if !known {
		st.CustomModel = a.conf.ModelFile
	}
	cur := a.runningBackend() // after a CPU fallback, not the configured one
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
