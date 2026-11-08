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
	if err := os.WriteFile(manifestFile, []byte("ClipboardTranslator.exe\nSetup.cmd\nStart.cmd\nStop.cmd\nsetup.ps1\nconfig.json\nREADME.txt\nLICENSE\n"), 0o644); err != nil {
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
	// Setup can also install the other sizes (-Model); a leftover download is ours too.
	for _, n := range []string{"plamo-3-translate-8b-Q4_K_M.gguf", "plamo-3-translate-0.8b-Q4_K_M.gguf.partial"} {
		if err := os.WriteFile(filepath.Join(modelsDir, n), []byte("gguf"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runtimeDir := filepath.Join(customDir, "runtime", "cpu")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "llama-server.exe"), []byte("server"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Other tools' models must survive even though they sit next to ours.
	otherModel := filepath.Join(modelsDir, "other-model.gguf")
	if err := os.WriteFile(otherModel, []byte("someone else's model"), 0o644); err != nil {
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
	if content, err := os.ReadFile(otherModel); err != nil || string(content) != "someone else's model" {
		t.Errorf("another tool's model was removed or changed: %v", err)
	}
	for _, n := range []string{"plamo-3-translate-2b-Q4_K_M.gguf", "plamo-3-translate-8b-Q4_K_M.gguf", "plamo-3-translate-0.8b-Q4_K_M.gguf.partial", "chat_template.jinja"} {
		if _, err := os.Stat(filepath.Join(modelsDir, n)); !os.IsNotExist(err) {
			t.Errorf("our model file was not removed: %s", n)
		}
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

// The manifest is a plain text file next to the app: entries that are not bare
// file names must never lead the removal out of the install folder.
func TestUninstallIgnoresManifestEntriesOutsideInstallDir(t *testing.T) {
	baseDir := t.TempDir()
	victim := filepath.Join(baseDir, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	installDir := filepath.Join(baseDir, "app")
	if err := os.MkdirAll(filepath.Join(installDir, "keepdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "ClipboardTranslator.exe"), []byte("exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := strings.Join([]string{`..\victim.txt`, victim, ".", "..", "keepdir", "ClipboardTranslator.exe"}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(installDir, ".installed"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runInstallScript(t, "-Uninstall", "-Yes", "-InstallDir", installDir)
	if err != nil {
		t.Fatalf("uninstall failed: %v\nOutput: %s", err, out)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("a manifest entry deleted a file outside the install folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "keepdir")); err != nil {
		t.Fatalf("a manifest entry deleted a directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "ClipboardTranslator.exe")); !os.IsNotExist(err) {
		t.Errorf("the exe should have been removed")
	}
}

// Files with generic names (config.json, LICENSE...) that predate the install in
// a manifest-tracked folder are only ours if the manifest says so.
func TestUninstallKeepsUnlistedGenericFilesWhenManifestExists(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"ClipboardTranslator.exe": "exe",
		"LICENSE":                 "someone else's license",
		"config.json":             "someone else's config",
		".installed":              "ClipboardTranslator.exe\n",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(installDir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := runInstallScript(t, "-Uninstall", "-Yes", "-InstallDir", installDir); err != nil {
		t.Fatalf("uninstall failed: %v\nOutput: %s", err, out)
	}
	for _, n := range []string{"LICENSE", "config.json"} {
		if _, err := os.Stat(filepath.Join(installDir, n)); err != nil {
			t.Errorf("%s is not in the manifest and must survive: %v", n, err)
		}
	}
}

// Installing must refuse a folder that already holds other files, before any
// download or write happens.
func TestInstallRefusesNonEmptyForeignDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dir, "config.json")
	if err := os.WriteFile(sentinel, []byte("other tool"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runInstallScript(t, "-NoLaunch", "-SkipSetup", "-InstallDir", dir)
	if err == nil {
		t.Fatalf("expected the installer to refuse a non-empty foreign folder; output: %s", out)
	}
	if !strings.Contains(out, "is not empty") {
		t.Fatalf("expected a 'not empty' message, got: %s", out)
	}
	if b, _ := os.ReadFile(sentinel); string(b) != "other tool" {
		t.Fatalf("the foreign file was touched: %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("the installer wrote into the foreign folder: %v", entries)
	}
}

// Per-user registrations that point at a different folder are not ours to remove.
func TestUninstallLeavesOtherInstallsShortcut(t *testing.T) {
	programs := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Clipboard Translator.lnk")
	_, beforeErr := os.Stat(programs)
	installDir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "ClipboardTranslator.exe"), []byte("exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runInstallScript(t, "-Uninstall", "-Yes", "-InstallDir", installDir); err != nil {
		t.Fatalf("uninstall failed: %v\nOutput: %s", err, out)
	}
	if _, afterErr := os.Stat(programs); beforeErr == nil && afterErr != nil {
		t.Fatalf("uninstalling a copy in a temp folder removed the real Start menu shortcut")
	}
}

func TestInstallHotkeyLookupProfiles(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"top-level hotkey", `{"hotkey": "Ctrl+Shift+E"}`, "Ctrl+Shift+E"},
		{"profiles only", `{"profiles": [{"hotkey": "Ctrl+Alt+T"}]}`, "Ctrl+Alt+T"},
		{"empty hotkey falls back to profiles", `{"hotkey": "", "profiles": [{"hotkey": "Ctrl+Alt+J"}]}`, "Ctrl+Alt+J"},
		{"default fallback", `{}`, "Ctrl+Alt+E"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tc.json), 0o644); err != nil {
				t.Fatal(err)
			}
			script := `$InstallDir = '` + dir + `'; Set-StrictMode -Version Latest; $hotkey = 'Ctrl+Alt+E'; try { $cfg = Get-Content -LiteralPath (Join-Path $InstallDir 'config.json') -Raw -Encoding UTF8 | ConvertFrom-Json; $hk = $null; if ($cfg.PSObject.Properties.Name -contains 'hotkey' -and $cfg.hotkey) { $hk = [string]$cfg.hotkey } elseif ($cfg.PSObject.Properties.Name -contains 'profiles') { $p = @($cfg.profiles); if ($p.Count -gt 0 -and $p[0].PSObject.Properties.Name -contains 'hotkey' -and $p[0].hotkey) { $hk = [string]$p[0].hotkey } }; if ($hk) { $hotkey = $hk } } catch { }; Write-Output $hotkey`
			cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("powershell failed: %v\nOutput: %s", err, out)
			}
			got := strings.TrimSpace(string(out))
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
