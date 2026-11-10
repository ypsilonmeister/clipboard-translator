package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func TestGuideShowsConfiguredHotkey(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.conf.Profiles[0].Hotkey = "Ctrl+Shift+Y"
	a.conf.Hotkey = "Ctrl+Shift+Y"

	g := a.quickstartGuide()
	if !g.Active || !g.Initial || len(g.Steps) != 3 {
		t.Fatalf("guide = %+v", g)
	}
	joined := strings.Join(g.Steps, "\n")
	if !strings.Contains(joined, "Ctrl+Shift+Y") || strings.Contains(joined, "Ctrl+Alt+E") {
		t.Fatalf("the steps must show the configured hotkey: %s", joined)
	}
	if !strings.Contains(joined, platform.PasteShortcut()) {
		t.Fatalf("the steps must name the paste shortcut %s: %s", platform.PasteShortcut(), joined)
	}
	if !strings.Contains(g.Extras, "optional") {
		t.Fatalf("extras must present more settings as optional: %q", g.Extras)
	}
	msg, isErr := a.guideNotification()
	if isErr || !strings.Contains(msg, "Ctrl+Shift+Y") {
		t.Fatalf("notification = %q, %v", msg, isErr)
	}
}

func TestGuideWithInactiveHotkeyPointsToSettings(t *testing.T) {
	a, tray, _ := newTestApp(t)
	a.hotkey = noHotkey{}

	g := a.quickstartGuide()
	if g.Active || len(g.Steps) != 0 || !strings.Contains(g.Intro, "not active") {
		t.Fatalf("guide = %+v", g)
	}
	msg, isErr := a.guideNotification()
	if !isErr || !strings.Contains(msg, "Settings") || strings.Contains(msg, "paste") {
		t.Fatalf("notification = %q, %v", msg, isErr)
	}

	// Nothing usable was shown, so the guide must come back later.
	a.showQuickstart()
	if a.conf.QuickstartShown {
		t.Fatal("an unusable guide must not count as shown")
	}
	if n := len(tray.notes); n == 0 {
		t.Fatal("the user must still be told")
	}
}

func TestGuideShownOnce(t *testing.T) {
	a, _, _ := newTestApp(t)
	if a.conf.QuickstartShown {
		t.Fatal("a fresh config must not have the guide marked as shown")
	}
	a.showQuickstart()
	if !a.conf.QuickstartShown {
		t.Fatal("showing the guide must be remembered")
	}
	b, err := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if err != nil || !strings.Contains(string(b), `"quickstart_shown"`) || !strings.Contains(string(b), "true") {
		t.Fatalf("config.json = %s (%v)", b, err)
	}
	if a.conf.AutoPaste {
		t.Fatal("the guide must not turn on auto-paste")
	}
	if g := a.quickstartGuide(); g.Initial {
		t.Fatal("the page must not open the guide again after the first run")
	}
}

func TestGuideInSettingsState(t *testing.T) {
	a, _, _ := newTestApp(t)
	st := a.settingsState()
	if !st.Guide.Active || len(st.Guide.Steps) != 3 || !strings.Contains(st.Guide.Steps[1], "Ctrl+Alt+E") {
		t.Fatalf("guide in state = %+v", st.Guide)
	}
}
