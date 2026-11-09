package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func findPython() (string, error) {
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("neither python3 nor python found in PATH")
}

func TestModelPinPrinterMatchesCatalog(t *testing.T) {
	py, err := findPython()
	if err != nil {
		t.Skip("skipping python printer check: python not in PATH")
	}

	for _, m := range modelCatalog {
		cmd := exec.Command(py, "tools/print_model_pin.py", m.Key)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tools/print_model_pin.py %s failed: %v, out: %s", m.Key, err, string(out))
		}
		fields := strings.Fields(strings.TrimSpace(string(out)))
		if len(fields) != 5 {
			t.Fatalf("expected 5 fields for %s, got %d: %v", m.Key, len(fields), fields)
		}
		file, modelURL, sha, tmplURL, tmplSha := fields[0], fields[1], fields[2], fields[3], fields[4]

		if file != m.File {
			t.Errorf("model %s: file is %q, want %q", m.Key, file, m.File)
		}
		if sha != strings.ToLower(m.Sha256) {
			t.Errorf("model %s: sha256 is %q, want %q", m.Key, sha, m.Sha256)
		}
		if tmplSha != strings.ToLower(m.TemplateSha256) {
			t.Errorf("model %s: template_sha256 is %q, want %q", m.Key, tmplSha, m.TemplateSha256)
		}
		wantModelURL := "https://huggingface.co/pfnet/" + m.Repo + "/resolve/" + m.Revision + "/" + m.File
		if modelURL != wantModelURL {
			t.Errorf("model %s: model_url is %q, want %q", m.Key, modelURL, wantModelURL)
		}
		wantTmplURL := "https://huggingface.co/pfnet/" + m.Repo + "/resolve/" + m.Revision + "/chat_template.jinja"
		if tmplURL != wantTmplURL {
			t.Errorf("model %s: template_url is %q, want %q", m.Key, tmplURL, wantTmplURL)
		}
	}
}
