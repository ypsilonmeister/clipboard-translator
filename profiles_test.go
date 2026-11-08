package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigProfiles(t *testing.T) {
	// No "profiles": one profile from hotkey and languages.
	c, err := loadConfig(writeConfig(t, `{"hotkey": "Ctrl+Shift+Y", "languages": ["en", "ko"]}`))
	if err != nil || len(c.Profiles) != 1 || c.Profiles[0].Hotkey != "Ctrl+Shift+Y" || !samePair(c.Profiles[0].Languages, []string{"en", "ko"}) || c.explicitProfiles {
		t.Fatalf("derived profile: %+v, %v", c, err)
	}

	// Explicit profiles win over hotkey/languages, and profile 0 is mirrored.
	c, err = loadConfig(writeConfig(t, `{"hotkey": "Ctrl+Alt+Q", "languages": ["en", "ko"], "profiles": [
		{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"]},
		{"hotkey": "Ctrl+Alt+K", "target": "KO"}]}`))
	if err != nil || len(c.Profiles) != 2 || !c.explicitProfiles {
		t.Fatalf("explicit profiles: %+v, %v", c, err)
	}
	if c.Hotkey != "Ctrl+Alt+E" || !samePair(c.Languages, []string{"ja", "en"}) {
		t.Errorf("profile 0 must be mirrored in hotkey/languages: %q %v", c.Hotkey, c.Languages)
	}
	if c.Profiles[1].Target != "ko" {
		t.Errorf("target not normalized: %q", c.Profiles[1].Target)
	}

	bad := []struct{ name, json, want string }{
		{"empty", `{"profiles": []}`, "must not be empty"},
		{"both", `{"profiles": [{"hotkey": "Ctrl+Alt+E", "languages": ["ja","en"], "target": "ko"}]}`, "either"},
		{"neither", `{"profiles": [{"hotkey": "Ctrl+Alt+E"}]}`, "languages"},
		{"bad target", `{"profiles": [{"hotkey": "Ctrl+Alt+E", "target": "xx"}]}`, `"xx"`},
		{"bad pair", `{"profiles": [{"hotkey": "Ctrl+Alt+E", "languages": ["en","de"]}]}`, "same script"},
		{"bad hotkey", `{"profiles": [{"hotkey": "E", "target": "ko"}]}`, "profiles[0]"},
		{"same key twice", `{"profiles": [{"hotkey": "Ctrl+Alt+E", "target": "ko"}, {"hotkey": "alt+control+e", "target": "de"}]}`, "same key combination"},
	}
	for _, tc := range bad {
		if err := loadConfig2(t, tc.json); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an error containing %q, got %v", tc.name, tc.want, err)
		}
	}
}

func TestProfileDirection(t *testing.T) {
	target := Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"}
	cases := []struct {
		p      Profile
		input  string
		source string
		target string
		ok     bool
	}{
		{target, "Hello", "", "ko", true},
		{target, "こんにちは", "", "ko", true},
		{target, "Guten Tag", "", "ko", true},
		{target, "https://example.com", "", "", false},
		{target, "12345 !?", "", "", false},
		{target, "   ", "", "", false},
		{Profile{Languages: []string{"ja", "en"}}, "Hello", "en", "ja", true},
	}
	for _, tc := range cases {
		got, ok := tc.p.direction(tc.input, 100)
		if ok != tc.ok || got.Source != tc.source || got.Target != tc.target {
			t.Errorf("%+v %q: got %+v, %v", tc.p, tc.input, got, ok)
		}
	}
	if _, ok := target.direction(strings.Repeat("a", 101), 100); ok {
		t.Error("max_chars ignored for a target profile")
	}
}

func TestTranslateOmitsSourceLangForTargetOnly(t *testing.T) {
	var kwargs map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		kwargs, _ = m["chat_template_kwargs"].(map[string]any)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "안녕"}}}})
	}))
	defer srv.Close()
	if _, err := translate(context.Background(), srv.URL, "", "Hello", translationDirection{Target: "ko"}, 64); err != nil {
		t.Fatal(err)
	}
	if _, has := kwargs["source_lang"]; has {
		t.Errorf("an empty source_lang is rejected by the template; the key must be left out: %v", kwargs)
	}
	if kwargs["target_lang"] != "ko" {
		t.Errorf("target_lang: %v", kwargs)
	}
}

// pressable is a hotkey whose press the test controls.
type pressable struct {
	spec  string
	fired *bool
}

func (p pressable) Pressed() (bool, error) {
	f := *p.fired
	*p.fired = false
	return f, nil
}
func (pressable) Close() error { return nil }

func profilesApp(t *testing.T, profiles ...Profile) *app {
	t.Helper()
	a, _, _ := newTestApp(t)
	a.conf.Profiles = profiles
	a.conf.normalizeProfiles()
	a.hotkeyLimit = 8
	return a
}

func TestExtraHotkeysRegisterAndReportFailures(t *testing.T) {
	a := profilesApp(t,
		Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"},
		Profile{Hotkey: "Ctrl+Alt+D", Target: "de"},
		Profile{Hotkey: "Ctrl+Alt+F", Target: "fr"})
	a.hotkeyLimit = 3 // profile 4 is beyond what this "OS" can hold
	a.newHotkey = func(spec string) (Hotkey, error) {
		if spec == "Ctrl+Alt+D" {
			return nil, errors.New("already registered")
		}
		return fakeHotkey{spec}, nil
	}
	a.registerStartupHotkey()

	if a.hasNoHotkey() {
		t.Fatal("profile 0 should be registered")
	}
	if len(a.extras) != 3 || a.extras[0].hk == nil || a.extras[1].hk != nil || a.extras[2].hk != nil {
		t.Fatalf("extras: %+v", a.extras)
	}
	fails := a.extraFailures()
	if !strings.Contains(fails, "Ctrl+Alt+D") || !strings.Contains(fails, "already registered") ||
		!strings.Contains(fails, "Ctrl+Alt+F") || !strings.Contains(fails, "3 hotkey(s) at most") {
		t.Errorf("failures must name each hotkey and why:\n%s", fails)
	}
	if strings.Contains(fails, "Ctrl+Alt+K") {
		t.Errorf("a working hotkey is listed as failed:\n%s", fails)
	}
	if !strings.Contains(a.readyTip, "Ctrl+Alt+E") || !strings.Contains(a.readyTip, "Ctrl+Alt+K") || strings.Contains(a.readyTip, "Ctrl+Alt+D") {
		t.Errorf("the tooltip must list exactly the working hotkeys: %q", a.readyTip)
	}
}

func TestSecondProfileWorksWhenFirstHotkeyIsTaken(t *testing.T) {
	a := profilesApp(t,
		Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"})
	a.newHotkey = func(spec string) (Hotkey, error) {
		if spec == "Ctrl+Alt+E" {
			return nil, errors.New("already registered")
		}
		return fakeHotkey{spec}, nil
	}
	a.registerStartupHotkey()
	if !a.hasNoHotkey() || a.extras[0].hk == nil {
		t.Fatal("only profile 0 should be without a hotkey")
	}
	if strings.Contains(a.readyTip, "no hotkey") || !strings.Contains(a.readyTip, "Ctrl+Alt+K") {
		t.Errorf("the tooltip must show the working hotkey: %q", a.readyTip)
	}
}

func TestPollHotkeysReportsTheProfileThatFired(t *testing.T) {
	var f0, f1, f2 bool
	a := profilesApp(t,
		Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"},
		Profile{Hotkey: "Ctrl+Alt+D", Target: "de"})
	a.hotkey = pressable{"Ctrl+Alt+E", &f0}
	a.extras = []extraHotkey{{profile: 1, hk: pressable{"Ctrl+Alt+K", &f1}}, {profile: 2, hk: pressable{"Ctrl+Alt+D", &f2}}}

	if p, _, _ := a.pollHotkeys(); p {
		t.Fatal("nothing was pressed")
	}
	f1 = true
	if p, i, _ := a.pollHotkeys(); !p || i != 1 {
		t.Fatalf("profile 1 pressed: %v %d", p, i)
	}
	f0 = true
	if p, i, _ := a.pollHotkeys(); !p || i != 0 {
		t.Fatalf("profile 0 pressed: %v %d", p, i)
	}
	f0, f2 = true, true // two in one tick: the later profile wins, both are consumed
	if p, i, _ := a.pollHotkeys(); !p || i != 2 {
		t.Fatalf("two pressed: %v %d", p, i)
	}
	if f0 || f2 {
		t.Error("a press must be consumed even when another hotkey won the tick")
	}
}

func TestEachProfileTranslatesWithItsOwnLanguages(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		kw, _ := m["chat_template_kwargs"].(map[string]any)
		got = append(got, kw)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "ok"}}}})
	}))
	defer srv.Close()

	a := profilesApp(t,
		Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"})
	a.endpoint = srv.URL
	a.cb = &testClipboard{text: "Hello"}
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }
	a.capture = func(Clipboard, uintptr) (string, bool, error) { return "Hello", true, nil }

	for _, idx := range []int{0, 1} {
		a.startTranslation(idx)
		select {
		case res := <-a.results:
			a.handleResult(res)
		case <-time.After(5 * time.Second):
			t.Fatalf("profile %d: no result", idx)
		}
	}
	if len(got) != 2 {
		t.Fatalf("requests: %v", got)
	}
	if got[0]["source_lang"] != "en" || got[0]["target_lang"] != "ja" {
		t.Errorf("profile 0 (ja/en pair): %v", got[0])
	}
	if _, has := got[1]["source_lang"]; has || got[1]["target_lang"] != "ko" {
		t.Errorf("profile 1 (target ko): %v", got[1])
	}
}

func TestTrayChangesSaveProfilesWhenTheFileHasThem(t *testing.T) {
	a := profilesApp(t,
		Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"})
	a.conf.explicitProfiles = true
	a.hotkey = fakeHotkey{"Ctrl+Alt+E"}

	a.changeLanguages([]string{"en", "ru"})
	a.changeHotkey("Ctrl+Shift+Y")

	b, err := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Profiles []Profile `json:"profiles"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Profiles) != 2 {
		t.Fatalf("profiles not saved: %s %v", b, err)
	}
	if out.Profiles[0].Hotkey != "Ctrl+Shift+Y" || !samePair(out.Profiles[0].Languages, []string{"en", "ru"}) {
		t.Errorf("profile 0 not updated: %+v", out.Profiles[0])
	}
	if out.Profiles[1].Target != "ko" || out.Profiles[1].Hotkey != "Ctrl+Alt+K" {
		t.Errorf("profile 1 must be untouched: %+v", out.Profiles[1])
	}
	if strings.Contains(string(b), `"languages": ["en","ru"],`+"\n"+`  "`) && !strings.Contains(string(b), "profiles") {
		t.Error("the legacy keys must not be written when the file uses profiles")
	}
}

func TestLanguagesMenuWhenFirstProfileHasATarget(t *testing.T) {
	a := profilesApp(t, Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"})
	var found *MenuItem
	for _, it := range a.buildMenu() {
		if strings.HasPrefix(it.Label, "Languages") {
			it := it
			found = &it
		}
	}
	if found == nil || !found.Disabled || len(found.Sub) != 0 || !strings.Contains(found.Label, "Korean") {
		t.Fatalf("a target profile has no pair to pick; the menu must say so: %+v", found)
	}
	a.changeLanguages([]string{"en", "ru"}) // must not turn the target profile into a pair
	if a.conf.Profiles[0].Target != "ko" || a.conf.Profiles[0].Languages != nil {
		t.Errorf("profile 0 was changed: %+v", a.conf.Profiles[0])
	}
}

// A profile whose hotkey is not registered (over the OS limit) holds no key with
// the OS, so registering the same key for profile 0 succeeds. Saving it would
// write a config that loadConfig rejects at the next start (#84).
func TestChangeHotkeyRefusesAKeyUsedByAnInactiveProfile(t *testing.T) {
	a, tray, _ := newTestApp(t)
	a.hotkeyLimit = 1
	a.conf.Profiles = []Profile{
		{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		{Hotkey: "Ctrl+Alt+K", Target: "ko"},
	}
	a.conf.explicitProfiles = true
	a.conf.normalizeProfiles()
	cfg := `{"port": ` + strconv.Itoa(a.conf.Port) + `, "profiles": [{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"]}, {"hotkey": "Ctrl+Alt+K", "target": "ko"}]}`
	path := filepath.Join(a.dir, "config.json")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a.registerStartupHotkey()
	registered := a.hotkey

	// Same key spelled differently: the parsed combinations are compared.
	for _, spec := range []string{"Ctrl+Alt+K", "alt+control+k"} {
		a.changeHotkey(spec)
		if a.hotkey != registered {
			t.Errorf("%s: the current registration must be kept", spec)
		}
		if a.conf.Hotkey != "Ctrl+Alt+E" || a.conf.Profiles[0].Hotkey != "Ctrl+Alt+E" {
			t.Errorf("%s: the hotkey in memory changed: %q", spec, a.conf.Hotkey)
		}
		if note := tray.lastNote(); !strings.Contains(note, "already used") {
			t.Errorf("%s: the user must be told why, got %q", spec, note)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != cfg {
		t.Errorf("config.json must be left alone: %s %v", got, err)
	}
	if _, err := loadConfig(a.dir); err != nil {
		t.Errorf("the saved configuration must still load: %v", err)
	}

	// A free key still works and the result loads.
	a.changeHotkey("Ctrl+Shift+Y")
	if a.conf.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("a free key was refused: %q", a.conf.Hotkey)
	}
	if _, err := loadConfig(a.dir); err != nil {
		t.Errorf("config after a valid change must load: %v", err)
	}
}
