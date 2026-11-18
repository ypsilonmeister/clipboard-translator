package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
)

// A direct Write is a test failure. The only permitted write path is the
// clipboard's atomic compare-and-replace operation, including deferred results.
type tidyClipboard struct {
	*testClipboard
	t       *testing.T
	checks  int
	pending bool
}

func TestLanguageOnlySettingsSavesTidyAction(t *testing.T) {
	a := apiUnitApp(t)
	a.cb = &asyncDesktopClipboard{testClipboard: a.cb.(*testClipboard), state: platform.DesktopStatus{Crostini: true, PortalCapabilitiesMissing: true}}
	if err := os.WriteFile(filepath.Join(a.dir, "config.json"), []byte(`{"hotkey":"Ctrl+Alt+E","languages":["ja","en"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{config.ActionTidy, config.ActionTranslate} {
		f := a.settingsState().Profiles[0]
		f.Action, f.Rules, f.PunctStyle = action, []string{"punct"}, config.PunctFullwidth
		f.Key, f.Input = "Q", config.InputClipboard // hidden fields remain protected
		if _, err := a.applySettings(settings.Form{Profiles: []settings.ProfileForm{f}, Changed: []string{"profiles"}}); err != nil {
			t.Fatal(err)
		}
		c, err := config.Load(a.dir)
		if err != nil {
			t.Fatal(err)
		}
		p := c.Profiles[0]
		if p.Action != action || !reflect.DeepEqual(p.Rules, f.Rules) || p.PunctStyle != f.PunctStyle || p.Hotkey != "Ctrl+Alt+E" || p.InputMode() != config.InputSelection {
			t.Fatalf("saved manual profile: %+v", p)
		}
	}
}

func (c *tidyClipboard) Write(string) error {
	c.t.Fatal("tidy called Clipboard.Write instead of ReplaceIfUnchanged")
	return errors.New("unexpected direct write")
}

func (c *tidyClipboard) ReplaceIfUnchanged(old, text string) (bool, error) {
	c.checks++
	if c.pending {
		return false, platform.ErrClipboardPending
	}
	return c.testClipboard.ReplaceIfUnchanged(old, text)
}

func TestTidyPipelineNeverLoadsModelAndUsesSafeReplace(t *testing.T) {
	for _, tc := range []struct {
		name, source, clipboard, want string
		unloaded, changed             bool
	}{
		{"unloaded", "ＡＢＣ ｶﾞ", "ＡＢＣ ｶﾞ", "ABC ガ", true, true},
		{"loaded state", "ＡＢＣ ｶﾞ", "ＡＢＣ ｶﾞ", "ABC ガ", false, true},
		{"unchanged", "ASCII カタカナ", "ASCII カタカナ", "ASCII カタカナ", true, false},
		{"new copy protected", "ＡＢＣ ｶﾞ", "new user copy", "new user copy", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, tray, base, _, _ := uxApp(t)
			cb := &tidyClipboard{testClipboard: base, t: t}
			a.cb = cb
			base.text = tc.clipboard
			a.conf.Profiles = []config.Profile{{Hotkey: "Ctrl+Alt+T", Action: config.ActionTidy, Rules: []string{"width"}, Output: config.OutputBilingual}}
			a.modelUnloaded = tc.unloaded
			a.paths.Server = filepath.Join(t.TempDir(), "no-llama-server")
			a.endpoint = "http://127.0.0.1:0" // no translation endpoint exists
			captures := 0
			a.capture = func(platform.Clipboard, uintptr) (string, bool, error) {
				captures++
				return tc.source, true, nil
			}
			oldGeneration := a.generation
			a.startTranslation(0)
			if captures != 1 || a.generation <= oldGeneration || base.text != tc.want || a.translating {
				t.Fatalf("captures=%d generation=%d text=%q translating=%v", captures, a.generation, base.text, a.translating)
			}
			if a.sup.PID() != 0 || a.sup.Starting() || a.modelUnloaded != tc.unloaded || a.serverWork != 0 {
				t.Fatal("tidy touched the model/server lifecycle")
			}
			for _, status := range tray.statuses {
				if strings.Contains(status, "loading model") || strings.Contains(status, "translating...") {
					t.Fatalf("tidy entered translation path: %q", status)
				}
			}
			if !tc.changed {
				if cb.checks != 0 || base.writes != 0 || len(a.hist.Items()) != 0 {
					t.Fatal("no-op tidy must leave the clipboard and history untouched")
				}
			} else {
				if cb.checks != 1 || len(a.hist.Items()) != 1 {
					t.Fatalf("safe replaces=%d history=%v", cb.checks, a.hist.Items())
				}
				e := a.hist.Items()[0]
				if e.Source != tc.source || e.Translated != "ABC ガ" {
					t.Fatalf("wrong history: %+v", e)
				}
			}
		})
	}
}

func TestTidyOnlyStartupDoesNotStartServer(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.conf.Profiles = []config.Profile{{Hotkey: "Ctrl+Alt+T", Action: config.ActionTidy}}
	a.paths.Server = filepath.Join(t.TempDir(), "missing-server")
	if err := a.startProfileEngine(); err != nil || !a.modelUnloaded || a.sup.PID() != 0 {
		t.Fatalf("tidy-only startup: unloaded=%v pid=%d err=%v", a.modelUnloaded, a.sup.PID(), err)
	}
}

func TestTidyPendingClipboardResultCanBeSuperseded(t *testing.T) {
	for _, supersede := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "supersede"}[supersede], func(t *testing.T) {
			a, _, base, _, _ := uxApp(t)
			cb := &tidyClipboard{testClipboard: base, t: t, pending: true}
			a.cb = cb
			base.text = "ＡＢＣ"
			a.modelUnloaded = true
			a.conf.Profiles = []config.Profile{{Hotkey: "Ctrl+Alt+T", Action: config.ActionTidy, Rules: []string{"width"}, Input: config.InputClipboard}}
			a.capture = func(platform.Clipboard, uintptr) (string, bool, error) {
				t.Fatal("clipboard profile attempted selection capture")
				return "", false, nil
			}
			a.startTranslation(0)
			if a.pendingResult == nil || base.writes != 0 {
				t.Fatal("expected pending compare/write")
			}
			stale := *a.pendingResult
			if supersede {
				a.abortTranslation()
			}
			cb.pending = false
			a.handleResult(stale)
			want := "ABC"
			if supersede {
				want = "ＡＢＣ"
			}
			if base.text != want || a.sup.PID() != 0 {
				t.Fatalf("clipboard=%q, want %q; pid=%d", base.text, want, a.sup.PID())
			}
		})
	}
}

func TestSettingsTidyRoundTripAndValidation(t *testing.T) {
	a, _, _ := newTestApp(t)
	p := config.Profile{Hotkey: "Ctrl+Alt+T", Action: config.ActionTidy, Rules: []string{"width", "punct"}, PunctStyle: config.PunctFullwidth}
	f, err := settings.FormFromProfile(p, "windows")
	if err != nil {
		t.Fatal(err)
	}
	form := settings.Form{Profiles: []settings.ProfileForm{f}, Changed: []string{"profiles"}}
	if _, err := a.applySettings(form); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(a.dir)
	if err != nil || c.Profiles[0].Action != p.Action || !reflect.DeepEqual(c.Profiles[0].Rules, p.Rules) || c.Profiles[0].PunctStyle != p.PunctStyle {
		t.Fatalf("persisted tidy profile: %+v, %v", c.Profiles, err)
	}
	st := a.settingsState().Profiles[0]
	if st.Action != p.Action || !reflect.DeepEqual(st.Rules, p.Rules) || st.PunctStyle != p.PunctStyle {
		t.Fatalf("settings lost tidy fields: %+v", st)
	}
	for _, field := range []string{"action", "rules", "punct_style"} {
		bad := f
		switch field {
		case "action":
			bad.Action = "rewrite"
		case "rules":
			bad.Rules = []string{"unknown"}
		case "punct_style":
			bad.PunctStyle = "unknown"
		}
		if _, err := (settings.Form{Profiles: []settings.ProfileForm{bad}, Changed: []string{"profiles"}}).ToConfig(config.Default()); err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("settings accepted bad %s: %v", field, err)
		}
	}
	f.Rules = []string{"quotes"}
	if _, err := a.applySettings(settings.Form{Profiles: []settings.ProfileForm{f}, Changed: []string{"profiles"}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.conf.Profiles[0].Rules, f.Rules) {
		t.Fatal("a rules-only edit was ignored")
	}
}
