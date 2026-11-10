package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/models"
)

func TestChatTemplateFreshInstall(t *testing.T) {
	// 1. Default config points to models/chat_template.2b.jinja
	conf := config.Default()
	if conf.TemplateFile != "models/chat_template.2b.jinja" {
		t.Fatalf("default template_file = %q, want models/chat_template.2b.jinja", conf.TemplateFile)
	}

	a, _, _ := newTestApp(t)
	m2b := models.Catalog[1]
	putModel(t, a.dir, m2b)
	a.conf.ModelFile = "models/" + m2b.File
	a.conf.TemplateFile = "models/" + m2b.TemplateFile()

	// Before template is present: missingFor must report it and startServer must fail.
	paths, err := config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	missing := paths.MissingFor(a.conf, a.dir)
	if len(missing) != 1 || !strings.Contains(missing[0], "chat_template.2b.jinja") {
		t.Fatalf("expected template missing, got: %v", missing)
	}
	a.paths = paths
	a.sup.Model = paths.Model
	a.sup.Template = paths.Template
	if err := a.startServer(); err == nil {
		t.Fatal("startServer must fail when template is missing")
	}

	// Install the pinned 2B template from testdata:
	tplBytes, err := os.ReadFile("testdata/chat_template.2b.jinja")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.dir, "models", m2b.TemplateFile()), tplBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Now template is installed:
	if !m2b.TemplateInstalled(a.dir) {
		t.Fatal("templateInstalled must be true after writing pinned template")
	}
	paths, err = config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	a.paths = paths
	a.sup.Model = paths.Model
	a.sup.Template = paths.Template

	missing = paths.MissingFor(a.conf, a.dir)
	if len(missing) != 0 {
		t.Fatalf("expected no missing files, got: %v", missing)
	}

	if err := a.startServer(); err != nil {
		t.Fatalf("startServer failed: %v", err)
	}
	if !models.VerifyFileSha256(a.sup.Template, m2b.TemplateSha256) {
		t.Errorf("running template sha256 mismatch")
	}

	// A corrupted template must prevent server start:
	a.sup.Stop()
	if err := os.WriteFile(filepath.Join(a.dir, "models", m2b.TemplateFile()), []byte("corrupted template"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.startServer(); err == nil || !strings.Contains(err.Error(), "does not match expected SHA-256") {
		t.Fatalf("startServer must refuse corrupted template, got: %v", err)
	}
}

func TestChatTemplateExistingInstallWithOld2BPin(t *testing.T) {
	a, _, _ := newTestApp(t)
	m2b := models.Catalog[1]
	putModel(t, a.dir, m2b)
	a.conf.ModelFile = "models/" + m2b.File
	a.conf.TemplateFile = "models/chat_template.jinja" // legacy config setting

	// Write old 2B template into legacy path models/chat_template.jinja
	oldTpl, err := os.ReadFile("testdata/chat_template.old_2b.jinja")
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(a.dir, "models", "chat_template.jinja")
	if err := os.WriteFile(legacyPath, oldTpl, 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Old 2B template must NOT count as installed
	if m2b.TemplateInstalled(a.dir) {
		t.Fatal("templateInstalled must be false for old 2B pin")
	}
	paths, err := config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	missing := paths.MissingFor(a.conf, a.dir)
	if len(missing) == 0 {
		t.Fatal("missingFor must detect stale template as missing")
	}

	// 2. startServer must refuse to start with stale template
	a.paths = paths
	a.sup.Model = paths.Model
	a.sup.Template = paths.Template
	if err := a.startServer(); err == nil || !strings.Contains(err.Error(), "does not match expected SHA-256") {
		t.Fatalf("startServer must refuse stale template: %v", err)
	}

	// 3. When new pinned template is downloaded (to chat_template.2b.jinja):
	newTpl, err := os.ReadFile("testdata/chat_template.2b.jinja")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.dir, "models", m2b.TemplateFile()), newTpl, 0o644); err != nil {
		t.Fatal(err)
	}

	// Now m2b.templateInstalled is true
	if !m2b.TemplateInstalled(a.dir) {
		t.Fatal("templateInstalled must be true after new template is placed")
	}
	// resolvePaths automatically picks the verified primary template even if conf has legacy path
	paths, err = config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Template != filepath.Join(a.dir, "models", m2b.TemplateFile()) {
		t.Fatalf("resolvePaths must pick %s, got %s", m2b.TemplateFile(), paths.Template)
	}
	a.paths = paths
	a.sup.Template = paths.Template
	if err := a.startServer(); err != nil {
		t.Fatalf("startServer must succeed with new template: %v", err)
	}

	// 4. Test existing install with matching legacy template:
	// If models/chat_template.jinja HAS the matching hash, it is used
	a.sup.Stop()
	_ = os.Remove(filepath.Join(a.dir, "models", m2b.TemplateFile()))
	if err := os.WriteFile(legacyPath, newTpl, 0o644); err != nil {
		t.Fatal(err)
	}
	if !m2b.TemplateInstalled(a.dir) {
		t.Fatal("templateInstalled must be true when legacy template has matching hash")
	}
	paths, err = config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Template != legacyPath {
		t.Fatalf("resolvePaths must fall back to matching legacy template %s, got %s", legacyPath, paths.Template)
	}
	a.paths = paths
	a.sup.Template = paths.Template
	if err := a.startServer(); err != nil {
		t.Fatalf("startServer must succeed with matching legacy template: %v", err)
	}
}

func TestChatTemplateModelSwitch(t *testing.T) {
	a, _, _ := newTestApp(t)
	m08 := models.Catalog[0]
	m2b := models.Catalog[1]
	m8b := models.Catalog[2]

	putModel(t, a.dir, m08)
	putModel(t, a.dir, m2b)
	putModel(t, a.dir, m8b)

	tpl08Bytes, err := os.ReadFile("testdata/chat_template.0.8b.jinja")
	if err != nil {
		t.Fatal(err)
	}
	tpl2bBytes, err := os.ReadFile("testdata/chat_template.2b.jinja")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(a.dir, "models", m08.TemplateFile()), tpl08Bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.dir, "models", m2b.TemplateFile()), tpl2bBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	// 8b shares the same template hash as 0.8b
	if err := os.WriteFile(filepath.Join(a.dir, "models", m8b.TemplateFile()), tpl08Bytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Start on 2B
	a.conf.ModelFile = "models/" + m2b.File
	a.conf.TemplateFile = "models/" + m2b.TemplateFile()
	paths, err := config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	a.paths = paths
	a.sup.Model = paths.Model
	a.sup.Template = paths.Template
	if err := a.startServer(); err != nil {
		t.Fatalf("startServer on 2B failed: %v", err)
	}
	if !models.VerifyFileSha256(a.sup.Template, m2b.TemplateSha256) {
		t.Fatalf("2B template sha mismatch")
	}

	// Switch 2B -> 0.8B
	if _, err := a.checkModelSwitch("0.8b"); err != nil {
		t.Fatalf("checkModelSwitch(0.8b) failed: %v", err)
	}
	if err := a.switchModel(m08); err != nil {
		t.Fatalf("switchModel(0.8b) failed: %v", err)
	}
	if a.conf.TemplateFile != "models/chat_template.0.8b.jinja" {
		t.Errorf("conf.TemplateFile = %q, want models/chat_template.0.8b.jinja", a.conf.TemplateFile)
	}
	if a.sup.Template != filepath.Join(a.dir, "models", "chat_template.0.8b.jinja") {
		t.Errorf("sup.Template = %q, want ...chat_template.0.8b.jinja", a.sup.Template)
	}
	if !models.VerifyFileSha256(a.sup.Template, m08.TemplateSha256) {
		t.Errorf("running 0.8B template sha mismatch")
	}

	// Verify config.json on disk was updated with template_file
	raw, err := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"template_file": "models/chat_template.0.8b.jinja"`) {
		t.Errorf("config.json missing updated template_file:\n%s", raw)
	}

	// Switch 0.8B -> 8B
	if _, err := a.checkModelSwitch("8b"); err != nil {
		t.Fatalf("checkModelSwitch(8b) failed: %v", err)
	}
	if err := a.switchModel(m8b); err != nil {
		t.Fatalf("switchModel(8b) failed: %v", err)
	}
	if a.conf.TemplateFile != "models/chat_template.8b.jinja" {
		t.Errorf("conf.TemplateFile = %q, want models/chat_template.8b.jinja", a.conf.TemplateFile)
	}
	if !models.VerifyFileSha256(a.sup.Template, m8b.TemplateSha256) {
		t.Errorf("running 8B template sha mismatch")
	}

	// Switch back 8B -> 2B
	if _, err := a.checkModelSwitch("2b"); err != nil {
		t.Fatalf("checkModelSwitch(2b) failed: %v", err)
	}
	if err := a.switchModel(m2b); err != nil {
		t.Fatalf("switchModel(2b) failed: %v", err)
	}
	if a.conf.TemplateFile != "models/chat_template.2b.jinja" {
		t.Errorf("conf.TemplateFile = %q, want models/chat_template.2b.jinja", a.conf.TemplateFile)
	}
	if !models.VerifyFileSha256(a.sup.Template, m2b.TemplateSha256) {
		t.Errorf("running 2B template sha mismatch")
	}
}

func TestChatTemplateSwitchRefusesMismatchedTemplate(t *testing.T) {
	a, _, _ := newTestApp(t)
	m08 := models.Catalog[0]
	m2b := models.Catalog[1]

	putModel(t, a.dir, m08)
	putModel(t, a.dir, m2b)

	tpl2bBytes, err := os.ReadFile("testdata/chat_template.2b.jinja")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.dir, "models", m2b.TemplateFile()), tpl2bBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Write bad template for 0.8B
	if err := os.WriteFile(filepath.Join(a.dir, "models", m08.TemplateFile()), []byte("bad template hash"), 0o644); err != nil {
		t.Fatal(err)
	}

	a.conf.ModelFile = "models/" + m2b.File
	a.conf.TemplateFile = "models/" + m2b.TemplateFile()
	paths, _ := config.ResolvePaths(a.dir, a.conf)
	a.paths = paths
	a.sup.Model = paths.Model
	a.sup.Template = paths.Template
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}

	// checkModelSwitch must refuse
	if _, err := a.checkModelSwitch("0.8b"); err == nil || !strings.Contains(err.Error(), "chat template is missing or incomplete") {
		t.Fatalf("checkModelSwitch must reject mismatched template, got: %v", err)
	}

	// switchModel directly must also refuse and preserve running model
	if err := a.switchModel(m08); err == nil || !strings.Contains(err.Error(), "does not match expected SHA-256") {
		t.Fatalf("switchModel must refuse mismatched template, got: %v", err)
	}
	if a.conf.ModelFile != "models/"+m2b.File {
		t.Errorf("model changed after failed switch: %s", a.conf.ModelFile)
	}
	if a.sup.Template != paths.Template {
		t.Errorf("template changed after failed switch: %s", a.sup.Template)
	}
}

func TestChatTemplateCustomTemplatePreserved(t *testing.T) {
	a, _, _ := newTestApp(t)
	m08 := models.Catalog[0]
	m2b := models.Catalog[1]
	putModel(t, a.dir, m08)
	putModel(t, a.dir, m2b)

	customContent := []byte("custom jinja template")
	customRel := "models/my_custom.jinja"
	customPath := filepath.Join(a.dir, filepath.FromSlash(customRel))
	if err := os.WriteFile(customPath, customContent, 0o644); err != nil {
		t.Fatal(err)
	}

	if !models.IsCustomTemplate(customRel) {
		t.Fatalf("models.IsCustomTemplate(%q) must be true", customRel)
	}

	a.conf.ModelFile = "models/" + m2b.File
	a.conf.TemplateFile = customRel
	// In a real install, custom template is set in config.json:
	if err := os.WriteFile(filepath.Join(a.dir, "config.json"), []byte(`{"template_file": "`+customRel+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	a.paths = paths
	a.sup.Model = paths.Model
	a.sup.Template = paths.Template

	// Starts without SHA-256 check
	if err := a.startServer(); err != nil {
		t.Fatalf("startServer with custom template failed: %v", err)
	}
	if a.sup.Template != customPath {
		t.Errorf("sup.Template = %s, want %s", a.sup.Template, customPath)
	}

	// Switch model to 0.8B: custom template must be kept and not hashed
	if err := a.switchModel(m08); err != nil {
		t.Fatalf("switchModel with custom template failed: %v", err)
	}
	if a.conf.TemplateFile != customRel {
		t.Errorf("conf.TemplateFile = %q, want %q", a.conf.TemplateFile, customRel)
	}
	if a.sup.Template != customPath {
		t.Errorf("sup.Template = %q, want %q", a.sup.Template, customPath)
	}

	// Verify config.json kept custom template
	raw, err := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"template_file": "models/my_custom.jinja"`) {
		t.Errorf("config.json did not preserve custom template:\n%s", raw)
	}
}
