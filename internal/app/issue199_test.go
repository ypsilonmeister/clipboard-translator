package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
)

func TestLanguageOnlySaveOutputAndCleanup(t *testing.T) {
	a := apiUnitApp(t)
	a.cb = &asyncDesktopClipboard{testClipboard: a.cb.(*testClipboard), state: platform.DesktopStatus{Crostini: true, PortalCapabilitiesMissing: true}}
	if err := os.WriteFile(filepath.Join(a.dir, "config.json"), []byte(`{"hotkey":"Ctrl+Alt+E","languages":["ja","en"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	// Edit the visible options, then change languages while retaining those options.
	for _, target := range []string{"", "de", ""} {
		st := a.settingsState()
		if !st.LanguageOnly {
			t.Fatal("manual settings path not active")
		}
		st.Profiles[0].Output = "bilingual"
		st.Profiles[0].CleanupNewlines = "auto"
		st.Profiles[0].Target = target
		st.Profiles[0].Languages = nil
		if target == "" {
			st.Profiles[0].Languages = []string{"ja", "en"}
		}
		st.Profiles[0].Key = "Q"
		st.Profiles[0].Input = "clipboard"
		warnings, err := a.applySettings(settings.Form{BaseRevision: st.BaseRevision, Changed: []string{"profiles"}, Profiles: st.Profiles})
		if err != nil || len(warnings) != 0 {
			t.Fatalf("save: %v %v", err, warnings)
		}
		c, err := config.Load(a.dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []config.Profile{a.conf.Profiles[0], c.Profiles[0]} {
			if p.Output != "bilingual" || p.CleanupNewlines != "auto" || p.Target != target {
				t.Fatalf("profile = %+v", p)
			}
			if p.Hotkey != "Ctrl+Alt+E" || p.InputMode() != config.InputSelection {
				t.Fatalf("hidden fields changed: %+v", p)
			}
		}
	}
}
