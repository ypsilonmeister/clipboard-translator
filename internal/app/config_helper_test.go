package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
)

// writeConfig makes a folder holding a config.json with content.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// loadConfig2 loads content as a config.json and returns only the error.
func loadConfig2(t *testing.T, content string) error {
	t.Helper()
	_, err := config.Load(writeConfig(t, content))
	return err
}
