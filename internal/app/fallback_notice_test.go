package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
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

func countNotes(tray *fakeTray, sub string) int {
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
	if n := countNotes(tray, "did not start"); n != 1 {
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
	if n := countNotes(tray, "did not start"); n != 1 {
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
	if n := countNotes(tray, "did not start"); n != 0 {
		t.Errorf("unexpected notice: %v", tray.notes)
	}
}
