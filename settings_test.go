package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHotkeyChoices(t *testing.T) {
	got := hotkeyChoices("Ctrl+Alt+E")
	if len(got) != len(hotkeyPresets) {
		t.Fatalf("a preset must not be added twice: %v", got)
	}
	got = hotkeyChoices("ctrl+alt+e") // case-insensitive match
	if len(got) != len(hotkeyPresets) {
		t.Fatalf("case differences must not add a duplicate: %v", got)
	}
	got = hotkeyChoices("Ctrl+Alt+Q")
	if len(got) != len(hotkeyPresets)+1 || got[len(got)-1] != "Ctrl+Alt+Q" {
		t.Fatalf("a custom hotkey must be appended: %v", got)
	}
}

func TestBackendHelpers(t *testing.T) {
	if got := currentBackend("runtime/cuda/llama-server.exe"); got != "cuda" {
		t.Errorf("currentBackend = %q", got)
	}
	if got := currentBackend(`runtime\vulkan\llama-server.exe`); got != "vulkan" {
		t.Errorf("backslashes: currentBackend = %q", got)
	}
	if got := currentBackend("tools/llama-server.exe"); got != "" {
		t.Errorf("custom location must give no backend, got %q", got)
	}
	if f, n := backendSettings("cpu"); f != "runtime/cpu/llama-server.exe" || n != 0 {
		t.Errorf("cpu settings = %q, %d", f, n)
	}
	if f, n := backendSettings("cuda"); f != "runtime/cuda/llama-server.exe" || n != 99 {
		t.Errorf("cuda settings = %q, %d", f, n)
	}
	dir := t.TempDir()
	for _, b := range []string{"cpu", "vulkan"} {
		p := filepath.Join(dir, "runtime", b)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "llama-server.exe"), []byte("x"), 0o644)
	}
	if got := availableBackends(dir); !reflect.DeepEqual(got, []string{"cpu", "vulkan"}) {
		t.Errorf("availableBackends = %v", got)
	}
}

func TestUpdateConfigFilePreservesEverythingElse(t *testing.T) {
	dir := t.TempDir()
	original := `{
  "hotkey": "Ctrl+Alt+E",
  "port": 18119,
  "some_future_setting": {"a": [1, 2], "b": "x"},
  "gpu_layers": 0
}
`
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(original), 0o644)
	if err := updateConfigFile(dir, map[string]any{"hotkey": "Ctrl+Shift+Y", "auto_paste": true}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := `{
  "hotkey": "Ctrl+Shift+Y",
  "port": 18119,
  "some_future_setting": {"a":[1,2],"b":"x"},
  "gpu_layers": 0,
  "auto_paste": true
}
`
	if string(b) != want {
		t.Fatalf("config.json =\n%s\nwant\n%s", b, want)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != original {
		t.Fatalf("config.json.bak must hold the previous file, got:\n%s", bak)
	}
	// The result must still load as a Config with the other values intact.
	c, err := loadConfig(dir)
	if err != nil || c.Hotkey != "Ctrl+Shift+Y" || c.Port != 18119 {
		t.Fatalf("loadConfig after update: %+v, %v", c, err)
	}
}

func TestUpdateConfigFileMissingAndInvalid(t *testing.T) {
	dir := t.TempDir()
	if err := updateConfigFile(dir, map[string]any{"hotkey": "Ctrl+Alt+T"}); err != nil {
		t.Fatalf("a missing config.json must be created: %v", err)
	}
	if c, err := loadConfig(dir); err != nil || c.Hotkey != "Ctrl+Alt+T" {
		t.Fatalf("created config: %+v, %v", c, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json.bak")); err == nil {
		t.Fatal("there was nothing to back up")
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "config.json"), []byte(`["not an object"]`), 0o644)
	err := updateConfigFile(bad, map[string]any{"hotkey": "x"})
	if err == nil || !strings.Contains(err.Error(), "object") {
		t.Fatalf("a non-object config must be refused, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(bad, "config.json")); string(b) != `["not an object"]` {
		t.Fatal("a refused update must leave the file untouched")
	}
}
