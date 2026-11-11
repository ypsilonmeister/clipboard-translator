package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
)

func TestChromeOSLanguageSettings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux capability profile")
	}
	for _, wayland := range []bool{false, true} {
		for _, unavailable := range []bool{false, true} {
			a := apiUnitApp(t)
			a.cb = &asyncDesktopClipboard{testClipboard: a.cb.(*testClipboard), state: platform.DesktopStatus{Wayland: wayland, Manual: wayland && unavailable, PortalWorkflowUnavailable: unavailable}}
			before, _ := json.Marshal(a.conf)
			st := a.settingsState()
			if st.LanguageOnly != unavailable {
				t.Fatalf("language only=%v want %v", st.LanguageOnly, unavailable)
			}
			after, _ := json.Marshal(a.conf)
			if string(before) != string(after) {
				t.Fatal("view changed config")
			}
		}
	}
}

func TestChromeOSLanguageSavePreservesHotkeys(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux capability profile")
	}
	a := apiUnitApp(t)
	a.cb = &asyncDesktopClipboard{testClipboard: a.cb.(*testClipboard), state: platform.DesktopStatus{PortalWorkflowUnavailable: true}}
	const original = `{
  "hotkey" : "Alt+Shift+E",
  "languages": ["ja", "en"],
  "profiles" : [ { "hotkey" : "Alt+Shift+E", "languages" : ["ja", "en"], "output":"bilingual", "input":"clipboard", "cleanup_newlines":"auto", "future": {"x": 1} },
    {"hotkey":"Ctrl+Alt+T", "target" : "fr", "output":"replace", "future":true} ],
  "unknown" : 42
}
`
	if err := os.WriteFile(filepath.Join(a.dir, "config.json"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	a.conf.Profiles = []config.Profile{{Hotkey: "Alt+Shift+E", Languages: []string{"ja", "en"}, Output: "bilingual", Input: "clipboard", CleanupNewlines: "auto"}, {Hotkey: "Ctrl+Alt+T", Target: "fr", Output: "replace"}}
	if err := a.conf.ResolveProfiles(true); err != nil {
		t.Fatal(err)
	}
	st := a.settingsState()
	view, _ := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if string(view) != original {
		t.Fatal("view rewrote file")
	}
	// Deliberately alter hidden controls: the server must ignore them.
	st.Profiles[0].Languages = nil
	st.Profiles[0].Target = "de"
	st.Profiles[0].Key = "Q"
	st.Profiles[0].Output = "replace"
	st.Profiles[1].Target = "es"
	warnings, err := a.applySettings(settings.Form{BaseRevision: st.BaseRevision, Changed: []string{"profiles"}, Profiles: st.Profiles})
	if err != nil || len(warnings) > 0 {
		t.Fatalf("save: %v %v", err, warnings)
	}
	got, _ := os.ReadFile(filepath.Join(a.dir, "config.json"))
	want := strings.Replace(original, `"languages" : ["ja", "en"]`, `"languages" : null`, 1)
	want = strings.Replace(want, `"future": {"x": 1} }`, `"future": {"x": 1} ,"target":"de"}`, 1)
	if string(got) != want {
		t.Fatalf("file changed outside language values:\ngot %s\nwant %s", got, want)
	}
	if a.conf.Profiles[0].Target != "de" || a.conf.Profiles[0].Hotkey != "Alt+Shift+E" || a.conf.Profiles[0].Output != "bilingual" || a.conf.Profiles[1].Target != "fr" {
		t.Fatalf("config=%+v", a.conf.Profiles)
	}
	reloaded, err := config.Load(a.dir)
	if err != nil || reloaded.Profiles[0].Target != "de" {
		t.Fatalf("reload: %+v %v", reloaded.Profiles, err)
	}
	// A save without language edits must not rewrite profiles either.
	_, err = a.applySettings(settings.Form{Changed: []string{"notify_on_complete"}, NotifyOnComplete: !a.conf.NotifyOnComplete})
	if err != nil {
		t.Fatal(err)
	}
}

func TestChromeOSReadyTip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux capability profile")
	}
	a := apiUnitApp(t)
	a.cb = &asyncDesktopClipboard{testClipboard: a.cb.(*testClipboard), state: platform.DesktopStatus{PortalWorkflowUnavailable: true}}
	a.updateReadyTip()
	if a.readyTip != "Clipboard Translator - ready" {
		t.Fatal(a.readyTip)
	}
	a.cb = &asyncDesktopClipboard{testClipboard: a.cb.(*asyncDesktopClipboard).testClipboard}
	a.conf.Profiles[0].Hotkey = "Alt+Shift+E"
	a.hotkey = fakeHotkey{spec: "Alt+Shift+E"}
	a.updateReadyTip()
	if a.readyTip != "Clipboard Translator - ready (Alt+Shift+E)" {
		t.Fatal(a.readyTip)
	}
	a.cb.(*asyncDesktopClipboard).state.PortalWorkflowUnavailable = true
	a.modelUnloaded = true
	a.updateReadyTip()
	if strings.Contains(a.readyTip, "hotkey") {
		t.Fatal(a.readyTip)
	}
}
