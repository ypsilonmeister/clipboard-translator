package app

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/models"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// Issue #190: a GPU engine that does not start is announced once, and the
// session then reloads the engine that worked instead of retrying the GPU.

func engineMenuLine(a *app) string {
	for _, it := range a.buildMenu() {
		if strings.HasPrefix(it.Label, "Engine:") {
			return it.Label
		}
	}
	return ""
}

// fixVulkan replaces the unusable vulkan build with a working fake server, so
// that a retry of it is visible as the running binary.
func fixVulkan(t *testing.T, a *app) {
	t.Helper()
	vulkan := filepath.Join(a.dir, "runtime", "vulkan", platform.ServerBinaryName())
	os.Remove(vulkan)
	self, _ := os.Executable()
	copyFile(t, self, vulkan)
}

func notesWith(tray *fakeTray, sub string) int {
	n := 0
	for _, note := range tray.notes {
		if strings.Contains(note, sub) {
			n++
		}
	}
	return n
}

func TestFallbackIsAnnouncedOnce(t *testing.T) {
	a := startOnFallback(t)
	tray := a.tray.(*fakeTray)
	if n := notesWith(tray, "did not start"); n != 1 {
		t.Fatalf("want one notice, got %d: %v", n, tray.notes)
	}
	if !strings.Contains(tray.lastNote(), "GPU (Vulkan)") || !strings.Contains(tray.lastNote(), "CPU") {
		t.Errorf("the notice must name both engines: %q", tray.lastNote())
	}
	if got := engineMenuLine(a); !strings.Contains(got, "CPU") || !strings.Contains(got, "Vulkan") {
		t.Errorf("menu engine line: %q", got)
	}
	// Another reload in the same session does not announce it again.
	a.unloadModel()
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if n := notesWith(tray, "did not start"); n != 1 {
		t.Fatalf("a reload must not repeat the notice, got %d", n)
	}
}

func TestReloadAfterFallbackSkipsThePreferredEngine(t *testing.T) {
	a, _, cb, _, _ := uxApp(t)
	a.conf.ServerFile, a.conf.GPULayers = config.BackendSettings("vulkan")
	paths, err := config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	a.paths = paths
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if !a.onFallback() {
		t.Fatal("precondition: CPU fallback runs")
	}
	a.unloadModel()
	fixVulkan(t, a) // were the GPU tried again, it would now start

	cb.text = "こんにちは"
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return cb.text, true, nil }
	a.startTranslation(0)
	if !a.sup.Running() || a.sup.BinaryPath() != a.paths.Fallback {
		t.Fatalf("the reload must start the CPU build directly, running %q", a.sup.BinaryPath())
	}
	if config.CurrentBackend(a.conf.ServerFile) != "vulkan" {
		t.Error("config must keep naming the preferred engine")
	}
}

func TestChoosingTheEngineAgainRetriesAndClearsTheNotice(t *testing.T) {
	a := startOnFallback(t)
	a.unloadModel()
	fixVulkan(t, a)
	a.switchBackend("vulkan")
	if a.runningBackend() != "vulkan" || a.fallbackPinned {
		t.Fatalf("the preferred engine must be retried: running %q pinned %v", a.runningBackend(), a.fallbackPinned)
	}
	if got := engineMenuLine(a); got != "" {
		t.Errorf("no engine line expected once the engine runs: %q", got)
	}
}

func TestNoEngineLineWithoutFallback(t *testing.T) {
	a, tray, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if got := engineMenuLine(a); got != "" {
		t.Errorf("unexpected engine line: %q", got)
	}
	if n := notesWith(tray, "did not start"); n != 0 {
		t.Errorf("unexpected notice: %v", tray.notes)
	}
}

// serverStarts runs f and returns the binaries Supervisor.Start launched
// meanwhile, read from its "starting <binary>" log lines.
func serverStarts(t *testing.T, f func()) []string {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	f()
	var bins []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if _, rest, ok := strings.Cut(line, "starting "); ok && strings.Contains(rest, "(gpu_layers=") {
			bins = append(bins, strings.TrimSpace(rest[:strings.Index(rest, "(gpu_layers=")]))
		}
	}
	return bins
}

// breakVulkan puts the unusable vulkan build back.
func breakVulkan(t *testing.T, a *app) {
	t.Helper()
	vulkan := filepath.Join(a.dir, "runtime", "vulkan", platform.ServerBinaryName())
	os.Remove(vulkan)
	if err := os.WriteFile(vulkan, []byte("not an executable"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The acceptance check of #190: the reload costs one Supervisor.Start, of the
// CPU build, while the preferred engine is as broken as before.
func TestReloadAfterFallbackStartsOnlyTheCPUBuild(t *testing.T) {
	a := startOnFallback(t)
	a.unloadModel()
	var err error
	starts := serverStarts(t, func() { err = a.startServer() })
	if err != nil {
		t.Fatal(err)
	}
	if len(starts) != 1 || starts[0] != a.paths.Fallback {
		t.Fatalf("want one start, of %q; got %q", a.paths.Fallback, starts)
	}
}

// A crashed CPU fallback is restarted directly too, with the usual "back" notice.
func TestRestartAfterFallbackStartsOnlyTheCPUBuild(t *testing.T) {
	a := startOnFallback(t)
	tray := a.tray.(*fakeTray)
	a.sup.Stop()
	ok := false
	starts := serverStarts(t, func() { ok = a.restartServer() })
	if !ok {
		t.Fatal("restartServer gave up")
	}
	if len(starts) != 1 || starts[0] != a.paths.Fallback {
		t.Fatalf("want one start, of %q; got %q", a.paths.Fallback, starts)
	}
	if n := notesWith(tray, "did not start"); n != 1 {
		t.Errorf("the restart must not repeat the notice, got %d", n)
	}
	if !strings.Contains(tray.lastNote(), "back") {
		t.Errorf("the user must be told the server is back, got %q", tray.lastNote())
	}
}

// A restart that ends on the CPU fallback says so last: "The translation server
// is back" right after it would replace the fallback notice on the screen.
func TestRestartThatFallsBackKeepsTheFallbackNotice(t *testing.T) {
	a, tray, _ := newTestApp(t)
	a.conf.ServerFile, a.conf.GPULayers = config.BackendSettings("vulkan")
	paths, err := config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	a.paths = paths
	fixVulkan(t, a)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if a.runningBackend() != "vulkan" || notesWith(tray, "did not start") != 0 {
		t.Fatalf("precondition: vulkan runs without a notice (running %q, notes %v)", a.runningBackend(), tray.notes)
	}
	a.sup.Stop()
	breakVulkan(t, a)
	if !a.restartServer() {
		t.Fatal("restartServer gave up")
	}
	if !a.onFallback() {
		t.Fatalf("the CPU fallback must run, running %q", a.sup.BinaryPath())
	}
	if !strings.Contains(tray.lastNote(), "did not start") {
		t.Errorf("the fallback notice must be the last one, got %v", tray.notes)
	}
}

// A model switch on the fallback lets the next load try the preferred engine
// again, but the CPU build still runs: the menu must keep saying so.
func TestModelSwitchOnFallbackKeepsTheEngineLine(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("b", "b.gguf"))
	a := startOnFallback(t)
	putModel(t, a.dir, models.Catalog[1])
	if err := a.switchModel(models.Catalog[1]); err != nil {
		t.Fatal(err)
	}
	if !a.onFallback() {
		t.Fatal("precondition: the new model runs on the CPU fallback")
	}
	if got := engineMenuLine(a); !strings.Contains(got, "CPU") {
		t.Errorf("the CPU build runs instead of the configured engine, but the menu line is %q", got)
	}
	// The preferred engine gets its retry at the next load.
	a.unloadModel()
	fixVulkan(t, a)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if a.runningBackend() != "vulkan" {
		t.Fatalf("the load after a model switch must retry the preferred engine, running %q", a.runningBackend())
	}
	if got := engineMenuLine(a); got != "" {
		t.Errorf("no engine line expected once the engine runs: %q", got)
	}
}

// With the model unloaded after a fallback, a model switch loads the new model
// on the CPU build, as it does while the fallback runs: the preferred engine
// has no fallback of its own there and would refuse the switch.
func TestModelSwitchWhileUnloadedAfterFallbackUsesTheCPUBuild(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("b", "b.gguf"))
	a := startOnFallback(t)
	putModel(t, a.dir, models.Catalog[1])
	a.unloadModel()
	var err error
	starts := serverStarts(t, func() { err = a.switchModel(models.Catalog[1]) })
	if err != nil {
		t.Fatalf("the switch must not fail on the engine that already failed: %v", err)
	}
	if len(starts) != 1 || starts[0] != a.paths.Fallback {
		t.Fatalf("want one start, of %q; got %q", a.paths.Fallback, starts)
	}
	if a.conf.ModelFile != "models/b.gguf" || !a.onFallback() {
		t.Fatalf("model %q, running %q", a.conf.ModelFile, a.sup.BinaryPath())
	}
	if got := engineMenuLine(a); !strings.Contains(got, "CPU") {
		t.Errorf("menu engine line: %q", got)
	}
}
