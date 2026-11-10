package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ypsilonmeister/clipboard-translator/internal/hotkeyspec"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

type Config struct {
	Hotkey                string `json:"hotkey"`
	Port                  int    `json:"port"`
	PollMS                int    `json:"poll_ms"`
	IdleUnloadMinutes     int    `json:"idle_unload_minutes"`
	MaxChars              int    `json:"max_chars"`
	MaxTokens             int    `json:"max_tokens"`
	ContextTokens         int    `json:"context_tokens"`
	GPULayers             int    `json:"gpu_layers"`
	TranslationTimeoutSec int    `json:"translation_timeout_seconds"`
	StartupTimeoutSec     int    `json:"startup_timeout_seconds"`
	ModelFile             string `json:"model_file"`
	TemplateFile          string `json:"template_file"`
	ServerFile            string `json:"server_file"`
	FallbackServerFile    string `json:"fallback_server_file"`
	// AutoPaste types Ctrl+V into the window the text was selected in once the
	// translation is ready. Off by default.
	AutoPaste bool `json:"auto_paste"`
	// NotifyOnComplete shows a tray notification when a translation finishes
	// without auto-pasting. Off by default.
	NotifyOnComplete bool `json:"notify_on_complete"`
	// QuickstartShown is set once the first-run guide has been shown, so later
	// launches stay quiet. The guide stays available in the tray and Settings.
	QuickstartShown bool `json:"quickstart_shown"`
	// Languages is the pair translated in both directions, e.g. ["ja", "en"].
	Languages []string `json:"languages"`
	// Profiles are the hotkeys, each with its own language setting. When it is
	// absent, one profile is made from Hotkey and Languages. Profiles[0] is always
	// mirrored in Hotkey and (for a pair profile) Languages for legacy config files.
	Profiles []Profile `json:"profiles"`

	ExplicitProfiles bool `json:"-"` // config.json has a "profiles" key rather than only legacy hotkey/languages
}

const (
	OutputReplace   = "replace"
	OutputBilingual = "bilingual"
)

// Where a profile takes its text from: the selection (Ctrl+C is sent first) or
// the clipboard as it is (nothing is copied, nothing is waited for).
const (
	InputSelection = "selection"
	InputClipboard = "clipboard"
)

// Whether spurious line breaks (e.g. from PDFs or terminals) should be cleaned up
// before translation. Off by default.
const (
	CleanupNewlinesOff  = "off"
	CleanupNewlinesAuto = "auto"
)

// Profile is one hotkey and what it translates: either a Languages pair (both
// directions) or a Target (always into that language, the model detects the source),
// an Output mode ("replace" or "bilingual") and an Input ("selection" or "clipboard").
type Profile struct {
	Hotkey          string   `json:"hotkey"`
	Languages       []string `json:"languages,omitempty"`
	Target          string   `json:"target,omitempty"`
	Output          string   `json:"output,omitempty"`
	Input           string   `json:"input,omitempty"`
	CleanupNewlines string   `json:"cleanup_newlines,omitempty"`
}

func (p Profile) InputMode() string {
	if p.Input == InputClipboard {
		return InputClipboard
	}
	return InputSelection
}

func (p Profile) OutputMode() string {
	if p.Output == OutputBilingual {
		return OutputBilingual
	}
	return OutputReplace
}

func (p Profile) CleanupNewlinesMode() string {
	if p.CleanupNewlines == CleanupNewlinesAuto {
		return CleanupNewlinesAuto
	}
	return CleanupNewlinesOff
}

// normalizeProfiles makes Profiles non-empty and mirrors profile 0 into
// Hotkey/Languages. It does not validate.
func (c *Config) NormalizeProfiles() {
	if len(c.Profiles) == 0 {
		c.Profiles = []Profile{{Hotkey: c.Hotkey, Languages: append([]string(nil), c.Languages...), Output: OutputReplace, Input: InputSelection, CleanupNewlines: CleanupNewlinesOff}}
		return
	}
	for i := range c.Profiles {
		if c.Profiles[i].Output == "" {
			c.Profiles[i].Output = OutputReplace
		}
		if c.Profiles[i].Input == "" {
			c.Profiles[i].Input = InputSelection
		}
		if c.Profiles[i].CleanupNewlines == "" {
			c.Profiles[i].CleanupNewlines = CleanupNewlinesOff
		}
	}
	c.Hotkey = c.Profiles[0].Hotkey
	if c.Profiles[0].Languages != nil {
		c.Languages = append([]string(nil), c.Profiles[0].Languages...)
	}
}

// resolveProfiles validates the hotkey/languages pair, or the profiles when the
// file has them, and fills Profiles.
func (c *Config) ResolveProfiles(explicit bool) error {
	if !explicit {
		if c.Hotkey == "" {
			return errors.New("hotkey cannot be empty")
		}
		if err := validateHotkey(c.Hotkey); err != nil {
			return err
		}
		langs, err := languages.Validate(c.Languages)
		if err != nil {
			return err
		}
		c.Languages = langs
		c.Profiles = nil
		c.NormalizeProfiles()
		return nil
	}
	if len(c.Profiles) == 0 {
		return errors.New("profiles must not be empty; remove the key to use hotkey and languages")
	}
	seen := map[hotkeyspec.Spec]int{}
	for i := range c.Profiles {
		p := &c.Profiles[i]
		where := fmt.Sprintf("profiles[%d]", i)
		spec, err := hotkeyspec.ParseNames(p.Hotkey, runtime.GOOS != "windows", runtime.GOOS == "darwin")
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if j, dup := seen[spec]; dup {
			return fmt.Errorf("%s: hotkey %q is the same key combination as profiles[%d] (%q)", where, p.Hotkey, j, c.Profiles[j].Hotkey)
		}
		seen[spec] = i
		switch {
		case p.Languages != nil && p.Target != "":
			return fmt.Errorf("%s: use either \"languages\" or \"target\", not both", where)
		case p.Target != "":
			t := strings.ToLower(strings.TrimSpace(p.Target))
			if !languages.Known(t) {
				return fmt.Errorf("%s: target %q is not a supported language code; supported: %s", where, p.Target, strings.Join(languages.Codes(), ", "))
			}
			p.Target = t
		default:
			if p.Languages == nil {
				return fmt.Errorf("%s: set \"languages\" (a pair) or \"target\"", where)
			}
			langs, err := languages.Validate(p.Languages)
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			p.Languages = langs
		}
		out := strings.ToLower(strings.TrimSpace(p.Output))
		switch out {
		case "", OutputReplace:
			p.Output = OutputReplace
		case OutputBilingual:
			p.Output = OutputBilingual
		default:
			return fmt.Errorf("%s: output %q is not supported; valid values: %q, %q", where, p.Output, OutputReplace, OutputBilingual)
		}
		switch strings.ToLower(strings.TrimSpace(p.Input)) {
		case "", InputSelection:
			p.Input = InputSelection
		case InputClipboard:
			p.Input = InputClipboard
		default:
			return fmt.Errorf("%s: input %q is not supported; valid values: %q, %q", where, p.Input, InputSelection, InputClipboard)
		}
		switch strings.ToLower(strings.TrimSpace(p.CleanupNewlines)) {
		case "", CleanupNewlinesOff:
			p.CleanupNewlines = CleanupNewlinesOff
		case CleanupNewlinesAuto:
			p.CleanupNewlines = CleanupNewlinesAuto
		default:
			return fmt.Errorf("%s: cleanup_newlines %q is not supported; valid values: %q, %q", where, p.CleanupNewlines, CleanupNewlinesOff, CleanupNewlinesAuto)
		}
	}
	c.ExplicitProfiles = true
	c.NormalizeProfiles()
	return nil
}

func Default() Config {
	return Config{
		Hotkey: "Ctrl+Alt+E", Port: 18119, PollMS: 50, MaxChars: 4000, MaxTokens: 2048, ContextTokens: 4096,
		GPULayers: 0, TranslationTimeoutSec: 180, StartupTimeoutSec: 120,
		Languages:          append([]string(nil), languages.Default...),
		ModelFile:          "models/plamo-3-translate-2b-Q4_K_M.gguf",
		TemplateFile:       "models/chat_template.2b.jinja",
		ServerFile:         "runtime/cpu/" + platform.ServerBinaryName(),
		FallbackServerFile: "runtime/cpu/" + platform.ServerBinaryName(),
	}
}

// utf8BOM is written at the start of a file by Windows PowerShell 5.1
// (-Encoding UTF8), some editors and older Notepad. encoding/json rejects it.
var utf8BOM = []byte("\xEF\xBB\xBF")

// validateHotkey checks a hotkey for the platform this build runs on.
func validateHotkey(s string) error { return hotkeyspec.Validate(runtime.GOOS, s) }

// Shared validation keeps config.json and the Settings page consistent.
func ValidateIdleUnloadMinutes(n int) error {
	if n < 0 || n > 1440 {
		return errors.New("idle_unload_minutes must be a whole number from 0 to 1440 (0 disables automatic unloading)")
	}
	return nil
}

// ValidateMaxChars is the one check of max_chars, for config.json and the settings page.
func ValidateMaxChars(n int) error {
	if n < 1 || n > 100000 {
		return errors.New("max_chars out of range (1..100000)")
	}
	return nil
}

func Load(dir string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(bytes.TrimPrefix(b, utf8BOM), &c); err != nil {
		return c, err
	}
	if err := c.ResolveProfiles(c.Profiles != nil); err != nil {
		return c, err
	}
	// main() also listens on port+1 to allow only one instance, so 65535 would overflow.
	if c.Port < 1024 || c.Port > 65534 {
		return c, errors.New("port must be 1024..65534 (port+1 is used to allow only one running copy)")
	}
	if c.PollMS < 20 || c.PollMS > 10000 {
		return c, errors.New("poll_ms must be 20..10000")
	}
	if err := ValidateIdleUnloadMinutes(c.IdleUnloadMinutes); err != nil {
		return c, err
	}
	if err := ValidateMaxChars(c.MaxChars); err != nil {
		return c, err
	}
	if c.MaxTokens < 1 || c.MaxTokens > 32768 {
		return c, errors.New("max_tokens out of range")
	}
	if c.ContextTokens < 512 || c.ContextTokens > 32768 {
		return c, errors.New("context_tokens out of range")
	}
	if c.StartupTimeoutSec < 10 || c.StartupTimeoutSec > 900 {
		return c, errors.New("startup_timeout_seconds out of range")
	}
	if c.TranslationTimeoutSec < 10 || c.TranslationTimeoutSec > 1800 {
		return c, errors.New("translation_timeout_seconds out of range")
	}
	if c.GPULayers < 0 || c.GPULayers > 10000 {
		return c, errors.New("gpu_layers out of range")
	}
	return c, nil
}
