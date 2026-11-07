//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runInstallScript(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "install.ps1")
	cmdArgs := append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}, args...)
	cmd := exec.Command("powershell.exe", cmdArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallDirRootAndUnsafeRejected(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		want string
	}{
		{"drive root backslash", `C:\`, "drive root or unsafe directory"},
		{"drive root short", `C:`, "drive root or unsafe directory"},
		{"empty string", ` `, "cannot be empty"},
		{"windows directory", os.Getenv("SystemRoot"), "system directory"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runInstallScript(t, "-Uninstall", "-Yes", "-InstallDir", tc.dir)
			if err == nil {
				t.Fatalf("expected error for directory %q, got success; output: %s", tc.dir, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("expected error containing %q, got: %s", tc.want, out)
			}
		})
	}
}

func TestUninstallPreservesUnrelatedFiles(t *testing.T) {
	baseDir := t.TempDir()
	customDir := filepath.Join(baseDir, "CustomTools")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Unrelated sentinel files and subdirectories
	sentinelFile := filepath.Join(customDir, "sentinel.txt")
	if err := os.WriteFile(sentinelFile, []byte("important user document"), 0o644); err != nil {
		t.Fatal(err)
	}
	otherSubDir := filepath.Join(customDir, "other_app")
	if err := os.MkdirAll(otherSubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	otherFile := filepath.Join(otherSubDir, "data.bin")
	if err := os.WriteFile(otherFile, []byte("important binary data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// App-managed files
	appFiles := []string{
		"ClipboardTranslator.exe",
		"config.json",
		"config.json.bak",
		"Setup.cmd",
		"Start.cmd",
		"Stop.cmd",
		"setup.ps1",
		"install.ps1",
		"README.txt",
		"LICENSE",
		"translator.log",
		"server.log",
		"STOP",
	}
	for _, f := range appFiles {
		if err := os.WriteFile(filepath.Join(customDir, f), []byte("dummy app content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manifestFile := filepath.Join(customDir, ".installed")
	if err := os.WriteFile(manifestFile, []byte("ClipboardTranslator.exe\nREADME.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// App-managed directories
	modelsDir := filepath.Join(customDir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelsDir, "plamo-3-translate-2b-Q4_K_M.gguf"), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelsDir, "chat_template.jinja"), []byte("jinja"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtimeDir := filepath.Join(customDir, "runtime", "cpu")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "llama-server.exe"), []byte("server"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run uninstall with -Yes
	out, err := runInstallScript(t, "-Uninstall", "-Yes", "-InstallDir", customDir)
	if err != nil {
		t.Fatalf("uninstall failed: %v\nOutput: %s", err, out)
	}

	// Verify unrelated files and directories survived
	if content, err := os.ReadFile(sentinelFile); err != nil || string(content) != "important user document" {
		t.Fatalf("sentinel file was corrupted or deleted: %v, content: %q", err, string(content))
	}
	if content, err := os.ReadFile(otherFile); err != nil || string(content) != "important binary data" {
		t.Fatalf("unrelated sub-file was corrupted or deleted: %v, content: %q", err, string(content))
	}
	if _, err := os.Stat(customDir); err != nil {
		t.Fatalf("custom install directory must be preserved when unrelated files exist: %v", err)
	}

	// Verify app-managed files were removed
	for _, f := range appFiles {
		p := filepath.Join(customDir, f)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("managed file was not removed: %s", f)
		}
	}
	if _, err := os.Stat(manifestFile); !os.IsNotExist(err) {
		t.Errorf("manifest file was not removed: %s", manifestFile)
	}
	if _, err := os.Stat(modelsDir); !os.IsNotExist(err) {
		t.Errorf("models dir was not removed: %s", modelsDir)
	}
	if _, err := os.Stat(filepath.Join(customDir, "runtime")); !os.IsNotExist(err) {
		t.Errorf("runtime dir was not removed: %s", filepath.Join(customDir, "runtime"))
	}
}

func TestUninstallRemovesDedicatedDirectory(t *testing.T) {
	baseDir := t.TempDir()
	dedicatedDir := filepath.Join(baseDir, "DedicatedClipboardTranslator")
	if err := os.MkdirAll(dedicatedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Only app-managed files
	if err := os.WriteFile(filepath.Join(dedicatedDir, "ClipboardTranslator.exe"), []byte("exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dedicatedDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	modelsDir := filepath.Join(dedicatedDir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelsDir, "plamo-3-translate-2b-Q4_K_M.gguf"), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runInstallScript(t, "-Uninstall", "-Yes", "-InstallDir", dedicatedDir)
	if err != nil {
		t.Fatalf("uninstall failed: %v\nOutput: %s", err, out)
	}

	// In a dedicated directory with no other files, the directory itself must be removed
	if _, err := os.Stat(dedicatedDir); !os.IsNotExist(err) {
		t.Fatalf("empty dedicated directory should be removed, but still exists: %v", err)
	}
}
