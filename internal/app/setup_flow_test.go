package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func TestEnsureSetupScript(t *testing.T) {
	// The real scripts are embedded by the main package; any content will do here.
	oldW, oldL, oldM := embeddedSetup, embeddedLinuxSetup, embeddedMacSetup
	script := []byte("# test script\n")
	embeddedSetup, embeddedLinuxSetup, embeddedMacSetup = script, script, script
	t.Cleanup(func() { embeddedSetup, embeddedLinuxSetup, embeddedMacSetup = oldW, oldL, oldM })
	dir := t.TempDir()
	if err := ensureSetupScript(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, platform.SetupScriptName()))
	if err != nil || string(got) != string(setupContents()) {
		t.Fatalf("the embedded script must be written when none exists: err=%v", err)
	}

	// A script the user edited must never be overwritten.
	custom := []byte("# my own version\n")
	os.WriteFile(filepath.Join(dir, platform.SetupScriptName()), custom, 0o644)
	if err := ensureSetupScript(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, platform.SetupScriptName())); string(got) != string(custom) {
		t.Fatal("an existing setup script was overwritten")
	}

	// A directory at the script path must not be treated as a valid script.
	dir2 := t.TempDir()
	os.MkdirAll(filepath.Join(dir2, platform.SetupScriptName()), 0o755)
	if err := ensureSetupScript(dir2); err == nil {
		t.Fatal("a directory at the script path must not be treated as valid")
	}

	if err := ensureSetupScript(filepath.Join(dir, "no", "such", "folder")); err == nil {
		t.Fatal("a folder that cannot be written must be reported")
	}
}

// The model files must be fetched from a pinned Hugging Face revision and
// checked against a SHA-256 (a moving "main" would break the pinned hashes).
