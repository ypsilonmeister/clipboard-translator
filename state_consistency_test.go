package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #60: a settings page save that changes neither hotkeys nor languages
// still writes "profiles", which loadConfig prefers; later another Settings page changes must
// then be saved there too, or they are lost at the next start.

// settingsOnlySave saves the page's current form on a legacy config.json (no
// "profiles") with edit applied, and checks no hotkey was registered again.
func settingsOnlySave(t *testing.T, edit func(*settingsForm)) *app {
	t.Helper()
	a, _, _ := newTestApp(t)
	// A key this version does not know must survive every save.
	legacy := fmt.Sprintf(`{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"], "port": %d, "x_custom": 1}`, a.conf.Port)
	if err := os.WriteFile(filepath.Join(a.dir, "config.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	a.registerStartupHotkey()
	regs := 0
	a.newHotkey = func(spec string) (Hotkey, error) { regs++; return fakeHotkey{spec}, nil }

	form := a.settingsState().settingsForm
	edit(&form)
	if _, err := a.applySettings(form); err != nil {
		t.Fatal(err)
	}
	if regs != 0 {
		t.Fatalf("a settings-only save must not register hotkeys again, got %d registrations", regs)
	}
	raw, _ := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if !strings.Contains(string(raw), `"profiles"`) {
		t.Fatalf("precondition: the save writes profiles: %s", raw)
	}
	return a
}

func checkReloaded(t *testing.T, a *app) Config {
	t.Helper()
	c, err := loadConfig(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if !strings.Contains(string(raw), `"x_custom": 1`) {
		t.Errorf("an unknown key was lost: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(a.dir, "config.json.bak")); err != nil {
		t.Errorf("config.json.bak must be written: %v", err)
	}
	return c
}

func TestSettingsLanguagesSurviveRestartAfterSettingsOnlySave(t *testing.T) {
	a := settingsOnlySave(t, func(f *settingsForm) { f.AutoPaste = !f.AutoPaste })
	changeSettingsLanguages(t, a, []string{"ja", "ko"})
	c := checkReloaded(t, a)
	if !samePair(c.Profiles[0].Languages, []string{"ja", "ko"}) || !samePair(c.Languages, []string{"ja", "ko"}) {
		t.Fatalf("Settings change lost after reload: memory=%v disk=%v explicit=%v", a.conf.Languages, c.Profiles[0].Languages, a.conf.explicitProfiles)
	}
	if !c.AutoPaste {
		t.Error("auto_paste from the settings page was lost")
	}
}

func TestSettingsHotkeySurvivesRestartAfterSettingsOnlySave(t *testing.T) {
	a := settingsOnlySave(t, func(f *settingsForm) { f.MaxChars = 2000 })
	changeSettingsHotkey(t, a, "Ctrl+Shift+Y")
	if a.conf.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("precondition: hotkey changed in memory, got %q", a.conf.Hotkey)
	}
	c := checkReloaded(t, a)
	if c.Profiles[0].Hotkey != "Ctrl+Shift+Y" || c.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("Settings change lost after reload: memory=%q disk=%q explicit=%v", a.conf.Hotkey, c.Profiles[0].Hotkey, a.conf.explicitProfiles)
	}
	if c.MaxChars != 2000 {
		t.Errorf("max_chars from the settings page was lost: %d", c.MaxChars)
	}
}

// Issue #61: when the configured GPU fails and the CPU fallback runs, Settings
// must show the CPU, and choosing the GPU must try it again.

// startOnFallback configures (and saves) the unusable vulkan build and starts
// the server, which falls back to the CPU.
func startOnFallback(t *testing.T) *app {
	t.Helper()
	a, _, _ := newTestApp(t)
	a.conf.ServerFile, a.conf.GPULayers = backendSettings("vulkan")
	paths, err := resolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	a.paths = paths
	if err := updateConfigFile(a.dir, map[string]any{"server_file": a.conf.ServerFile, "gpu_layers": a.conf.GPULayers}); err != nil {
		t.Fatal(err)
	}
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if !healthy(a) || !a.onFallback() {
		t.Fatalf("precondition: the CPU fallback must run (running %q)", a.sup.cmd.Path)
	}
	return a
}

// engineChecks returns the engines marked active on the page and in the menu.
func engineChecks(t *testing.T, a *app) (page, menu []string) {
	t.Helper()
	for _, e := range a.settingsState().Engines {
		if e.Active {
			page = append(page, e.Name)
		}
	}
	for _, it := range a.buildMenu() {
		if it.Label != "Translation engine" {
			continue
		}
		for _, s := range it.Sub {
			if s.Checked {
				menu = append(menu, s.Label)
			}
		}
	}
	return page, menu
}

// preferenceOnDisk is the engine config.json names.
func preferenceOnDisk(t *testing.T, a *app) string {
	t.Helper()
	c, err := loadConfig(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	return currentBackend(c.ServerFile)
}

func TestCPUFallbackIsShownAsTheRunningEngine(t *testing.T) {
	a := startOnFallback(t)
	if got := a.runningBackend(); got != "cpu" {
		t.Fatalf("running engine: %q", got)
	}
	st := a.settingsState()
	if st.ConfiguredEngine != "vulkan" || st.RunningEngine != "cpu" {
		t.Errorf("state fields when configured != running: configured=%q, running=%q", st.ConfiguredEngine, st.RunningEngine)
	}
	page, menu := engineChecks(t, a)
	if len(page) != 1 || page[0] != "cpu" || len(menu) != 0 {
		t.Fatalf("the CPU must be the one checked: page %v, menu %v", page, menu)
	}
	// One failed start does not change the preference: the GPU is tried again
	// at the next start.
	if currentBackend(a.conf.ServerFile) != "vulkan" || preferenceOnDisk(t, a) != "vulkan" {
		t.Fatalf("the configured engine must stay vulkan: memory %q, disk %q", a.conf.ServerFile, preferenceOnDisk(t, a))
	}
}

func TestChoosingRunningFallbackViaSettingsRequest(t *testing.T) {
	a := startOnFallback(t)
	pid := a.sup.cmd.Process.Pid

	st := a.settingsState()
	if st.ConfiguredEngine != "vulkan" || st.RunningEngine != "cpu" {
		t.Fatalf("state mismatch before keep: configured=%q, running=%q", st.ConfiguredEngine, st.RunningEngine)
	}
	if st.ConfiguredEngine == st.RunningEngine {
		t.Fatal("expected configured != running before request")
	}

	req := newActionRequest("engine", "cpu", time.Minute)
	a.startEngineSwitch(req)
	rep := <-req.reply
	if rep.err != nil {
		t.Fatalf("startEngineSwitch error: %v", rep.err)
	}

	if a.sup.cmd.Process.Pid != pid || !healthy(a) {
		t.Fatal("choosing the engine that already runs must not restart it")
	}

	if c, _ := loadConfig(a.dir); currentBackend(c.ServerFile) != "cpu" || c.GPULayers != 0 {
		t.Errorf("config.json on disk: %q gpu_layers=%d", c.ServerFile, c.GPULayers)
	}

	if rep.state.ConfiguredEngine != "cpu" || rep.state.RunningEngine != "cpu" {
		t.Errorf("reply state after keep: configured=%q, running=%q", rep.state.ConfiguredEngine, rep.state.RunningEngine)
	}
	stAfter := a.settingsState()
	if stAfter.ConfiguredEngine != "cpu" || stAfter.RunningEngine != "cpu" {
		t.Errorf("state after keep: configured=%q, running=%q", stAfter.ConfiguredEngine, stAfter.RunningEngine)
	}
	if stAfter.ConfiguredEngine != stAfter.RunningEngine {
		t.Errorf("mismatch must be gone: configured=%q, running=%q", stAfter.ConfiguredEngine, stAfter.RunningEngine)
	}
}

func TestChoosingTheConfiguredGPUAfterFallbackRetriesIt(t *testing.T) {
	a := startOnFallback(t)
	tray := a.tray.(*fakeTray)
	pid := a.sup.cmd.Process.Pid

	a.switchBackend("vulkan") // still broken
	if a.sup.cmd == nil || a.sup.cmd.Process.Pid == pid {
		t.Fatal("choosing the configured GPU while the CPU runs must launch it again, not be a no-op")
	}
	if !healthy(a) || a.runningBackend() != "cpu" || !a.onFallback() {
		t.Fatalf("a failed retry must leave the CPU running: healthy=%v running=%q", healthy(a), a.runningBackend())
	}
	if !strings.Contains(tray.lastNote(), "keeping the previous engine") {
		t.Errorf("the user must be told, got %q", tray.lastNote())
	}
	if currentBackend(a.conf.ServerFile) != "vulkan" || preferenceOnDisk(t, a) != "vulkan" {
		t.Errorf("the preference must not change: memory %q, disk %q", a.conf.ServerFile, preferenceOnDisk(t, a))
	}

	// The same from the settings page: the request is answered as busy and
	// the page is told afterwards that the engine did not change.
	pid = a.sup.cmd.Process.Pid
	req := newActionRequest("engine", "vulkan", time.Minute)
	a.startEngineSwitch(req)
	if rep := <-req.reply; rep.err != nil || !strings.Contains(rep.state.Busy, "Vulkan") {
		t.Fatalf("the page must be told the app is switching: %+v", rep)
	}
	if a.sup.cmd.Process.Pid == pid || !healthy(a) || a.runningBackend() != "cpu" {
		t.Fatal("the page's retry must launch the GPU and fall back to a working CPU server")
	}
	if a.settingsNotice == "" {
		t.Error("the page must be told the engine could not be switched")
	}
	page, _ := engineChecks(t, a)
	if len(page) != 1 || page[0] != "cpu" {
		t.Errorf("after the failed retry the CPU must be shown: %v", page)
	}
}

func TestRetryingTheConfiguredGPUWorksOnceItCanStart(t *testing.T) {
	a := startOnFallback(t)
	// The GPU problem goes away (a driver update, memory freed): the vulkan
	// build is now the working fake server. Remove first so the new file gets
	// the executable mode.
	vulkan := filepath.Join(a.dir, "runtime", "vulkan", serverBinaryName())
	os.Remove(vulkan)
	self, _ := os.Executable()
	copyFile(t, self, vulkan)

	a.switchBackend("vulkan")
	if !healthy(a) || a.runningBackend() != "vulkan" || a.onFallback() {
		t.Fatalf("vulkan must run now: healthy=%v running=%q", healthy(a), a.runningBackend())
	}
	page, menu := engineChecks(t, a)
	if len(page) != 1 || page[0] != "vulkan" || len(menu) != 0 {
		t.Fatalf("vulkan must be the one checked: page %v, menu %v", page, menu)
	}
	if c, _ := loadConfig(a.dir); currentBackend(c.ServerFile) != "vulkan" || c.GPULayers != 99 {
		t.Errorf("config.json: %q gpu_layers=%d", c.ServerFile, c.GPULayers)
	}
}

func TestChoosingTheRunningFallbackKeepsItWithoutRestart(t *testing.T) {
	a := startOnFallback(t)
	pid := a.sup.cmd.Process.Pid
	a.switchBackend("cpu")
	if a.sup.cmd.Process.Pid != pid || !healthy(a) {
		t.Fatal("choosing the engine that already runs must not restart it")
	}
	if currentBackend(a.conf.ServerFile) != "cpu" || a.conf.GPULayers != 0 || a.onFallback() {
		t.Fatalf("the running CPU must become the configured engine: %q gpu_layers=%d", a.conf.ServerFile, a.conf.GPULayers)
	}
	if c, _ := loadConfig(a.dir); currentBackend(c.ServerFile) != "cpu" || c.GPULayers != 0 {
		t.Errorf("config.json: %q gpu_layers=%d", c.ServerFile, c.GPULayers)
	}
	// Choosing it again changes nothing.
	before, _ := os.ReadFile(filepath.Join(a.dir, "config.json"))
	a.switchBackend("cpu")
	if after, _ := os.ReadFile(filepath.Join(a.dir, "config.json")); string(after) != string(before) || a.sup.cmd.Process.Pid != pid {
		t.Error("choosing the configured, running engine must be a no-op")
	}
}

// Issue #80: switching models while on CPU fallback must use the fallback build
// with gpuLayers=0, and restore via startFallback if the new model fails.
func TestSwitchModelOnCPUFallbackUsesFallbackAndKeepsFallbackState(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("b", "b.gguf"))
	a := startOnFallback(t)
	putModel(t, a.dir, modelCatalog[1])
	oldPid := a.sup.cmd.Process.Pid

	if err := a.switchModel(modelCatalog[1]); err != nil {
		t.Fatalf("switching model on CPU fallback failed: %v", err)
	}
	if !healthy(a) || a.sup.cmd.Process.Pid == oldPid {
		t.Fatal("server must be restarted with the new model")
	}
	if a.sup.cmd.Path != a.paths.fallback {
		t.Fatalf("server should be running fallback %q, got %q", a.paths.fallback, a.sup.cmd.Path)
	}
	if !a.onFallback() {
		t.Fatal("a.onFallback() must still be true after switching models on CPU fallback")
	}
	if got := a.runningBackend(); got != "cpu" {
		t.Fatalf("running backend must still be cpu, got %q", got)
	}
	if currentBackend(a.conf.ServerFile) != "vulkan" {
		t.Fatalf("preferred engine in config must stay vulkan, got %q", a.conf.ServerFile)
	}
	if a.conf.ModelFile != "models/b.gguf" {
		t.Fatalf("model file should be updated to models/b.gguf, got %q", a.conf.ModelFile)
	}
	c, err := loadConfig(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.ModelFile != "models/b.gguf" || currentBackend(c.ServerFile) != "vulkan" {
		t.Errorf("saved config: model=%q server=%q", c.ModelFile, c.ServerFile)
	}
}

func TestSwitchModelOnCPUFallbackFailureRestoresFallbackWithoutTouchingGPU(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("bad", "bad-FAIL.gguf"))
	a := startOnFallback(t)
	putModel(t, a.dir, modelCatalog[1])
	oldModel := a.sup.model

	err := a.switchModel(modelCatalog[1])
	if err == nil || !strings.Contains(err.Error(), "previous model is back") {
		t.Fatalf("expected error indicating previous model is back, got: %v", err)
	}
	if !healthy(a) {
		t.Fatal("server must be restored and healthy")
	}
	if a.sup.cmd.Path != a.paths.fallback {
		t.Fatalf("server should be restored to fallback %q, got %q", a.paths.fallback, a.sup.cmd.Path)
	}
	if !a.onFallback() {
		t.Fatal("a.onFallback() must still be true after failed model switch on CPU fallback")
	}
	if got := a.runningBackend(); got != "cpu" {
		t.Fatalf("running backend must still be cpu, got %q", got)
	}
	if a.sup.model != oldModel {
		t.Fatalf("supervisor model must be restored to %q, got %q", oldModel, a.sup.model)
	}
}

// Issue #80: settingsState must not report the unverified target model/engine as active while switching.
func TestSettingsStateDoesNotReportTargetModelAsActiveWhileSwitching(t *testing.T) {
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "200")
	withModels(t, testEntry("a", "a.gguf"), testEntry("b", "b.gguf"))
	a, _, _ := newTestApp(t)
	putModel(t, a.dir, modelCatalog[0])
	putModel(t, a.dir, modelCatalog[1])
	a.conf.ModelFile = "models/a.gguf"
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}

	checked := 0
	origUI := a.sup.ui
	a.sup.ui = func(label string, elapsed time.Duration) bool {
		st := a.settingsState()
		mA := findModel(st, "a")
		mB := findModel(st, "b")
		if !mA.Active {
			t.Errorf("old model a must be active while switching, got %+v", mA)
		}
		if mB.Active {
			t.Errorf("target model b must NOT be active while switching, got %+v", mB)
		}
		checked++
		return origUI(label, elapsed)
	}

	if err := a.switchModel(modelCatalog[1]); err != nil {
		t.Fatalf("switchModel failed: %v", err)
	}
	if checked == 0 {
		t.Fatal("ui callback must have checked settingsState while server was loading")
	}
	st := a.settingsState()
	if !findModel(st, "b").Active || findModel(st, "a").Active {
		t.Fatalf("after switch: b must be active, got %+v", st.Models)
	}
}

func TestSettingsStateDoesNotReportTargetEngineAsActiveWhileSwitching(t *testing.T) {
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "200")
	a, _, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if a.runningBackend() != "cpu" {
		t.Fatalf("precondition: running backend should be cpu, got %q", a.runningBackend())
	}

	checked := 0
	origUI := a.sup.ui
	a.sup.ui = func(label string, elapsed time.Duration) bool {
		page, _ := engineChecks(t, a)
		if len(page) != 1 || page[0] != "cpu" {
			t.Errorf("cpu must remain active while switching engine, got %v", page)
		}
		checked++
		return origUI(label, elapsed)
	}

	a.switchBackend("cuda")
	if checked == 0 {
		t.Fatal("ui callback must have checked settingsState while server was loading")
	}
	if a.runningBackend() != "cuda" {
		t.Fatalf("after switch: running backend must be cuda, got %q", a.runningBackend())
	}
	page, _ := engineChecks(t, a)
	if len(page) != 1 || page[0] != "cuda" {
		t.Fatalf("after switch: cuda must be active, got %v", page)
	}
}

func TestSettingsStateDoesNotReportConfiguredGPUAsActiveWhileRetryingFromFallback(t *testing.T) {
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "200")
	a := startOnFallback(t)
	checked := 0
	origUI := a.sup.ui
	a.sup.ui = func(label string, elapsed time.Duration) bool {
		page, _ := engineChecks(t, a)
		if len(page) != 1 || page[0] != "cpu" {
			t.Errorf("cpu must remain active while retrying broken vulkan GPU, got %v", page)
		}
		checked++
		return origUI(label, elapsed)
	}

	a.switchBackend("vulkan")
	if checked == 0 {
		t.Fatal("ui callback must have checked settingsState while server was loading")
	}
	page, _ := engineChecks(t, a)
	if len(page) != 1 || page[0] != "cpu" {
		t.Fatalf("after failed retry: cpu must remain active, got %v", page)
	}
}
