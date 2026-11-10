// Package setup is the first-run installer that used to live in setup.ps1,
// setup.sh and setup-macos.sh: it downloads the PLaMo model and the pinned
// llama.cpp runtime into the app folder and points config.json at them.
// Issue #152 moved it into the binary so macOS and Linux no longer need
// Python (or PowerShell) for it; the scripts are now thin wrappers around
// "clipboard-translator setup".
//
// Nothing here runs unless the user asks: the GUI offers it, Setup.cmd /
// the setup scripts invoke it, and normal translation stays offline.
package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/models"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// Options controls one setup run. Backend "" means the platform default
// (auto-detect on Windows, cpu on Linux, metal on macOS) and is not an
// explicit choice; the wrappers pass ExplicitBackend only when the user
// named a backend, because only an explicit choice may rewrite the engine
// settings of an existing install on a re-run.
type Options struct {
	Root            string
	Backend         string
	ExplicitBackend bool
	Model           string // "" keeps the model config.json names (2b on a fresh install)
	Out             io.Writer

	// Transport replaces the HTTP transport; tests set one that only
	// reaches a local test server, so no test can contact the real hosts.
	Transport http.RoundTripper
}

type runner struct {
	opt    Options
	out    io.Writer
	client *http.Client
	root   string
	entry  models.Entry // the model being installed, for the final messages
}

// Run performs the whole setup in opt.Root and returns the process exit
// code: 0 on success, 1 on failure (the error is printed to stderr by the
// caller-visible log line here), 2 on bad options.
func Run(opt Options) int {
	r := &runner{opt: opt, out: opt.Out, root: opt.Root}
	if r.out == nil {
		r.out = os.Stdout
	}
	r.client = &http.Client{Transport: opt.Transport}
	if err := r.run(); err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		return 1
	}
	return 0
}

func (r *runner) printf(format string, args ...any) {
	fmt.Fprintf(r.out, format+"\n", args...)
}

func (r *runner) run() error {
	if r.root == "" {
		return errors.New("no app folder given")
	}
	plat, err := currentPlatform()
	if err != nil {
		return err
	}
	if r.opt.Backend != "" && !plat.validBackend(r.opt.Backend) {
		return fmt.Errorf("unsupported backend %q on this platform (want one of %s)", r.opt.Backend, strings.Join(plat.backends, ", "))
	}
	if r.opt.Model != "" {
		if _, ok := models.ByKey(r.opt.Model); !ok {
			return fmt.Errorf("unsupported model %q (want 0.8b, 2b or 8b)", r.opt.Model)
		}
	}

	modelsDir := filepath.Join(r.root, "models")
	runtimeDir := filepath.Join(r.root, "runtime")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return err
	}

	// Read config.json before the big downloads: a damaged file fails now.
	cfg, cfgExists, err := readConfig(r.root)
	if err != nil {
		return err
	}
	serverPresentAtStart := configuredServer(r.root, cfg, "server_file")
	completeAtStart := map[string]bool{}
	for _, b := range plat.backends {
		completeAtStart[b] = plat.backendComplete(r.root, b)
	}

	entry, explicitModel, err := resolveModel(cfg, r.opt.Model)
	if err != nil {
		return err
	}
	r.entry = entry
	if plat.modelBannerSize {
		r.printf("Model: PLaMo 3 Translate %s (%s)", strings.ToUpper(entry.Key), entry.SizeLabel)
	} else {
		r.printf("Model: PLaMo 3 Translate %s", strings.ToUpper(entry.Key))
	}
	if err := r.installModel(entry); err != nil {
		return err
	}
	if err := r.installTemplate(entry); err != nil {
		return err
	}

	backend, explicit := r.opt.Backend, r.opt.ExplicitBackend
	if backend == "" {
		backend, explicit = plat.defaultBackend, false
	}
	if plat.autoDetect && (backend == "auto" || backend == "") {
		backend, explicit = plat.detectBackend(r), false
	}
	installedBackend, err := plat.installRuntimes(r, backend)
	if err != nil {
		return err
	}

	set := engineUpdates(engineState{
		root: r.root, cfg: cfg, backend: installedBackend,
		explicitBackend:        explicit,
		serverPresentAtStart:   serverPresentAtStart,
		backendCompleteAtStart: completeAtStart[installedBackend],
	})
	if explicitModel {
		set["model_file"] = "models/" + entry.File
		cur, _ := cfg["template_file"].(string)
		if !models.IsCustomTemplate(cur) {
			set["template_file"] = "models/" + entry.TemplateFile()
		}
	}
	// Drop values that are already in the file: a plain re-run of an
	// installed setup writes nothing (and makes no .bak), like setup.ps1.
	for k, v := range set {
		if cur, ok := cfg[k]; ok && jsonValueEqual(cur, v) {
			delete(set, k)
		}
	}
	wrote := false
	if len(set) > 0 || !cfgExists {
		if err := config.UpdateFile(r.root, set); err != nil {
			return err
		}
		wrote = true
	}
	if !wrote && cfgExists {
		plat.keptEngineMessage(r)
	}
	finalServer, _ := cfg["server_file"].(string)
	if v, ok := set["server_file"].(string); ok {
		finalServer = v
	}
	plat.finish(r, installedBackend, finalServer)
	return nil
}

// readConfig parses config.json into a plain map: setup only reads a few
// keys and must not reject a file over values it does not touch (the
// scripts only JSON-parse it too). A file that is not a JSON object fails
// before anything is downloaded.
func readConfig(root string) (map[string]any, bool, error) {
	b, err := os.ReadFile(filepath.Join(root, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf}), &cfg); err != nil {
		return nil, false, fmt.Errorf("config.json is not valid JSON: %w", err)
	}
	return cfg, true, nil
}

func jsonValueEqual(cur, want any) bool {
	switch w := want.(type) {
	case string:
		c, ok := cur.(string)
		return ok && c == w
	case int:
		c, ok := cur.(float64)
		return ok && c == float64(w)
	}
	return false
}

// configuredServer reports whether the config value names an existing
// regular file inside root, using the scripts' rules: no absolute paths,
// no Windows drive, no ".." element, either slash.
func configuredServer(root string, cfg map[string]any, key string) bool {
	v, ok := cfg[key].(string)
	if !ok || strings.TrimSpace(v) == "" {
		return false
	}
	s := strings.ReplaceAll(v, `\`, "/")
	if strings.HasPrefix(s, "/") || (len(s) >= 2 && s[1] == ':') {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." {
			return false
		}
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(s)))
	return err == nil && info.Mode().IsRegular()
}

// resolveModel picks the model to install: the explicit --model, else the
// one the config already names (so an update never swaps a chosen model),
// else the 2b default.
func resolveModel(cfg map[string]any, requested string) (models.Entry, bool, error) {
	if requested != "" {
		e, ok := models.ByKey(requested)
		if !ok {
			return models.Entry{}, false, fmt.Errorf("unsupported model %q", requested)
		}
		return e, true, nil
	}
	if cur, ok := cfg["model_file"].(string); ok {
		if e, ok := models.ForFile(cur); ok {
			return e, false, nil
		}
	}
	e, _ := models.ByKey("2b")
	return e, false, nil
}

func modelBaseURL(e models.Entry) string {
	return "https://huggingface.co/pfnet/" + e.Repo + "/resolve/" + e.Revision + "/"
}

func (r *runner) installModel(e models.Entry) error {
	dest := e.Path(r.root)
	if _, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
		r.printf("Downloading %s", e.File)
		if err := r.download(modelBaseURL(e)+e.File, dest, e.Sha256); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return err
	}
	if info.Size() < e.MinBytes {
		return fmt.Errorf("incomplete file; remove it and retry: %s", dest)
	}
	return nil
}

func (r *runner) installTemplate(e models.Entry) error {
	dest := e.TemplatePath(r.root)
	legacy := filepath.Join(r.root, "models", "chat_template.jinja")
	need := true
	if models.VerifyFileSha256(dest, e.TemplateSha256) {
		need = false
	} else if models.VerifyFileSha256(legacy, e.TemplateSha256) {
		if err := copyFile(legacy, dest); err != nil {
			return err
		}
		need = false
	}
	if need {
		r.printf("Downloading chat_template.jinja")
		if err := r.download(modelBaseURL(e)+"chat_template.jinja", dest, e.TemplateSha256); err != nil {
			return err
		}
	}
	info, err := os.Stat(dest)
	if err != nil {
		return err
	}
	if info.Size() < 100 {
		return fmt.Errorf("incomplete file; remove it and retry: %s", dest)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fileSha256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// engineState carries everything the config decision needs, captured the
// way the scripts capture it (the "at start" values before any install).
type engineState struct {
	root                   string
	cfg                    map[string]any
	backend                string
	explicitBackend        bool
	serverPresentAtStart   bool
	backendCompleteAtStart bool
}

// engineUpdates is Update-BackendConfig from setup.ps1 (the Unix scripts
// mirror it): the engine a user already runs is left alone on re-runs
// unless the backend was explicit, the configured server was missing at
// the start or is missing now, or the backend was incomplete at the start
// and has just been installed.
func engineUpdates(st engineState) map[string]any {
	set := map[string]any{}
	serverFile, gpuLayers := config.BackendSettings(st.backend)
	fallbackFile, _ := config.BackendSettings("cpu")
	write := st.explicitBackend || !st.serverPresentAtStart ||
		!configuredServer(st.root, st.cfg, "server_file") || !st.backendCompleteAtStart
	if write {
		set["server_file"] = serverFile
		set["fallback_server_file"] = fallbackFile
		set["gpu_layers"] = gpuLayers
		return set
	}
	if !configuredServer(st.root, st.cfg, "fallback_server_file") {
		set["fallback_server_file"] = fallbackFile
	}
	if v, ok := st.cfg["gpu_layers"]; !ok || v == nil {
		cur, _ := st.cfg["server_file"].(string)
		if isCPUServerFile(cur) {
			set["gpu_layers"] = 0
		} else {
			set["gpu_layers"] = 99
		}
	}
	return set
}

var cpuServerPrefix = regexp.MustCompile(`(?i)^runtime[\\/]cpu[\\/]`)

func isCPUServerFile(serverFile string) bool {
	return cpuServerPrefix.MatchString(serverFile)
}

// serverFilePath is the runtime path of backend b as the config holds it.
func serverFilePath(root, backend string) string {
	return filepath.Join(root, "runtime", backend, platform.ServerBinaryName())
}
