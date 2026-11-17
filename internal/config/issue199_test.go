package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTranslationSettingsFreshLanguageSwitch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"hotkey":"Ctrl+Alt+E","languages":["ja","en"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Profile{
		{Hotkey: "Ctrl+Alt+E", Target: "de"},
		{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
	} {
		if err := UpdateTranslationSettings(dir, p, true, nil); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var saved map[string]json.RawMessage
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
		var langs []string
		if err := json.Unmarshal(saved["languages"], &langs); err != nil || len(langs) != 2 {
			t.Fatalf("invalid top-level languages: %s (%v)", saved["languages"], err)
		}
		c, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if c.Profiles[0].Target != p.Target || len(c.Profiles[0].Languages) != len(p.Languages) {
			t.Fatalf("profile = %+v", c.Profiles[0])
		}
	}
}

func TestTranslationSettingsPersistsOutputAndCleanup(t *testing.T) {
	for _, original := range []string{
		`{"hotkey":"Ctrl+Alt+E","languages":["ja","en"]}`,
		`{"profiles":[{"hotkey":"Ctrl+Alt+E","languages":["ja","en"],"output":"replace","cleanup_newlines":"off"}]}`,
		`{"profiles":[],"hotkey":"Ctrl+Alt+E","languages":["ja","en"]}`,
	} {
		t.Run(original, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			p := Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: "bilingual", CleanupNewlines: "auto"}
			if err := UpdateTranslationSettings(dir, p, true, nil); err != nil {
				t.Fatal(err)
			}
			c, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if c.Profiles[0].Output != p.Output || c.Profiles[0].CleanupNewlines != p.CleanupNewlines {
				t.Fatalf("saved profile = %+v", c.Profiles[0])
			}
		})
	}
}
