package app

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func TestChromeOSManualMenu(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux manual profile")
	}
	a := apiUnitApp(t)
	base := a.cb.(*testClipboard)
	cb := &asyncDesktopClipboard{testClipboard: base, state: platform.DesktopStatus{Wayland: true, Manual: true, Crostini: true, PortalCapabilitiesMissing: true}}
	a.cb = cb
	for _, enabled := range []bool{false, true} {
		a.conf.TranslateAPIEnabled = enabled
		items := a.buildMenu()
		labels := []string{"Settings...", "Quick start", "Translate clipboard", "Chrome extension endpoint", "Copy connection info"}
		if enabled {
			labels = append(labels, "ChromeOS: enable port forwarding for TCP "+translateAPIPort)
		}
		labels = append(labels, "Manual mode (hotkey unavailable)", "Retry Wayland permissions")
		for i, label := range labels {
			if items[i].Label != label {
				t.Fatalf("item %d = %+v; want %q", i, items[i], label)
			}
		}
		if items[3].Checked != enabled || (enabled && !items[5].Disabled) {
			t.Fatal("endpoint state missing")
		}
	}
	st := a.settingsState()
	if st.AutoPasteUnavailable != "Not available on ChromeOS: the app cannot paste into other apps" || len(st.Guide.Steps) != 4 || !st.Guide.Active {
		t.Fatalf("state = %+v", st)
	}
	a.conf.TranslateAPIEnabled = false
	a.handleCommand(cmdCopyConnectionInfo)
	var info struct{ Endpoint, Token string }
	if err := json.Unmarshal([]byte(base.text), &info); err != nil {
		t.Fatal(err)
	}
	if info.Endpoint != translateAPIURL() || len(info.Token) != 64 || a.apiServer != nil {
		t.Fatalf("invalid connection info or listener started")
	}
	data, err := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["translate_api_token"] != info.Token {
		t.Fatal("token not persisted")
	}
	a.handleCommand(cmdCopyConnectionInfo)
	if a.conf.TranslateAPIToken != info.Token {
		t.Fatal("token changed")
	}
	cb.state.Manual = false
	cb.state.PortalCapabilitiesMissing = false
	if a.settingsState().AutoPasteUnavailable != "" {
		t.Fatal("note escaped unavailable-portal profile")
	}
	for _, item := range a.buildMenu() {
		if item.ID == cmdTranslateAPIToggle || item.ID == cmdCopyConnectionInfo {
			t.Fatal("endpoint menu escaped unavailable-portal profile")
		}
	}
}

func TestChromeOSMenuEndpointToggle(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux manual profile")
	}
	a := apiUnitApp(t)
	base := a.cb.(*testClipboard)
	a.cb = &asyncDesktopClipboard{testClipboard: base, state: platform.DesktopStatus{Wayland: true, Manual: true, Crostini: true, PortalCapabilitiesMissing: true}}
	// A free endpoint port is needed for this listener lifecycle check.
	ln, err := net.Listen("tcp4", net.JoinHostPort("0.0.0.0", translateAPIPort))
	if err != nil {
		t.Skipf("endpoint port unavailable: %v", err)
	}
	ln.Close()
	t.Cleanup(a.stopTranslateAPI)
	a.handleCommand(cmdTranslateAPIToggle)
	if !a.conf.TranslateAPIEnabled || a.apiServer == nil {
		t.Fatal("menu did not enable endpoint")
	}
	saved, err := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var conf map[string]any
	if err := json.Unmarshal(saved, &conf); err != nil {
		t.Fatal(err)
	}
	if conf["translate_api_enabled"] != true {
		t.Fatal("menu did not persist enabled state")
	}
	a.handleCommand(cmdTranslateAPIToggle)
	if a.conf.TranslateAPIEnabled || a.apiServer != nil {
		t.Fatal("menu did not disable endpoint")
	}
}

func TestChromeOSMenuEndpointSaveFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux manual profile")
	}
	a := apiUnitApp(t)
	a.cb = &asyncDesktopClipboard{testClipboard: a.cb.(*testClipboard), state: platform.DesktopStatus{Wayland: true, Manual: true, Crostini: true, PortalCapabilitiesMissing: true}}
	// A file cannot serve as the config directory, so token persistence fails before listen.
	a.dir = filepath.Join(a.dir, "blocked")
	if err := os.WriteFile(a.dir, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	a.handleCommand(cmdTranslateAPIToggle)
	if a.conf.TranslateAPIEnabled || a.apiServer != nil || a.conf.TranslateAPIToken != "" {
		t.Fatal("failed toggle changed endpoint state")
	}
}

func TestChromeOSProfileCapability(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux capability profile")
	}
	for _, wayland := range []bool{false, true} {
		for _, unavailable := range []bool{false, true} {
			a := apiUnitApp(t)
			a.cb = &asyncDesktopClipboard{testClipboard: a.cb.(*testClipboard), state: platform.DesktopStatus{
				Crostini: true, Wayland: wayland, Manual: wayland && unavailable, PortalCapabilitiesMissing: unavailable,
			}}
			if got := a.chromeOSProfile(); got != unavailable {
				t.Fatalf("wayland=%v unavailable=%v: profile=%v", wayland, unavailable, got)
			}
			found := false
			for _, item := range a.buildMenu() {
				if item.Label == "Chrome extension endpoint" {
					found = true
				}
			}
			if found != unavailable {
				t.Fatalf("wayland=%v unavailable=%v: endpoint=%v", wayland, unavailable, found)
			}
			if got := a.settingsState().AutoPasteUnavailable; unavailable && got != "Not available on ChromeOS: the app cannot paste into other apps" {
				t.Fatalf("missing profile settings note: %q", got)
			}
		}
	}
}

func TestOrdinaryDesktopNeverChromeOS(t *testing.T) {
	a := apiUnitApp(t)
	for _, wayland := range []bool{false, true} {
		a.cb = &asyncDesktopClipboard{testClipboard: &testClipboard{}, state: platform.DesktopStatus{Wayland: wayland, PortalCapabilitiesMissing: true}}
		if a.chromeOSProfile() {
			t.Fatal("ordinary Linux desktop received ChromeOS profile")
		}
	}
}

func TestConnectionCopyWhileTranslating(t *testing.T) {
	a := apiUnitApp(t)
	cb := a.cb.(*testClipboard)
	cb.text = "source"
	cancelled := false
	a.cancelCurrent = func() { cancelled = true }
	a.translating = true
	generation := a.generation
	a.copyConnectionInfo()
	if cancelled || a.generation != generation || cb.text == "source" || len(a.conf.TranslateAPIToken) != 64 || !a.translating {
		t.Fatal("copy disturbed active translation")
	}
	var info struct{ Endpoint, Token string }
	if err := json.Unmarshal([]byte(cb.text), &info); err != nil || info.Endpoint != translateAPIURL() || info.Token != a.conf.TranslateAPIToken {
		t.Fatalf("connection info = %q, error %v", cb.text, err)
	}
}

func TestReadyMessageWithoutHotkey(t *testing.T) {
	if got := readyMessage(""); got != "ready: copy text and choose Translate clipboard, then paste" {
		t.Fatal(got)
	}
	if got := readyMessage("Ctrl+Alt+E"); got != "ready: select text and press Ctrl+Alt+E (or translate existing clipboard)" {
		t.Fatal(got)
	}
}

func TestConnectionCopyTokenNotice(t *testing.T) {
	a := apiUnitApp(t)
	a.copyConnectionInfo()
	tray := a.tray.(*fakeTray)
	if len(tray.notes) != 1 || !strings.Contains(tray.notes[0], "token in clear text") {
		t.Fatalf("notifications: %v", tray.notes)
	}
	if !strings.Contains(a.cb.(*testClipboard).text, a.conf.TranslateAPIToken) || len(a.conf.TranslateAPIToken) != 64 {
		t.Fatal("connection info not copied")
	}
}
