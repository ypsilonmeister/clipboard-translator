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
}

func defaultConfig() Config {
	return Config{
		Hotkey: "Ctrl+Alt+E", Port: 18119, PollMS: 50, MaxChars: 4000, MaxTokens: 2048, ContextTokens: 4096,
		GPULayers: 0, TranslationTimeoutSec: 180, StartupTimeoutSec: 120,
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
	key                   byte // upper-case 'A'..'Z'
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
				return hotkeySpec{}, fmt.Errorf("unsupported hotkey %q: use Ctrl+Alt+E (one A-Z key)", s)
			}
			h.win = true
		default:
			if len(p) != 1 || p[0] < 'A' || p[0] > 'Z' || h.key != 0 {
				return hotkeySpec{}, fmt.Errorf("unsupported hotkey %q: use Ctrl+Alt+E (one A-Z key)", s)
			}
			h.key = p[0]
		}
	}
	if !(h.ctrl || h.alt || h.shift || h.win) || h.key == 0 {
		return hotkeySpec{}, fmt.Errorf("unsupported hotkey %q: it must include a modifier (Ctrl, Alt, Shift or Win) and one letter A-Z, like Ctrl+Alt+E", s)
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
	if c.Hotkey == "" {
		return c, errors.New("hotkey cannot be empty")
	}
	if err := validateHotkey(c.Hotkey); err != nil {
		return c, err
	}
	// main() also listens on port+1 to allow only one instance, so 65535 would overflow.
	if c.Port < 1024 || c.Port > 65534 {
		return c, errors.New("port must be 1024..65534 (port+1 is used to allow only one running copy)")
	}
	if c.PollMS < 20 || c.PollMS > 10000 {
		return c, errors.New("poll_ms must be 20..10000")
	}
	if c.MaxChars < 1 || c.MaxChars > 100000 {
		return c, errors.New("max_chars out of range")
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
