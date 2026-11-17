package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSyntaxErrorPosition(t *testing.T) {
	for _, tc := range []struct {
		name, content, position string
	}{
		{"missing comma", "{\n  \"port\": 18119\n  \"poll_ms\": 50\n}", "line 3, column 3"},
		{"first character", "!", "line 1, column 1"},
		{"BOM and CRLF", "\xEF\xBB\xBF{\r\n  \"port\": 18119,\r\n}", "line 3, column 1"},
		{"Unicode column", "{\"note\": \"日本語\", !}", "line 1, column 17"},
		{"truncated", "{\n  \"port\": 18119", "line 2, column 15"},
		{"empty", "", "line 1, column 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeConfig(t, tc.content)
			_, err := Load(dir)
			var syntax *json.SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("expected wrapped syntax error, got %v", err)
			}
			for _, want := range []string{filepath.Join(dir, "config.json"), tc.position, syntax.Error()} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestLoadFileRequiresExistingBackup(t *testing.T) {
	dir := t.TempDir()
	if c, err := Load(dir); err != nil || c.MaxTokens != Default().MaxTokens {
		t.Fatalf("missing config must still use defaults: %+v, %v", c, err)
	}
	path := filepath.Join(dir, "config.json.bak")
	if _, err := LoadFile(path); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), path) {
		t.Fatalf("missing backup must be an error with its path: %v", err)
	}
}

func TestLoadRangeErrors(t *testing.T) {
	for _, tc := range []struct {
		key      string
		min, max int
	}{
		{"max_tokens", 1, 32768},
		{"context_tokens", 512, 32768},
		{"gpu_layers", 0, 10000},
		{"startup_timeout_seconds", 10, 900},
		{"translation_timeout_seconds", 10, 1800},
	} {
		for _, value := range []int{tc.min - 1, tc.min, tc.max, tc.max + 1} {
			t.Run(fmt.Sprintf("%s/%d", tc.key, value), func(t *testing.T) {
				dir := writeConfig(t, fmt.Sprintf(`{"%s": %d}`, tc.key, value))
				_, err := Load(dir)
				if value >= tc.min && value <= tc.max {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if err == nil {
					t.Fatal("out-of-range value accepted")
				}
				for _, want := range []string{filepath.Join(dir, "config.json"), tc.key, fmt.Sprintf("%d..%d", tc.min, tc.max)} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not contain %q", err, want)
					}
				}
			})
		}
	}
}

func TestLoadErrorsIncludeAbsolutePath(t *testing.T) {
	// Keep this on the working directory's volume; Windows Temp may be on C:.
	rel, err := os.MkdirTemp(".", "config-error-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Remove(filepath.Join(rel, "config.json"))
		os.Remove(rel)
	})
	dir, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"poll_ms": "fast"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(rel)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) || !strings.Contains(err.Error(), filepath.Join(dir, "config.json")) {
		t.Fatalf("type error must include absolute path and preserve cause: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "config.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = Load(rel)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "config.json")) {
		t.Fatalf("read error must include absolute path: %v", err)
	}
}
