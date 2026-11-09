package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	// Languages is the pair translated in both directions, e.g. ["ja", "en"].
	Languages []string `json:"languages"`
	// Profiles are the hotkeys, each with its own language setting. When it is
	// absent, one profile is made from Hotkey and Languages. Profiles[0] is always
	// mirrored in Hotkey and (for a pair profile) Languages, which the tray menu edits.
	Profiles []Profile `json:"profiles"`

	explicitProfiles bool // config.json has a "profiles" key; the tray saves that instead of hotkey/languages
}

const (
	OutputReplace   = "replace"
	OutputBilingual = "bilingual"
)

// Profile is one hotkey and what it translates: either a Languages pair (both
// directions) or a Target (always into that language, the model detects the source),
// and an Output mode ("replace" or "bilingual").
type Profile struct {
	Hotkey    string   `json:"hotkey"`
	Languages []string `json:"languages,omitempty"`
	Target    string   `json:"target,omitempty"`
	Output    string   `json:"output,omitempty"`
}

func (p Profile) outputMode() string {
	if p.Output == OutputBilingual {
		return OutputBilingual
	}
	return OutputReplace
}

// normalizeProfiles makes Profiles non-empty and mirrors profile 0 into
// Hotkey/Languages. It does not validate.
func (c *Config) normalizeProfiles() {
	if len(c.Profiles) == 0 {
		c.Profiles = []Profile{{Hotkey: c.Hotkey, Languages: append([]string(nil), c.Languages...), Output: OutputReplace}}
		return
	}
	for i := range c.Profiles {
		if c.Profiles[i].Output == "" {
			c.Profiles[i].Output = OutputReplace
		}
	}
	c.Hotkey = c.Profiles[0].Hotkey
	if c.Profiles[0].Languages != nil {
		c.Languages = append([]string(nil), c.Profiles[0].Languages...)
	}
}

// resolveProfiles validates the hotkey/languages pair, or the profiles when the
// file has them, and fills Profiles.
func (c *Config) resolveProfiles(explicit bool) error {
	if !explicit {
		if c.Hotkey == "" {
			return errors.New("hotkey cannot be empty")
		}
		if err := validateHotkey(c.Hotkey); err != nil {
			return err
		}
		langs, err := validateLanguages(c.Languages)
		if err != nil {
			return err
		}
		c.Languages = langs
		c.Profiles = nil
		c.normalizeProfiles()
		return nil
	}
	if len(c.Profiles) == 0 {
		return errors.New("profiles must not be empty; remove the key to use hotkey and languages")
	}
	seen := map[hotkeySpec]int{}
	for i := range c.Profiles {
		p := &c.Profiles[i]
		where := fmt.Sprintf("profiles[%d]", i)
		spec, err := parseHotkeyNames(p.Hotkey, runtime.GOOS != "windows", runtime.GOOS == "darwin")
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
			if _, ok := languages[t]; !ok {
				return fmt.Errorf("%s: target %q is not a supported language code; supported: %s", where, p.Target, strings.Join(languageCodes(), ", "))
			}
			p.Target = t
		default:
			if p.Languages == nil {
				return fmt.Errorf("%s: set \"languages\" (a pair) or \"target\"", where)
			}
			langs, err := validateLanguages(p.Languages)
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
	}
	c.explicitProfiles = true
	c.normalizeProfiles()
	return nil
}

// otherProfileWithHotkey reports the index of a profile other than the first
// that already uses spec. Keys are compared as parsed combinations, as
// resolveProfiles does, so "alt+control+e" and "Ctrl+Alt+E" are the same key.
// An unparsable spec matches nothing: the caller's own validation reports it.
func (c *Config) otherProfileWithHotkey(spec string) (int, bool) {
	want, err := parseHotkeyNames(spec, runtime.GOOS != "windows", runtime.GOOS == "darwin")
	if err != nil {
		return 0, false
	}
	for i := 1; i < len(c.Profiles); i++ {
		got, err := parseHotkeyNames(c.Profiles[i].Hotkey, runtime.GOOS != "windows", runtime.GOOS == "darwin")
		if err == nil && got == want {
			return i, true
		}
	}
	return 0, false
}

func defaultConfig() Config {
	return Config{
		Hotkey: "Ctrl+Alt+E", Port: 18119, PollMS: 50, MaxChars: 4000, MaxTokens: 2048, ContextTokens: 4096,
		GPULayers: 0, TranslationTimeoutSec: 180, StartupTimeoutSec: 120,
		Languages:          append([]string(nil), defaultLanguages...),
		ModelFile:          "models/plamo-3-translate-2b-Q4_K_M.gguf",
		TemplateFile:       "models/chat_template.jinja",
		ServerFile:         "runtime/cpu/" + serverBinaryName(),
		FallbackServerFile: "runtime/cpu/" + serverBinaryName(),
	}
}

// utf8BOM is written at the start of a file by Windows PowerShell 5.1
// (-Encoding UTF8), some editors and older Notepad. encoding/json rejects it.
var utf8BOM = []byte("\xEF\xBB\xBF")

// hotkeySpec is a parsed hotkey such as "Ctrl+Alt+E".
type hotkeySpec struct {
	ctrl, alt, shift, win bool
	key                   string // upper-case "A".."Z", "0".."9", or "F1".."F12"
}

// parseFunctionKey parses "F1" through "F12" (case-insensitive) and returns
// the 1-based number (1..12), or false if s is not a function key.
func parseFunctionKey(s string) (int, bool) {
	if len(s) < 2 || len(s) > 3 || (s[0] != 'F' && s[0] != 'f') {
		return 0, false
	}
	switch strings.ToUpper(s) {
	case "F1":
		return 1, true
	case "F2":
		return 2, true
	case "F3":
		return 3, true
	case "F4":
		return 4, true
	case "F5":
		return 5, true
	case "F6":
		return 6, true
	case "F7":
		return 7, true
	case "F8":
		return 8, true
	case "F9":
		return 9, true
	case "F10":
		return 10, true
	case "F11":
		return 11, true
	case "F12":
		return 12, true
	}
	return 0, false
}

// parseHotkeySpec is the one place that knows the hotkey syntax, so loadConfig
// can reject a bad config.json before the model is loaded and the per-platform
// parsers (RegisterHotKey, X11) cannot drift from it. allowSuper accepts
// "Super" as another name for the Windows key (X11 and macOS). allowMac accepts
// the macOS names Cmd/Command (the same key as Win here) and Option (Alt).
func parseHotkeySpec(s string, allowSuper bool) (hotkeySpec, error) {
	return parseHotkeyNames(s, allowSuper, false)
}

func parseHotkeyNames(s string, allowSuper, allowMac bool) (hotkeySpec, error) {
	var h hotkeySpec
	for _, p := range strings.Split(strings.ToUpper(strings.ReplaceAll(s, " ", "")), "+") {
		switch p {
		case "CTRL", "CONTROL":
			h.ctrl = true
		case "ALT":
			h.alt = true
		case "SHIFT":
			h.shift = true
		case "WIN", "WINDOWS":
			h.win = true
		case "CMD", "COMMAND":
			if !allowMac {
				return hotkeySpec{}, fmt.Errorf("unsupported hotkey %q: %s is a macOS name; use Ctrl+Alt+E", s, p)
			}
			h.win = true
		case "OPTION":
			if !allowMac {
				return hotkeySpec{}, fmt.Errorf("unsupported hotkey %q: Option is a macOS name; use Alt", s)
			}
			h.alt = true
		case "SUPER":
			if !allowSuper {
				return hotkeySpec{}, fmt.Errorf("unsupported hotkey %q: use Ctrl+Alt+E (letters A-Z, digits 0-9, or F1-F12)", s)
			}
			h.win = true
		default:
			if h.key != "" {
				return hotkeySpec{}, fmt.Errorf("unsupported hotkey %q: use Ctrl+Alt+E (letters A-Z, digits 0-9, or F1-F12)", s)
			}
			if len(p) == 1 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= '0' && p[0] <= '9')) {
				h.key = p
			} else if _, ok := parseFunctionKey(p); ok {
				h.key = p
			} else {
				return hotkeySpec{}, fmt.Errorf("unsupported hotkey %q: use Ctrl+Alt+E (letters A-Z, digits 0-9, or F1-F12)", s)
			}
		}
	}
	if !(h.ctrl || h.alt || h.shift || h.win) || h.key == "" {
		return hotkeySpec{}, fmt.Errorf("unsupported hotkey %q: it must include a modifier (Ctrl, Alt, Shift or Win) and one key (A-Z, 0-9, or F1-F12), like Ctrl+Alt+E", s)
	}
	return h, nil
}

// validateHotkey checks a hotkey for the platform this build runs on.
func validateHotkey(s string) error { return validateHotkeyFor(runtime.GOOS, s) }

// validateHotkeyFor is validateHotkey for another OS, so tests can cover all
// three parsers (RegisterHotKey, X11, Carbon) from any machine.
func validateHotkeyFor(goos, s string) error {
	_, err := parseHotkeyNames(s, goos != "windows", goos == "darwin")
	return err
}

// Shared validation keeps config.json and the Settings page consistent.
func validateIdleUnloadMinutes(n int) error {
	if n < 0 || n > 1440 {
		return errors.New("idle_unload_minutes must be a whole number from 0 to 1440 (0 disables automatic unloading)")
	}
	return nil
}

// validateMaxChars is the one check of max_chars, for config.json and the settings page.
func validateMaxChars(n int) error {
	if n < 1 || n > 100000 {
		return errors.New("max_chars out of range (1..100000)")
	}
	return nil
}

func loadConfig(dir string) (Config, error) {
	c := defaultConfig()
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
	if err := c.resolveProfiles(c.Profiles != nil); err != nil {
		return c, err
	}
	// main() also listens on port+1 to allow only one instance, so 65535 would overflow.
	if c.Port < 1024 || c.Port > 65534 {
		return c, errors.New("port must be 1024..65534 (port+1 is used to allow only one running copy)")
	}
	if c.PollMS < 20 || c.PollMS > 10000 {
		return c, errors.New("poll_ms must be 20..10000")
	}
	if err := validateIdleUnloadMinutes(c.IdleUnloadMinutes); err != nil {
		return c, err
	}
	if err := validateMaxChars(c.MaxChars); err != nil {
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
