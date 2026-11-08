package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedSetupIsTheRealScript(t *testing.T) {
	onDisk, err := os.ReadFile("setup.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != string(embeddedSetup) {
		t.Fatal("the embedded setup.ps1 differs from the file in the repository")
	}
	s := string(embeddedSetup)
	for _, want := range []string{"param(", "-Backend", "plamo-3-translate-2b", "llama-server.exe", "config.json"} {
		if !strings.Contains(s, want) {
			t.Errorf("embedded setup.ps1 lacks %q", want)
		}
	}
}

func TestEmbeddedMacSetupIsTheRealScript(t *testing.T) {
	onDisk, err := os.ReadFile("setup-macos.sh")
	if err != nil || string(onDisk) != string(embeddedMacSetup) {
		t.Fatalf("embedded macOS setup differs: %v", err)
	}
}

func TestEnsureSetupScript(t *testing.T) {
	dir := t.TempDir()
	if err := ensureSetupScript(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, setupScriptName()))
	if err != nil || string(got) != string(setupContents()) {
		t.Fatalf("the embedded script must be written when none exists: err=%v", err)
	}

	// A script the user edited must never be overwritten.
	custom := []byte("# my own version\n")
	os.WriteFile(filepath.Join(dir, setupScriptName()), custom, 0o644)
	if err := ensureSetupScript(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, setupScriptName())); string(got) != string(custom) {
		t.Fatal("an existing setup script was overwritten")
	}

	// A directory at the script path must not be treated as a valid script.
	dir2 := t.TempDir()
	os.MkdirAll(filepath.Join(dir2, setupScriptName()), 0o755)
	if err := ensureSetupScript(dir2); err == nil {
		t.Fatal("a directory at the script path must not be treated as valid")
	}

	if err := ensureSetupScript(filepath.Join(dir, "no", "such", "folder")); err == nil {
		t.Fatal("a folder that cannot be written must be reported")
	}
}

// The model files must be fetched from a pinned Hugging Face revision and
// checked against a SHA-256 (a moving "main" would break the pinned hashes).
func TestSetupPinsModelDownloads(t *testing.T) {
	s := string(embeddedSetup)
	if strings.Contains(s, "resolve/main") {
		t.Error("setup.ps1 must download the model from a pinned revision, not resolve/main")
	}
	for _, re := range []string{
		`Download-File \(\$hf \+ \$modelInfo\.File\) \$modelFile \$modelInfo\.Sha256`,
		`Download-File \(\$hf \+ 'chat_template\.jinja'\) \$templateFile \$modelInfo\.TemplateSha256`,
		`resolve/\$\(\$modelInfo\.Revision\)/`,
	} {
		if !regexp.MustCompile(re).MatchString(s) {
			t.Errorf("setup.ps1 does not match %s", re)
		}
	}
	// Every model on offer needs a commit and two SHA-256 values.
	entries := regexp.MustCompile(`(?s)'(0\.8b|2b|8b)' = @\{(.*?)\}\r?\n`).FindAllStringSubmatch(s, -1)
	if len(entries) != 3 {
		t.Fatalf("expected 3 model catalog entries, found %d", len(entries))
	}
	for _, e := range entries {
		for _, re := range []string{
			`Revision = '[0-9a-f]{40}'`,
			`\n\s*Sha256 = '[0-9a-f]{64}'`,
			`TemplateSha256 = '[0-9a-f]{64}'`,
			`File = 'plamo-3-translate-` + e[1] + `-Q4_K_M\.gguf'`,
			`Repo = 'plamo-3-translate-` + e[1] + `-GGUF'`,
		} {
			if !regexp.MustCompile(re).MatchString(e[2]) {
				t.Errorf("model %s: %q does not match %s", e[1], e[2], re)
			}
		}
	}
}
