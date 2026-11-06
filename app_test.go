package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		copyFile(t, self, filepath.Join(dir, "runtime", b, "llama-server.exe"))
	}
	os.MkdirAll(filepath.Join(dir, "runtime", "vulkan"), 0o755)
	os.WriteFile(filepath.Join(dir, "runtime", "vulkan", "llama-server.exe"), []byte("not an executable"), 0o644)
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
	a.newHotkey = func(spec string) (Hotkey, error) { return fakeHotkey{spec}, nil }
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
	if err != nil || c.ServerFile != "runtime/cuda/llama-server.exe" || c.GPULayers != 99 {
		t.Fatalf("the choice must be saved: %+v, %v", c, err)
	}

	// A build that cannot start must leave the previous one running and the
	// settings unchanged.
	a.switchBackend("vulkan")
	if currentBackend(a.conf.ServerFile) != "cuda" || !healthy(a) {
		t.Fatalf("a failed switch must keep cuda: server_file=%q healthy=%v", a.conf.ServerFile, healthy(a))
	}
	if c, _ := loadConfig(a.dir); c.ServerFile != "runtime/cuda/llama-server.exe" {
		t.Fatalf("a failed switch must not be saved, config has %q", c.ServerFile)
	}
	if !strings.Contains(tray.lastNote(), "keeping the previous engine") {
		t.Errorf("the user must be told, got %q", tray.lastNote())
	}
}

func TestChangeHotkey(t *testing.T) {
	a, tray, _ := newTestApp(t)
	a.hotkey = fakeHotkey{"Ctrl+Alt+E"}

	a.changeHotkey("Ctrl+Shift+Y")
	if a.conf.Hotkey != "Ctrl+Shift+Y" || !strings.Contains(a.readyTip, "Ctrl+Shift+Y") {
		t.Fatalf("hotkey not applied: %q / %q", a.conf.Hotkey, a.readyTip)
	}
	if c, _ := loadConfig(a.dir); c.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("hotkey not saved: %q", c.Hotkey)
	}

	// A hotkey another program owns: keep the old one, save nothing.
	a.newHotkey = func(spec string) (Hotkey, error) {
		if spec == "Ctrl+Alt+T" {
			return nil, errors.New("already registered")
		}
		return fakeHotkey{spec}, nil
	}
	a.changeHotkey("Ctrl+Alt+T")
	if a.conf.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("a taken hotkey must not replace the working one, got %q", a.conf.Hotkey)
	}
	if hk, ok := a.hotkey.(fakeHotkey); !ok || hk.spec != "Ctrl+Shift+Y" {
		t.Fatalf("the previous hotkey must be registered again, got %#v", a.hotkey)
	}
	if c, _ := loadConfig(a.dir); c.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("a failed change must not be saved, config has %q", c.Hotkey)
	}
	if !strings.Contains(tray.lastNote(), "Keeping Ctrl+Shift+Y") {
		t.Errorf("the user must be told, got %q", tray.lastNote())
	}
}

func TestBuildMenu(t *testing.T) {
	a, _, _ := newTestApp(t)
	find := func(items []MenuItem, label string) *MenuItem {
		for i := range items {
			if items[i].Label == label || strings.HasPrefix(items[i].Label, label) {
				return &items[i]
			}
		}
		return nil
	}
	items := a.buildMenu()

	hk := find(items, "Hotkey")
	if hk == nil || len(hk.Sub) != len(hotkeyPresets) {
		t.Fatalf("hotkey submenu missing: %+v", hk)
	}
	checked := 0
	for _, s := range hk.Sub {
		if s.Checked {
			checked++
			if s.Label != "Ctrl+Alt+E" {
				t.Errorf("wrong hotkey checked: %q", s.Label)
			}
		}
	}
	if checked != 1 {
		t.Errorf("exactly one hotkey must be checked, got %d", checked)
	}

	eng := find(items, "Translation engine")
	if eng == nil || len(eng.Sub) != 3 { // cpu, cuda and the unusable vulkan are all "installed"
		t.Fatalf("engine submenu: %+v", eng)
	}
	if !eng.Sub[0].Checked || eng.Sub[0].Label != "CPU" {
		t.Errorf("CPU must be checked: %+v", eng.Sub)
	}

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
	var walk func([]MenuItem)
	walk = func(m []MenuItem) {
		for _, it := range m {
			if len(it.Sub) > 0 {
				walk(it.Sub)
				continue
			}
			if it.Separator || it.Disabled {
				continue
			}
			if it.ID < firstAppCommand || seen[it.ID] {
				t.Errorf("bad or duplicate command id %d (%q)", it.ID, it.Label)
			}
			seen[it.ID] = true
		}
	}
	walk(items)
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
