package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/llamaserver"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func TestMissingFilesAndPaths(t *testing.T) {
	dir := t.TempDir()
	conf := config.Default()
	p, err := config.ResolvePaths(dir, conf)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Missing(); len(got) != 3 {
		t.Fatalf("empty folder: missing = %v, want model, template and server", got)
	}
	for _, f := range []string{p.Model, p.Template, p.Server} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.Missing(); len(got) != 0 {
		t.Fatalf("all present: missing = %v", got)
	}
	conf.ModelFile = filepath.Join("..", "outside.gguf")
	if _, err := config.ResolvePaths(dir, conf); err == nil {
		t.Fatal("a path outside the app folder must be rejected")
	}
}

// A llama-server that dies with STATUS_DLL_NOT_FOUND must be reported as that,
// not as "exited before becoming ready" or "CPU fallback failed". Exit codes
// above 255 only exist on Windows.
func TestStartServerReportsMissingDLL(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("exit codes above 255 are a Windows thing")
	}
	t.Setenv("CT_TEST_FAKE_EXIT_CODE", "3221225781")
	a, _, _ := newTestApp(t)
	err := a.startServer()
	var dnf *llamaserver.DLLNotFoundError
	if !errors.As(err, &dnf) {
		t.Fatalf("startServer error = %v, want a dllNotFoundError", err)
	}
	if strings.Contains(err.Error(), "CPU fallback failed") {
		t.Errorf("message must not blame the fallback: %v", err)
	}
	if a.sup.Running() {
		t.Error("no server may be left running")
	}
}

// Without the VC++ runtime no llama.cpp build can start, so the app must say so
// without launching a process and without trying the CPU build.
func TestStartServerWithoutVCRuntime(t *testing.T) {
	a, _, _ := newTestApp(t)
	old := platform.VCRuntimeAvailable
	platform.VCRuntimeAvailable = func(string) bool { return false }
	t.Cleanup(func() { platform.VCRuntimeAvailable = old })
	err := a.startServer()
	var dnf *llamaserver.DLLNotFoundError
	if !errors.As(err, &dnf) || !dnf.VCMissing {
		t.Fatalf("startServer error = %v, want a dllNotFoundError with VCMissing", err)
	}
	if !strings.Contains(err.Error(), "winget install") || strings.Contains(err.Error(), "CPU fallback") {
		t.Errorf("unexpected message:\n%v", err)
	}
	if a.sup.Running() {
		t.Error("no server may be started")
	}
}
