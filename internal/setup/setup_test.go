package setup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/models"
)

// The tests never contact the real hosts: a stub transport serves fixture
// bytes, and the catalog / llama pins are swapped for fixture values whose
// hashes match those bytes (the production pins stay untouched).

type stubTransport struct {
	mu      sync.Mutex
	calls   []string
	handler func(url string) ([]byte, int)
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req.URL.String())
	s.mu.Unlock()
	body, code := s.handler(req.URL.String())
	return &http.Response{
		StatusCode: code,
		Status:     http.StatusText(code),
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func (s *stubTransport) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var (
	fixtureModel    = []byte("fixture model bytes")
	fixtureModel8   = []byte("fixture 8b model bytes")
	fixtureTemplate = []byte(strings.Repeat("t", 128))
)

// fixtureCatalog replaces models.Catalog with two entries backed by the
// fixture bytes, and returns a restore function.
func fixtureCatalog(t *testing.T) func() {
	t.Helper()
	old := models.Catalog
	models.Catalog = []models.Entry{
		{Key: "2b", Name: "Fixture 2B", File: "fixture-2b.gguf", Repo: "fixture-2b-repo",
			Revision: "rev2b", Sha256: sha256Hex(fixtureModel), TemplateSha256: sha256Hex(fixtureTemplate),
			SizeLabel: "1.8 GB", MinBytes: 4},
		{Key: "8b", Name: "Fixture 8B", File: "fixture-8b.gguf", Repo: "fixture-8b-repo",
			Revision: "rev8b", Sha256: sha256Hex(fixtureModel8), TemplateSha256: sha256Hex(fixtureTemplate),
			SizeLabel: "5.3 GB", MinBytes: 4},
		{Key: "0.8b", Name: "Fixture 0.8B", File: "fixture-0.8b.gguf", Repo: "fixture-08-repo",
			Revision: "rev08", Sha256: sha256Hex(fixtureModel), TemplateSha256: sha256Hex(fixtureTemplate),
			SizeLabel: "0.6 GB", MinBytes: 4},
	}
	return func() { models.Catalog = old }
}

// fixtureTar builds the runtime archive the pinned llama.cpp release would
// carry: one llama-server, a library and a symlink to it.
func fixtureTar(t *testing.T, unsafe bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	serverName := "llama-fixture/llama-server"
	if unsafe {
		serverName = "../escaped"
	}
	files := []struct {
		name string
		body []byte
		mode int64
	}{
		{serverName, []byte("#!/bin/sh\nexit 0\n"), 0o755},
		{"llama-fixture/libggml.so.1", []byte("fixture"), 0o644},
	}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: "llama-fixture/libggml.so", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "libggml.so.1"}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fixturePins points the linux llama assets at archive and returns a restore function.
func fixturePins(t *testing.T, archive []byte) func() {
	t.Helper()
	oldCPU, oldVulkan := llamaAssets["linux/cpu"], llamaAssets["linux/vulkan"]
	pin := llamaAsset{File: "llama-fixture.tar.gz", Sha256: sha256Hex(archive)}
	llamaAssets["linux/cpu"] = pin
	llamaAssets["linux/vulkan"] = pin
	return func() {
		llamaAssets["linux/cpu"] = oldCPU
		llamaAssets["linux/vulkan"] = oldVulkan
	}
}

func fixtureTransport(archive []byte) *stubTransport {
	return &stubTransport{handler: func(url string) ([]byte, int) {
		switch {
		case strings.Contains(url, "fixture-2b-repo"):
			if strings.HasSuffix(url, "chat_template.jinja") {
				return fixtureTemplate, 200
			}
			return fixtureModel, 200
		case strings.Contains(url, "fixture-8b-repo"):
			if strings.HasSuffix(url, "chat_template.jinja") {
				return fixtureTemplate, 200
			}
			return fixtureModel8, 200
		case strings.Contains(url, "fixture-08-repo"):
			if strings.HasSuffix(url, "chat_template.jinja") {
				return fixtureTemplate, 200
			}
			return fixtureModel, 200
		case strings.Contains(url, "github.com/ggml-org/llama.cpp"):
			return archive, 200
		}
		return nil, 404
	}}
}

func readConfigMap(t *testing.T, root string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeConfigMap(t *testing.T, root string, cfg map[string]any) {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func runOK(t *testing.T, root string, tr http.RoundTripper, opt Options) {
	t.Helper()
	opt.Root = root
	opt.Transport = tr
	opt.Out = io.Discard
	if code := Run(opt); code != 0 {
		t.Fatalf("setup exited %d", code)
	}
}

func TestRunFreshInstallAndIdempotentRerun(t *testing.T) {
	defer fixtureCatalog(t)()
	archive := fixtureTar(t, false)
	defer fixturePins(t, archive)()
	tr := fixtureTransport(archive)
	root := t.TempDir()
	writeConfigMap(t, root, map[string]any{"hotkey": "Ctrl+Alt+T", "future_key": map[string]any{"keep": true}})

	runOK(t, root, tr, Options{})

	server := filepath.Join(root, "runtime", "cpu", "llama-server")
	info, err := os.Stat(server)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("llama-server is not executable")
	}
	link, err := os.Lstat(filepath.Join(root, "runtime", "cpu", "libggml.so"))
	if err != nil || link.Mode()&os.ModeSymlink == 0 {
		t.Errorf("library symlink not preserved: %v %v", link, err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "runtime", "cpu", "libggml.so")); err != nil || string(b) != "fixture" {
		t.Errorf("symlink target content: %q %v", b, err)
	}
	cfg := readConfigMap(t, root)
	if cfg["server_file"] != "runtime/cpu/llama-server" || cfg["fallback_server_file"] != "runtime/cpu/llama-server" || cfg["gpu_layers"] != float64(0) {
		t.Errorf("engine config: %v", cfg)
	}
	if cfg["hotkey"] != "Ctrl+Alt+T" {
		t.Errorf("user config lost: %v", cfg)
	}
	if _, ok := cfg["model_file"]; ok {
		t.Error("a plain run must not add model_file")
	}
	if _, err := os.Stat(filepath.Join(root, "config.json.bak")); err != nil {
		t.Error("config.json.bak missing after a run that changed config.json")
	}

	before, _ := os.ReadFile(filepath.Join(root, "config.json"))
	calls := tr.callCount()
	runOK(t, root, tr, Options{})
	if got := tr.callCount(); got != calls {
		t.Errorf("an installed setup contacted the network on re-run (%d extra calls)", got-calls)
	}
	after, _ := os.ReadFile(filepath.Join(root, "config.json"))
	if !bytes.Equal(before, after) {
		t.Error("config.json changed on an idempotent re-run")
	}
}

func TestRunFreshInstallWithoutConfigWritesOnlyEngineKeys(t *testing.T) {
	defer fixtureCatalog(t)()
	archive := fixtureTar(t, false)
	defer fixturePins(t, archive)()
	tr := fixtureTransport(archive)
	root := t.TempDir()

	runOK(t, root, tr, Options{})
	cfg := readConfigMap(t, root)
	want := map[string]any{
		"server_file": "runtime/cpu/llama-server", "fallback_server_file": "runtime/cpu/llama-server",
		"gpu_layers": float64(0),
	}
	if len(cfg) != len(want) {
		t.Fatalf("fresh config = %v, want exactly %v", cfg, want)
	}
	for k, v := range want {
		if cfg[k] != v {
			t.Errorf("config[%s] = %v, want %v", k, cfg[k], v)
		}
	}
}

func TestRunExplicitModelAndBackendAreKept(t *testing.T) {
	defer fixtureCatalog(t)()
	archive := fixtureTar(t, false)
	defer fixturePins(t, archive)()
	tr := fixtureTransport(archive)
	root := t.TempDir()

	runOK(t, root, tr, Options{Backend: "vulkan", ExplicitBackend: true, Model: "0.8b"})
	cfg := readConfigMap(t, root)
	if cfg["server_file"] != "runtime/vulkan/llama-server" || cfg["gpu_layers"] != float64(99) {
		t.Errorf("explicit backend not written: %v", cfg)
	}
	if cfg["model_file"] != "models/fixture-0.8b.gguf" || cfg["template_file"] != "models/chat_template.0.8b.jinja" {
		t.Errorf("explicit model not written: %v", cfg)
	}
	// A later plain run keeps both choices and downloads nothing.
	calls := tr.callCount()
	runOK(t, root, tr, Options{})
	cfg = readConfigMap(t, root)
	if cfg["model_file"] != "models/fixture-0.8b.gguf" {
		t.Errorf("model choice lost on re-run: %v", cfg)
	}
	if cfg["server_file"] != "runtime/vulkan/llama-server" || cfg["gpu_layers"] != float64(99) {
		t.Errorf("engine choice lost on re-run: %v", cfg)
	}
	if got := tr.callCount(); got != calls {
		t.Errorf("re-run contacted the network (%d extra calls)", got-calls)
	}
}

func TestRunConfiguredModelIsFetchedAndHashChecked(t *testing.T) {
	defer fixtureCatalog(t)()
	archive := fixtureTar(t, false)
	defer fixturePins(t, archive)()
	tr := fixtureTransport(archive)
	// Serve wrong bytes for the 8b model: setup must refuse them.
	orig := tr.handler
	tr.handler = func(url string) ([]byte, int) {
		if strings.Contains(url, "fixture-8b-repo") && strings.HasSuffix(url, ".gguf") {
			return []byte("not the model"), 200
		}
		return orig(url)
	}
	root := t.TempDir()
	// A Windows-style path in config.json still names the model.
	writeConfigMap(t, root, map[string]any{"model_file": `models\fixture-8b.gguf`})

	opt := Options{Root: root, Transport: tr, Out: io.Discard}
	if code := Run(opt); code == 0 {
		t.Fatal("setup accepted a model with a bad SHA-256")
	}
	if _, err := os.Stat(filepath.Join(root, "models", "fixture-8b.gguf")); !os.IsNotExist(err) {
		t.Error("the bad download was left in place")
	}
	if partials, _ := filepath.Glob(filepath.Join(root, "models", "*.partial")); len(partials) > 0 {
		t.Errorf("partial files left behind: %v", partials)
	}
	found := false
	for _, call := range tr.calls {
		if strings.Contains(call, "fixture-8b-repo/resolve/rev8b/") {
			found = true
		}
	}
	if !found {
		t.Errorf("the configured model was not fetched from its pinned revision: %v", tr.calls)
	}
}

func TestRunUnsafeArchiveIsRejected(t *testing.T) {
	defer fixtureCatalog(t)()
	archive := fixtureTar(t, true)
	defer fixturePins(t, archive)()
	tr := fixtureTransport(archive)
	root := t.TempDir()

	opt := Options{Root: root, Transport: tr, Out: io.Discard}
	if code := Run(opt); code == 0 {
		t.Fatal("setup accepted an archive with a ../ member")
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "cpu")); !os.IsNotExist(err) {
		t.Error("the unsafe archive left a runtime behind")
	}
	var escaped []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if d != nil && d.Name() == "escaped" {
			escaped = append(escaped, path)
		}
		return nil
	})
	if len(escaped) > 0 {
		t.Errorf("archive escaped into %v", escaped)
	}
}

func TestRunChecksumMismatchIsRejected(t *testing.T) {
	defer fixtureCatalog(t)()
	archive := fixtureTar(t, false)
	defer fixturePins(t, archive)()
	tr := fixtureTransport(archive)
	orig := tr.handler
	tr.handler = func(url string) ([]byte, int) {
		if strings.Contains(url, "github.com/ggml-org/llama.cpp") {
			return []byte("corrupted bytes"), 200
		}
		return orig(url)
	}
	root := t.TempDir()
	opt := Options{Root: root, Transport: tr, Out: io.Discard}
	if code := Run(opt); code == 0 {
		t.Fatal("setup accepted a runtime archive with a bad SHA-256")
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "cpu")); !os.IsNotExist(err) {
		t.Error("the corrupted archive left a runtime behind")
	}
}

func TestRunPartialRuntimeIsNotOverwritten(t *testing.T) {
	defer fixtureCatalog(t)()
	archive := fixtureTar(t, false)
	defer fixturePins(t, archive)()
	tr := fixtureTransport(archive)
	root := t.TempDir()
	partial := filepath.Join(root, "runtime", "cpu")
	if err := os.MkdirAll(partial, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(partial, "keep")
	if err := os.WriteFile(sentinel, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	opt := Options{Root: root, Transport: tr, Out: io.Discard}
	if code := Run(opt); code == 0 {
		t.Fatal("setup overwrote an incomplete runtime directory")
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "keep me" {
		t.Errorf("the incomplete runtime was touched: %q %v", b, err)
	}
}

func TestRunTooSmallModelIsRejected(t *testing.T) {
	defer fixtureCatalog(t)()
	archive := fixtureTar(t, false)
	defer fixturePins(t, archive)()
	tr := fixtureTransport(archive)
	root := t.TempDir()
	// Raise the 8b minimum so the fixture file counts as incomplete.
	models.Catalog[1].MinBytes = int64(len(fixtureModel8)) + 1
	modelPath := filepath.Join(root, "models", "fixture-8b.gguf")
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, fixtureModel8, 0o644); err != nil {
		t.Fatal(err)
	}
	opt := Options{Root: root, Transport: tr, Out: io.Discard, Model: "8b"}
	if code := Run(opt); code == 0 {
		t.Fatal("setup accepted a too-small model file")
	}
}
