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
// still writes "profiles", which loadConfig prefers; later tray changes must
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

func TestTrayLanguagesSurviveRestartAfterSettingsOnlySave(t *testing.T) {
	a := settingsOnlySave(t, func(f *settingsForm) { f.AutoPaste = !f.AutoPaste })
	a.changeLanguages([]string{"ja", "ko"})
	c := checkReloaded(t, a)
	if !samePair(c.Profiles[0].Languages, []string{"ja", "ko"}) || !samePair(c.Languages, []string{"ja", "ko"}) {
		t.Fatalf("tray change lost after reload: memory=%v disk=%v explicit=%v", a.conf.Languages, c.Profiles[0].Languages, a.conf.explicitProfiles)
	}
	if !c.AutoPaste {
		t.Error("auto_paste from the settings page was lost")
	}
}

func TestTrayHotkeySurvivesRestartAfterSettingsOnlySave(t *testing.T) {
	a := settingsOnlySave(t, func(f *settingsForm) { f.MaxChars = 2000 })
	a.changeHotkey("Ctrl+Shift+Y")
	if a.conf.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("precondition: hotkey changed in memory, got %q", a.conf.Hotkey)
	}
	c := checkReloaded(t, a)
	if c.Profiles[0].Hotkey != "Ctrl+Shift+Y" || c.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("tray change lost after reload: memory=%q disk=%q explicit=%v", a.conf.Hotkey, c.Profiles[0].Hotkey, a.conf.explicitProfiles)
	}
	if c.MaxChars != 2000 {
		t.Errorf("max_chars from the settings page was lost: %d", c.MaxChars)
	}
}

// Issue #61: when the configured GPU fails and the CPU fallback runs, the menu
// and the page must show the CPU, and choosing the GPU must try it again.

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
	page, menu := engineChecks(t, a)
	if len(page) != 1 || page[0] != "cpu" || len(menu) != 1 || menu[0] != "CPU" {
		t.Fatalf("the CPU must be the one checked: page %v, menu %v", page, menu)
	}
	// One failed start does not change the preference: the GPU is tried again
	// at the next start.
	if currentBackend(a.conf.ServerFile) != "vulkan" || preferenceOnDisk(t, a) != "vulkan" {
		t.Fatalf("the configured engine must stay vulkan: memory %q, disk %q", a.conf.ServerFile, preferenceOnDisk(t, a))
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
	if len(page) != 1 || page[0] != "vulkan" || len(menu) != 1 || menu[0] != "GPU (Vulkan)" {
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
