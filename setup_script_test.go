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
		`\$modelRevision = '[0-9a-f]{40}'`,
		`\$modelSha256 = '[0-9a-f]{64}'`,
		`\$templateSha256 = '[0-9a-f]{64}'`,
		`Download-File \(\$hf \+ 'plamo-3-translate-2b-Q4_K_M\.gguf'\) \$modelFile \$modelSha256`,
		`Download-File \(\$hf \+ 'chat_template\.jinja'\) \$templateFile \$templateSha256`,
	} {
		if !regexp.MustCompile(re).MatchString(s) {
			t.Errorf("setup.ps1 does not match %s", re)
		}
	}
}
