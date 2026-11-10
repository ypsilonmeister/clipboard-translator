package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/models"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

// newTestApp builds an app whose llama-server is this test binary acting as a
// fake (see TestMain). cpu and cuda builds are installed; vulkan is a file that
// cannot be started.
func newTestApp(t *testing.T) (*app, *fakeTray, *[]string) {
	t.Helper()
	t.Setenv("CT_TEST_FAKE_SERVER", "1")
	dir := t.TempDir()
	self, _ := os.Executable()
	for _, b := range []string{"cpu", "cuda"} {
		copyFile(t, self, filepath.Join(dir, "runtime", b, platform.ServerBinaryName()))
	}
	os.MkdirAll(filepath.Join(dir, "runtime", "vulkan"), 0o755)
	os.WriteFile(filepath.Join(dir, "runtime", "vulkan", platform.ServerBinaryName()), []byte("not an executable"), 0o644)
	os.MkdirAll(filepath.Join(dir, "models"), 0o755)
	os.WriteFile(filepath.Join(dir, "models", "m.gguf"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "models", "t.jinja"), []byte("x"), 0o644)

	conf := defaultConfig()
	conf.Port = freePort(t)
	conf.ModelFile, conf.TemplateFile = "models/m.gguf", "models/t.jinja"
	conf.StartupTimeoutSec = 20
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(fmt.Sprintf(`{"hotkey": "Ctrl+Alt+E", "port": %d}`, conf.Port)), 0o644)
	paths, err := resolvePaths(dir, conf)
	if err != nil {
		t.Fatal(err)
	}
	tray := &fakeTray{}
	a := newApp(dir, conf, paths, tray, io.Discard)
	var errs []string
	a.showErr = func(msg string) { errs = append(errs, msg) }
	a.newHotkey = func(spec string) (platform.Hotkey, error) { return fakeHotkey{spec}, nil }
	a.fallbackEditor = func(path string) error { return errors.New("fallback disabled in tests") }
	a.askYesNo = func(msg string) bool { return false }
	// Tests script the capture through a.capture (or set beginCapture
	// themselves); the real one would send Ctrl+C to this desktop.
	a.beginCapture = nil
	t.Cleanup(a.sup.Stop)
	return a, tray, &errs
}

func healthy(a *app) bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(a.endpoint + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func TestRestartServerAfterCrash(t *testing.T) {
	a, tray, errs := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	pid := a.sup.cmd.Process.Pid
	select {
	case <-a.sup.Exited():
		t.Fatal("server must be running")
	default:
	}
	a.sup.cmd.Process.Kill()
	select {
	case <-a.sup.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("a killed server must close Exited")
	}
	a.sup.Stop()
	if !a.restartServer() {
		t.Fatalf("restartServer gave up; errors: %v", *errs)
	}
	if a.sup.cmd == nil || a.sup.cmd.Process.Pid == pid || !healthy(a) {
		t.Fatal("a new, healthy server must be running")
	}
	if !strings.Contains(tray.lastNote(), "back") {
		t.Errorf("the user must be told the server is back, got %q", tray.lastNote())
	}
}

func TestRestartServerGivesUp(t *testing.T) {
	a, _, errs := newTestApp(t)
	a.limiter = restartLimiter{max: 0, window: time.Minute}
	if a.restartServer() {
		t.Fatal("with no restarts allowed it must give up")
	}
	if len(*errs) != 1 || !strings.Contains((*errs)[0], "keeps stopping") {
		t.Fatalf("the user must see why the app quits, got %v", *errs)
	}
}

func TestRestartServerHonoursQuit(t *testing.T) {
	a, tray, _ := newTestApp(t)
	tray.quit = true
	if a.restartServer() {
		t.Fatal("a quit during the backoff must stop the restart")
	}
	if a.sup.cmd != nil {
		t.Fatal("no server may be started after quitting")
	}
}

func TestSwitchBackend(t *testing.T) {
	a, tray, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.switchBackend("cuda")
	if currentBackend(a.conf.ServerFile) != "cuda" || a.conf.GPULayers != 99 || !healthy(a) {
		t.Fatalf("switch to cuda failed: server_file=%q gpu=%d healthy=%v notes=%v",
			a.conf.ServerFile, a.conf.GPULayers, healthy(a), tray.notes)
	}
	c, err := loadConfig(a.dir)
	if err != nil || c.ServerFile != "runtime/cuda/"+platform.ServerBinaryName() || c.GPULayers != 99 {
		t.Fatalf("the choice must be saved: %+v, %v", c, err)
	}

	// A build that cannot start must leave the previous one running and the
	// settings unchanged.
	a.switchBackend("vulkan")
	if currentBackend(a.conf.ServerFile) != "cuda" || !healthy(a) {
		t.Fatalf("a failed switch must keep cuda: server_file=%q healthy=%v", a.conf.ServerFile, healthy(a))
	}
	if c, _ := loadConfig(a.dir); c.ServerFile != "runtime/cuda/"+platform.ServerBinaryName() {
		t.Fatalf("a failed switch must not be saved, config has %q", c.ServerFile)
	}
	if !strings.Contains(tray.lastNote(), "keeping the previous engine") {
		t.Errorf("the user must be told, got %q", tray.lastNote())
	}
}

func TestChangeHotkey(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.hotkey = fakeHotkey{"Ctrl+Alt+E"}

	changeSettingsHotkey(t, a, "Ctrl+Shift+Y")
	if a.conf.Hotkey != "Ctrl+Shift+Y" || !strings.Contains(a.readyTip, "Ctrl+Shift+Y") {
		t.Fatalf("hotkey not applied: %q / %q", a.conf.Hotkey, a.readyTip)
	}
	if c, _ := loadConfig(a.dir); c.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("hotkey not saved: %q", c.Hotkey)
	}

	// A hotkey another program owns: keep the old one, save nothing.
	a.newHotkey = func(spec string) (platform.Hotkey, error) {
		if spec == "Ctrl+Alt+T" {
			return nil, errors.New("already registered")
		}
		return fakeHotkey{spec}, nil
	}
	err := changeSettingsHotkey(t, a, "Ctrl+Alt+T")
	if a.conf.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("a taken hotkey must not replace the working one, got %q", a.conf.Hotkey)
	}
	if hk, ok := a.hotkey.(fakeHotkey); !ok || hk.spec != "Ctrl+Shift+Y" {
		t.Fatalf("the previous hotkey must be registered again, got %#v", a.hotkey)
	}
	if c, _ := loadConfig(a.dir); c.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("a failed change must not be saved, config has %q", c.Hotkey)
	}
	if err == nil || !strings.Contains(err.Error(), "previous settings were kept") {
		t.Errorf("the user must be told, got %v", err)
	}
}

func TestBuildMenu(t *testing.T) {
	a, _, _ := newTestApp(t)
	find := func(items []platform.MenuItem, label string) *platform.MenuItem {
		for i := range items {
			if items[i].Label == label || strings.HasPrefix(items[i].Label, label) {
				return &items[i]
			}
		}
		return nil
	}
	items := a.buildMenu()

	if it := find(items, "Check for updates"); it == nil || it.ID != cmdCheckUpdate {
		t.Fatalf("update entry: %+v", it)
	}
	old := version
	defer func() { version = old }()
	version = "0.5.1" // a development build is never "outdated"
	a.latest = releaseInfo{Tag: "v99.0.0"}
	if it := find(a.buildMenu(), "Update to v99.0.0"); it == nil || it.ID != cmdDoUpdate {
		t.Fatalf("an available update must show 'Update to ...'")
	}
	a.checking = true
	if it := find(a.buildMenu(), "Checking"); it == nil || !it.Disabled {
		t.Fatalf("while checking, the entry must be disabled")
	}
	if find(items, "Built with PLaMo") == nil {
		t.Error("the PLaMo attribution item is required by its license")
	}
	// All command IDs must be unique and in the app range.
	seen := map[int]bool{}
	var walk func([]platform.MenuItem)
	walk = func(m []platform.MenuItem) {
		for _, it := range m {
			if len(it.Sub) > 0 {
				walk(it.Sub)
				continue
			}
			if it.Separator || it.Disabled {
				continue
			}
			if it.ID < platform.FirstAppCommand || seen[it.ID] {
				t.Errorf("bad or duplicate command id %d (%q)", it.ID, it.Label)
			}
			seen[it.ID] = true
		}
	}
	walk(items)
	for _, m := range models.Catalog {
		putModel(t, a.dir, m)
	}
	seen = map[int]bool{}
	walk(a.buildMenu())
}

func TestOnUpdateResult(t *testing.T) {
	a, tray, _ := newTestApp(t)
	old := version
	defer func() { version = old }()
	version = "0.5.1"

	a.checking = true
	a.onUpdateResult(updateResult{info: releaseInfo{Tag: "v0.6.0"}})
	if a.checking || a.latest.Tag != "v0.6.0" || !strings.Contains(tray.lastNote(), "v0.6.0 is available") {
		t.Fatalf("newer release: checking=%v latest=%v note=%q", a.checking, a.latest, tray.lastNote())
	}
	a.onUpdateResult(updateResult{info: releaseInfo{Tag: "v0.5.1"}})
	if a.latest.Tag != "" || !strings.Contains(tray.lastNote(), "latest version") {
		t.Fatalf("same release: latest=%v note=%q", a.latest, tray.lastNote())
	}
	a.onUpdateResult(updateResult{err: errors.New("offline")})
	if !strings.Contains(tray.lastNote(), "Could not check") {
		t.Fatalf("error: note=%q", tray.lastNote())
	}
	version = "dev"
	a.startUpdateCheck()
	if a.checking || !strings.Contains(tray.lastNote(), "development build") {
		t.Fatalf("a dev build must not check: checking=%v note=%q", a.checking, tray.lastNote())
	}
}

func TestBuildMenuExactLabels(t *testing.T) {
	old := version
	version = "1.0.0"
	defer func() { version = old }()
	for _, state := range []string{"idle", "translating", "unloaded", "no hotkey active", "target profile", "checking", "update"} {
		for _, engines := range []int{1, 3} {
			for _, nModels := range []int{0, 2} {
				t.Run(fmt.Sprintf("%s/%d engines/%d models", state, engines, nModels), func(t *testing.T) {
					a, _, _ := newTestApp(t)
					for i := 0; i < nModels && i < len(models.Catalog); i++ {
						putModel(t, a.dir, models.Catalog[i])
					}
					a.registerStartupHotkey()
					if engines == 1 {
						for _, b := range []string{"cuda", "vulkan"} {
							if err := os.Remove(filepath.Join(a.dir, "runtime", b, platform.ServerBinaryName())); err != nil {
								t.Fatal(err)
							}
						}
					}
					if err := a.startServer(); err != nil {
						t.Fatal(err)
					}
					switch state {
					case "translating":
						pending(a)
					case "unloaded":
						a.unloadModel()
					case "no hotkey active":
						a.closeHotkeys()
						a.hotkey = noHotkey{}
					case "target profile":
						a.conf.Profiles[0] = Profile{Hotkey: "Ctrl+Alt+E", Target: "ko"}
					case "checking":
						a.checking = true
					case "update":
						a.latest = releaseInfo{Tag: "v2.0.0"}
					}
					want := []string{"Settings...", "Quick start"}
					if a.translating {
						want = append(want, "Cancel translation", "---")
					}
					update := "Check for updates"
					if a.checking {
						update = "Checking for updates..."
					} else if state == "update" {
						update = "Update to v2.0.0..."
					}
					want = append(want, "Unload model")
					if nModels >= 2 {
						want = append(want, "Translation model")
					}
					want = append(want, "Recent translations (click to copy)", "---", update, "Open config.json", "---", "Built with PLaMo (license)", "Clipboard Translator 1.0.0")
					items := a.buildMenu()
					var got []string
					for _, it := range items {
						if it.Separator {
							got = append(got, "---")
						} else {
							got = append(got, it.Label)
						}
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("menu=%q want=%q", got, want)
					}
					if items[0].ID != cmdSettings || items[0].Disabled {
						t.Fatalf("Settings must be first and enabled: %+v", items[0])
					}
					if unloadMenuEnabled(t, a) != (state != "translating" && state != "unloaded") {
						t.Fatal("incorrect Unload availability")
					}
				})
			}
		}
	}
}
