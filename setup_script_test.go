package main

import (
	"os"
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

// The setup scripts must install a pinned llama.cpp release tag and verify
// downloads against pinned SHA-256 hashes instead of searching recent releases.
func TestLlamaCppPinAgreesAcrossScripts(t *testing.T) {
	ps1Bytes, err := os.ReadFile("setup.ps1")
	if err != nil {
		t.Fatal(err)
	}
	shBytes, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	macBytes, err := os.ReadFile("setup-macos.sh")
	if err != nil {
		t.Fatal(err)
	}

	scripts := map[string]string{
		"setup.ps1":      string(ps1Bytes),
		"setup.sh":       string(shBytes),
		"setup-macos.sh": string(macBytes),
	}

	const (
		startMarker = "# --- llama.cpp pinned release (managed by tools/bump_llama_pin.py) ---"
		endMarker   = "# --- end llama.cpp pinned release ---"
	)

	// No script may still contain the dynamic release discovery query.
	for name, s := range scripts {
		if strings.Contains(s, "releases?per_page") {
			t.Errorf("%s still contains dynamic release search 'releases?per_page'", name)
		}
		if !strings.Contains(s, startMarker) {
			t.Errorf("%s lacks start marker %q", name, startMarker)
		}
		if !strings.Contains(s, endMarker) {
			t.Errorf("%s lacks end marker %q", name, endMarker)
		}
	}

	tagRe := regexp.MustCompile(`(?:Tag = |LLAMA_TAG = )'([^']+)'`)
	var tags = make(map[string]string)
	for name, s := range scripts {
		m := tagRe.FindStringSubmatch(s)
		if len(m) < 2 {
			t.Fatalf("%s lacks pinned release tag", name)
		}
		tags[name] = m[1]
		if !regexp.MustCompile(`^b[0-9]+$`).MatchString(m[1]) {
			t.Errorf("%s tag %q is not a valid llama.cpp release tag (expected bXXXX)", name, m[1])
		}
	}

	pinnedTag := tags["setup.ps1"]
	for name, tag := range tags {
		if tag != pinnedTag {
			t.Errorf("%s tag %q does not match setup.ps1 tag %q", name, tag, pinnedTag)
		}
	}

	// Verify setup.ps1 assets
	psAssetRe := regexp.MustCompile(`'(\w+)'\s*=\s*\[ordered\]@\{[^}]*?File\s*=\s*'([^']+)'[^}]*?Sha256\s*=\s*'([0-9a-fA-F]{64})'`)
	psMatches := psAssetRe.FindAllStringSubmatch(scripts["setup.ps1"], -1)
	psAssets := make(map[string][2]string) // key -> [file, sha256]
	for _, m := range psMatches {
		psAssets[m[1]] = [2]string{m[2], strings.ToLower(m[3])}
	}
	for _, k := range []string{"cpu", "cuda", "cudart", "vulkan"} {
		a, ok := psAssets[k]
		if !ok {
			t.Errorf("setup.ps1 missing %s asset", k)
			continue
		}
		if len(a[1]) != 64 {
			t.Errorf("setup.ps1 %s hash length %d != 64", k, len(a[1]))
		}
	}
	if got := psAssets["cpu"][0]; got != "llama-"+pinnedTag+"-bin-win-cpu-x64.zip" {
		t.Errorf("setup.ps1 cpu asset file is %q", got)
	}
	if got := psAssets["cuda"][0]; got != "llama-"+pinnedTag+"-bin-win-cuda-12.4-x64.zip" {
		t.Errorf("setup.ps1 cuda asset file is %q", got)
	}
	if got := psAssets["cudart"][0]; !regexp.MustCompile(`^cudart-llama.*-bin-win-cuda-12\.4-x64\.zip$`).MatchString(got) {
		t.Errorf("setup.ps1 cudart asset file is %q", got)
	}
	if got := psAssets["vulkan"][0]; got != "llama-"+pinnedTag+"-bin-win-vulkan-x64.zip" {
		t.Errorf("setup.ps1 vulkan asset file is %q", got)
	}

	// Verify setup.sh assets
	pyAssetRe := regexp.MustCompile(`'(\w+)'\s*:\s*\{[^}]*?'file'\s*:\s*'([^']+)'[^}]*?'sha256'\s*:\s*'([0-9a-fA-F]{64})'`)
	shMatches := pyAssetRe.FindAllStringSubmatch(scripts["setup.sh"], -1)
	shAssets := make(map[string][2]string)
	for _, m := range shMatches {
		shAssets[m[1]] = [2]string{m[2], strings.ToLower(m[3])}
	}
	for _, k := range []string{"cpu", "vulkan"} {
		a, ok := shAssets[k]
		if !ok {
			t.Errorf("setup.sh missing %s asset", k)
			continue
		}
		if len(a[1]) != 64 {
			t.Errorf("setup.sh %s hash length %d != 64", k, len(a[1]))
		}
	}
	if got := shAssets["cpu"][0]; got != "llama-"+pinnedTag+"-bin-ubuntu-x64.tar.gz" {
		t.Errorf("setup.sh cpu asset file is %q", got)
	}
	if got := shAssets["vulkan"][0]; got != "llama-"+pinnedTag+"-bin-ubuntu-vulkan-x64.tar.gz" {
		t.Errorf("setup.sh vulkan asset file is %q", got)
	}

	// Verify setup-macos.sh assets
	macMatches := pyAssetRe.FindAllStringSubmatch(scripts["setup-macos.sh"], -1)
	macAssets := make(map[string][2]string)
	for _, m := range macMatches {
		macAssets[m[1]] = [2]string{m[2], strings.ToLower(m[3])}
	}
	for _, k := range []string{"arm64", "x64"} {
		a, ok := macAssets[k]
		if !ok {
			t.Errorf("setup-macos.sh missing %s asset", k)
			continue
		}
		if len(a[1]) != 64 {
			t.Errorf("setup-macos.sh %s hash length %d != 64", k, len(a[1]))
		}
	}
	if got := macAssets["arm64"][0]; got != "llama-"+pinnedTag+"-bin-macos-arm64.tar.gz" {
		t.Errorf("setup-macos.sh arm64 asset file is %q", got)
	}
	if got := macAssets["x64"][0]; got != "llama-"+pinnedTag+"-bin-macos-x64.tar.gz" {
		t.Errorf("setup-macos.sh x64 asset file is %q", got)
	}
}
