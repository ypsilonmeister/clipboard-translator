package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// The cases in this file port tools/test_linux_setup.py, which exercised
// the Python body of the old scripts; the semantics now live in Go.

func TestConfiguredServer(t *testing.T) {
	root := t.TempDir()
	server := filepath.Join(root, "runtime", "cpu", "llama-server")
	if err := os.MkdirAll(filepath.Dir(server), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(server, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		value any
		want  bool
	}{
		{"installed", "runtime/cpu/llama-server", true},
		{"installed backslashes", `runtime\cpu\llama-server`, true},
		{"missing file", "runtime/missing/llama-server", false},
		{"a directory", "runtime/cpu", false},
		{"dotdot", "../runtime/cpu/llama-server", false},
		{"inner dotdot", "runtime/cpu/../cpu/llama-server", false},
		{"absolute", "/runtime/cpu/llama-server", false},
		{"drive", `C:\runtime\cpu\llama-server`, false},
		{"empty", "", false},
		{"blank", "  ", false},
		{"null", nil, false},
		{"number", float64(42), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := map[string]any{"server_file": c.value}
			if got := configuredServer(root, cfg, "server_file"); got != c.want {
				t.Errorf("configuredServer(%v) = %v, want %v", c.value, got, c.want)
			}
		})
	}
	if configuredServer(root, map[string]any{}, "server_file") {
		t.Error("a missing key must not count as configured")
	}
}

func engineFixtureRoot(t *testing.T, backends ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, b := range backends {
		p := filepath.Join(root, "runtime", b, "llama-server")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestEngineUpdatesPreservesUserEngine(t *testing.T) {
	root := engineFixtureRoot(t, "cpu", "vulkan")
	cfg := map[string]any{
		"server_file": "runtime/vulkan/llama-server", "fallback_server_file": "runtime/cpu/llama-server",
		"gpu_layers": float64(17),
	}
	set := engineUpdates(engineState{
		root: root, cfg: cfg, backend: "cpu",
		serverPresentAtStart: true, backendCompleteAtStart: true,
	})
	if len(set) != 0 {
		t.Errorf("a plain re-run rewrote the engine: %v", set)
	}
}

func TestEngineUpdatesExplicitBackendReplaces(t *testing.T) {
	root := engineFixtureRoot(t, "cpu", "vulkan")
	cfg := map[string]any{"server_file": "runtime/vulkan/llama-server", "gpu_layers": float64(17)}
	set := engineUpdates(engineState{
		root: root, cfg: cfg, backend: "cpu", explicitBackend: true,
		serverPresentAtStart: true, backendCompleteAtStart: true,
	})
	want := map[string]any{
		"server_file": "runtime/cpu/llama-server", "fallback_server_file": "runtime/cpu/llama-server",
		"gpu_layers": 0,
	}
	for k, v := range want {
		if set[k] != v {
			t.Errorf("set[%s] = %v, want %v (all: %v)", k, set[k], v, set)
		}
	}
}

func TestEngineUpdatesMissingServerIsReplaced(t *testing.T) {
	root := engineFixtureRoot(t, "cpu")
	for _, server := range []any{"runtime/missing/llama-server", "../runtime/cpu/llama-server", float64(42), nil} {
		cfg := map[string]any{"server_file": server, "gpu_layers": float64(17)}
		set := engineUpdates(engineState{
			root: root, cfg: cfg, backend: "cpu",
			serverPresentAtStart: false, backendCompleteAtStart: true,
		})
		if set["server_file"] != "runtime/cpu/llama-server" || set["gpu_layers"] != 0 {
			t.Errorf("server %v: set = %v", server, set)
		}
	}
}

func TestEngineUpdatesFillsDefaultsWithoutSwitching(t *testing.T) {
	root := engineFixtureRoot(t, "cpu", "vulkan")
	for _, backend := range []string{"cpu", "vulkan"} {
		cfg := map[string]any{
			// The Windows-style spelling, as the Python test uses.
			"server_file":          "runtime\\" + backend + "\\llama-server",
			"gpu_layers":           nil,
			"fallback_server_file": "missing",
		}
		set := engineUpdates(engineState{
			root: root, cfg: cfg, backend: "cpu",
			serverPresentAtStart: true, backendCompleteAtStart: true,
		})
		wantGPU := 99
		if backend == "cpu" {
			wantGPU = 0
		}
		if set["gpu_layers"] != wantGPU {
			t.Errorf("%s: gpu_layers = %v, want %d (set %v)", backend, set["gpu_layers"], wantGPU, set)
		}
		if set["fallback_server_file"] != "runtime/cpu/llama-server" {
			t.Errorf("%s: fallback = %v (set %v)", backend, set["fallback_server_file"], set)
		}
		if _, changed := set["server_file"]; changed {
			t.Errorf("%s: server_file was rewritten: %v", backend, set)
		}
	}
}

func TestEngineUpdatesNewlyInstalledBackendUpdatesEngine(t *testing.T) {
	root := engineFixtureRoot(t, "cpu")
	custom := filepath.Join(root, "custom", "llama-server")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"server_file": "custom/llama-server", "gpu_layers": float64(17)}
	set := engineUpdates(engineState{
		root: root, cfg: cfg, backend: "cpu",
		serverPresentAtStart: true, backendCompleteAtStart: false, // cpu was installed by this run
	})
	if set["server_file"] != "runtime/cpu/llama-server" {
		t.Errorf("set = %v", set)
	}
}
