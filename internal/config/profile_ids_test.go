package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshConfigHasProfileID(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Profiles) != 1 || c.Profiles[0].ID != "profile_1" {
		t.Fatalf("default profiles = %+v", c.Profiles)
	}
}

func TestLegacyConfigMigratesProfileIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	legacy := `{
  "profiles": [
    {"hotkey": "Ctrl+E", "target": "en", "future_option": true},
    {"hotkey": "Ctrl+J", "target": "ja"}
  ],
  "future_setting": 42
}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Profiles[0].ID != "profile_1" || c.Profiles[1].ID != "profile_2" {
		t.Fatalf("legacy profiles = %+v", c.Profiles)
	}
	if err := c.PersistProfileIDs(dir); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"future_option": true`, `"future_setting": 42`} {
		if !strings.Contains(string(saved), want) {
			t.Errorf("migrated config is missing %s:\n%s", want, saved)
		}
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.profileIDsNeedMigration || reloaded.Profiles[0].ID != c.Profiles[0].ID || reloaded.Profiles[1].ID != c.Profiles[1].ID {
		t.Fatalf("reloaded profiles = %+v", reloaded.Profiles)
	}
}
