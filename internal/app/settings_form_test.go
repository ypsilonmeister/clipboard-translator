package app

import (
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
)

func TestSettingsCustomHotkeysRoundTrip(t *testing.T) {
	for _, spec := range []string{"Ctrl+Alt+E", "ctrl+alt+e", "Ctrl+Alt+Q"} {
		f, err := settings.FormFromProfile(config.Profile{Hotkey: spec, Languages: []string{"ja", "en"}}, "windows")
		if err != nil || !strings.EqualFold(f.HotkeyString(), spec) {
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
		form := settings.Form{MaxChars: 4000}
		for _, p := range profiles {
			f, err := settings.FormFromProfile(p, "windows")
			if err != nil {
				t.Fatal(err)
			}
			form.Profiles = append(form.Profiles, f)
		}
		if _, err := form.ToConfig(config.Default()); err == nil || !strings.Contains(err.Error(), "same key combination") {
			t.Errorf("Settings must reject %q and %q: %v", tc.a, tc.b, err)
		}
	}
}

func TestSettingsProfileCleanupNewlinesRoundTrip(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{"", config.CleanupNewlinesOff},
		{config.CleanupNewlinesOff, config.CleanupNewlinesOff},
		{config.CleanupNewlinesAuto, config.CleanupNewlinesAuto},
	}
	for _, tc := range cases {
		p := config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, CleanupNewlines: tc.mode}
		f, err := settings.FormFromProfile(p, "windows")
		if err != nil {
			t.Fatalf("FormFromProfile(%q): %v", tc.mode, err)
		}
		if f.CleanupNewlines != tc.want {
			t.Errorf("FormFromProfile(%q) = %q, want %q", tc.mode, f.CleanupNewlines, tc.want)
		}
		sf := settings.Form{MaxChars: 4000, Profiles: []settings.ProfileForm{f}}
		cfg, err := sf.ToConfig(config.Default())
		if err != nil {
			t.Fatalf("ToConfig(%q): %v", tc.mode, err)
		}
		if cfg.Profiles[0].CleanupNewlines != tc.want || cfg.Profiles[0].CleanupNewlinesMode() != tc.want {
			t.Errorf("ToConfig(%q) = %q, want %q", tc.mode, cfg.Profiles[0].CleanupNewlines, tc.want)
		}
	}
}
