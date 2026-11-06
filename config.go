package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
}

func defaultConfig() Config {
	return Config{
		Hotkey: "Ctrl+Alt+E", Port: 18119, PollMS: 50, MaxChars: 4000, MaxTokens: 2048, ContextTokens: 4096,
		GPULayers: 0, TranslationTimeoutSec: 180, StartupTimeoutSec: 120,
		ModelFile:          "models/plamo-3-translate-2b-Q4_K_M.gguf",
		TemplateFile:       "models/chat_template.jinja",
		ServerFile:         "runtime/cpu/llama-server.exe",
		FallbackServerFile: "runtime/cpu/llama-server.exe",
	}
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
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Hotkey == "" {
		return c, errors.New("hotkey cannot be empty")
	}
	if c.Port < 1024 || c.Port > 65535 {
		return c, errors.New("port must be 1024..65535")
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
