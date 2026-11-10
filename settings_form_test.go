package main

import (
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
)

func TestSettingsCustomHotkeysRoundTrip(t *testing.T) {
	for _, spec := range []string{"Ctrl+Alt+E", "ctrl+alt+e", "Ctrl+Alt+Q"} {
		f, err := formFromProfile(config.Profile{Hotkey: spec, Languages: []string{"ja", "en"}}, "windows")
		if err != nil || !strings.EqualFold(f.hotkeyString(), spec) {
			t.Fatalf("%s: %+v %v", spec, f, err)
		}
	}
}

// Settings validates the same duplicate hotkeys as config.json does, before
// touching registrations (the config side is internal/config's TestHotkeyDuplicateDetection).
func TestSettingsRejectsDuplicateHotkeys(t *testing.T) {
	cases := []struct {
		a, b string
	}{
		{"Ctrl+Alt+E", "alt+control+e"},
		{"ctrl+f5", "Ctrl+F5"},
		{"Ctrl+7", "ctrl+7"},
		{"Shift+Ctrl+F12", "ctrl+shift+f12"},
		{"Win+Alt+0", "alt+win+0"},
	}
	for _, tc := range cases {
		profiles := []config.Profile{
			{Hotkey: tc.a, Languages: []string{"ja", "en"}},
			{Hotkey: tc.b, Target: "ko"},
		}
		form := settingsForm{MaxChars: 4000}
		for _, p := range profiles {
			f, err := formFromProfile(p, "windows")
			if err != nil {
				t.Fatal(err)
			}
			form.Profiles = append(form.Profiles, f)
		}
		if _, err := form.toConfig(config.Default()); err == nil || !strings.Contains(err.Error(), "same key combination") {
			t.Errorf("Settings must reject %q and %q: %v", tc.a, tc.b, err)
		}
	}
}
