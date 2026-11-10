package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
)

func TestLoadConfigLanguages(t *testing.T) {
	c, err := config.Load(writeConfig(t, `{}`))
	if err != nil || !languages.SamePair(c.Languages, []string{"ja", "en"}) {
		t.Fatalf("default languages: %v, %v", c.Languages, err)
	}
	c, err = config.Load(writeConfig(t, `{"languages": ["EN", "ko"]}`))
	if err != nil || c.Languages[0] != "en" || c.Languages[1] != "ko" {
		t.Fatalf("explicit languages: %v, %v", c.Languages, err)
	}
	for _, bad := range []string{`{"languages": []}`, `{"languages": ["ja", "xx"]}`, `{"languages": ["en", "de"]}`} {
		if err := loadConfig2(t, bad); err == nil || !strings.Contains(err.Error(), "languages") {
			t.Errorf("%s must be rejected with a message about languages, got %v", bad, err)
		}
	}
}

func TestSettingsLanguageSwitchesAndSaves(t *testing.T) {
	a, _, _ := newTestApp(t)
	changeSettingsLanguages(t, a, []string{"en", "ko"})
	if !languages.SamePair(a.conf.Languages, []string{"en", "ko"}) {
		t.Fatalf("pair not switched: %v", a.conf.Languages)
	}
	if got, ok := languages.DirectionFor(a.conf.Languages, "안녕"); !ok || got.Source != "ko" || got.Target != "en" {
		t.Errorf("the new pair is not used: %+v, %v", got, ok)
	}
	b, err := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if err != nil || !strings.Contains(string(b), `"languages": ["en","ko"]`) || !strings.Contains(string(b), `"hotkey"`) {
		t.Errorf("config.json not updated while keeping other keys: %s %v", b, err)
	}
	if !languages.SamePair(a.settingsState().Profiles[0].Languages, []string{"en", "ko"}) {
		t.Fatal("Settings must show the saved pair")
	}
}
