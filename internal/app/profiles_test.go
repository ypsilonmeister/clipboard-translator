package app

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

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/llamaserver"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func TestLoadConfigProfiles(t *testing.T) {
	// No "profiles": one profile from hotkey and languages.
	c, err := config.Load(writeConfig(t, `{"hotkey": "Ctrl+Shift+Y", "languages": ["en", "ko"]}`))
	if err != nil || len(c.Profiles) != 1 || c.Profiles[0].Hotkey != "Ctrl+Shift+Y" || !languages.SamePair(c.Profiles[0].Languages, []string{"en", "ko"}) || c.ExplicitProfiles {
		t.Fatalf("derived profile: %+v, %v", c, err)
	}

	// Explicit profiles win over hotkey/languages, and profile 0 is mirrored.
	c, err = config.Load(writeConfig(t, `{"hotkey": "Ctrl+Alt+Q", "languages": ["en", "ko"], "profiles": [
		{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"]},
		{"hotkey": "Ctrl+Alt+K", "target": "KO"}]}`))
	if err != nil || len(c.Profiles) != 2 || !c.ExplicitProfiles {
		t.Fatalf("explicit profiles: %+v, %v", c, err)
	}
	if c.Hotkey != "Ctrl+Alt+E" || !languages.SamePair(c.Languages, []string{"ja", "en"}) {
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
		{"bad output", `{"profiles": [{"hotkey": "Ctrl+Alt+E", "target": "ko", "output": "invalid"}]}`, "not supported"},
		{"same key twice", `{"profiles": [{"hotkey": "Ctrl+Alt+E", "target": "ko"}, {"hotkey": "alt+control+e", "target": "de"}]}`, "same key combination"},
	}
	for _, tc := range bad {
		if err := loadConfig2(t, tc.json); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an error containing %q, got %v", tc.name, tc.want, err)
		}
	}
}

func TestProfileDirection(t *testing.T) {
	target := config.Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"}
	cases := []struct {
		p      config.Profile
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
		{config.Profile{Languages: []string{"ja", "en"}}, "Hello", "en", "ja", true},
	}
	for _, tc := range cases {
		got, ok := tc.p.Direction(tc.input, 100)
		if ok != tc.ok || got.Source != tc.source || got.Target != tc.target {
			t.Errorf("%+v %q: got %+v, %v", tc.p, tc.input, got, ok)
		}
	}
	if _, ok := target.Direction(strings.Repeat("a", 101), 100); ok {
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
	if _, err := llamaserver.Translate(context.Background(), srv.URL, "", "Hello", languages.Direction{Target: "ko"}, 64); err != nil {
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

func profilesApp(t *testing.T, profiles ...config.Profile) *app {
	t.Helper()
	a, _, _ := newTestApp(t)
	a.conf.Profiles = profiles
	a.conf.NormalizeProfiles()
	a.hotkeyLimit = 8
	return a
}

func TestExtraHotkeysRegisterAndReportFailures(t *testing.T) {
	a := profilesApp(t,
		config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		config.Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"},
		config.Profile{Hotkey: "Ctrl+Alt+D", Target: "de"},
		config.Profile{Hotkey: "Ctrl+Alt+F", Target: "fr"})
	a.hotkeyLimit = 3 // profile 4 is beyond what this "OS" can hold
	a.newHotkey = func(spec string) (platform.Hotkey, error) {
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
		config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		config.Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"})
	a.newHotkey = func(spec string) (platform.Hotkey, error) {
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
		config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		config.Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"},
		config.Profile{Hotkey: "Ctrl+Alt+D", Target: "de"})
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
		config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		config.Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"})
	a.endpoint = srv.URL
	a.cb = &testClipboard{text: "Hello"}
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return "Hello", true, nil }

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

func TestEachProfileTranslatesWithItsOwnOutputMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "こんにちは"}}}})
	}))
	defer srv.Close()

	a := profilesApp(t,
		config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: config.OutputReplace},
		config.Profile{Hotkey: "Ctrl+Alt+B", Languages: []string{"ja", "en"}, Output: config.OutputBilingual})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }

	// Profile 0 (replace mode)
	a.cb = &testClipboard{text: "Hello\r\n"}
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return "Hello\r\n", true, nil }
	a.startTranslation(0)
	select {
	case res := <-a.results:
		a.handleResult(res)
	case <-time.After(5 * time.Second):
		t.Fatal("profile 0: no result")
	}
	if a.cb.(*testClipboard).text != "こんにちは" {
		t.Fatalf("profile 0 clipboard = %q, want plain translation", a.cb.(*testClipboard).text)
	}

	// Profile 1 (bilingual mode)
	a.cb = &testClipboard{text: "Hello\r\n"}
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return "Hello\r\n", true, nil }
	a.startTranslation(1)
	select {
	case res := <-a.results:
		a.handleResult(res)
	case <-time.After(5 * time.Second):
		t.Fatal("profile 1: no result")
	}
	wantBilingual := "Hello\nこんにちは"
	if a.cb.(*testClipboard).text != wantBilingual {
		t.Fatalf("profile 1 clipboard = %q, want %q", a.cb.(*testClipboard).text, wantBilingual)
	}
}

// A clipboard profile uses the clipboard text as it is: no Ctrl+C is sent (the
// capture seam is never called) and the result is never auto-pasted. A
// selection profile keeps capturing.
func TestClipboardInputProfileDoesNotCapture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "こんにちは"}}}})
	}))
	defer srv.Close()

	a := profilesApp(t,
		config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Input: config.InputClipboard},
		config.Profile{Hotkey: "Ctrl+Alt+S", Languages: []string{"ja", "en"}, Input: config.InputSelection})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	pastes := 0
	a.paste = func(uintptr) error { pastes++; return nil }
	a.conf.AutoPaste = true

	captures := 0
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) {
		captures++
		return "Selected text", true, nil
	}

	// Profile 0: clipboard input.
	a.cb = &testClipboard{text: "Hello from the clipboard"}
	a.startTranslation(0)
	select {
	case res := <-a.results:
		if res.original != "Hello from the clipboard" || res.fromSelection {
			t.Fatalf("clipboard result: original %q, fromSelection %v", res.original, res.fromSelection)
		}
		a.handleResult(res)
	case <-time.After(5 * time.Second):
		t.Fatal("clipboard profile: no result")
	}
	if captures != 0 {
		t.Fatalf("a clipboard profile must not capture a selection (capture called %d times)", captures)
	}
	if got := a.cb.(*testClipboard).text; got != "こんにちは" {
		t.Fatalf("clipboard = %q, want the translation", got)
	}
	if pastes != 0 {
		t.Fatalf("a clipboard profile must never auto-paste (pastes = %d)", pastes)
	}

	// Profile 1: selection input still captures.
	a.cb = &testClipboard{text: "old clipboard"}
	a.startTranslation(1)
	select {
	case res := <-a.results:
		if res.original != "Selected text" || !res.fromSelection {
			t.Fatalf("selection result: original %q, fromSelection %v", res.original, res.fromSelection)
		}
		a.handleResult(res)
	case <-time.After(5 * time.Second):
		t.Fatal("selection profile: no result")
	}
	if captures != 1 {
		t.Fatalf("a selection profile must capture once (capture called %d times)", captures)
	}
}

// An empty clipboard with a clipboard profile is a routine skip, not an error.
func TestClipboardInputProfileEmptyClipboardIsSkipped(t *testing.T) {
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Input: config.InputClipboard})
	a.foreground = func() uintptr { return 1 }
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) {
		t.Error("capture must not be called for a clipboard profile")
		return "", false, nil
	}
	a.cb = &testClipboard{text: ""}
	a.startTranslation(0)
	if a.translating {
		t.Fatal("an empty clipboard must not start a translation")
	}
	select {
	case res := <-a.results:
		t.Fatalf("unexpected result %+v", res)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSettingsChangesSaveProfilesWhenTheFileHasThem(t *testing.T) {
	a := profilesApp(t,
		config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		config.Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"})
	a.conf.ExplicitProfiles = true
	a.hotkey = fakeHotkey{"Ctrl+Alt+E"}

	changeSettingsLanguages(t, a, []string{"en", "ru"})
	changeSettingsHotkey(t, a, "Ctrl+Shift+Y")

	b, err := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Profiles []config.Profile `json:"profiles"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Profiles) != 2 {
		t.Fatalf("profiles not saved: %s %v", b, err)
	}
	if out.Profiles[0].Hotkey != "Ctrl+Shift+Y" || !languages.SamePair(out.Profiles[0].Languages, []string{"en", "ru"}) {
		t.Errorf("profile 0 not updated: %+v", out.Profiles[0])
	}
	if out.Profiles[1].Target != "ko" || out.Profiles[1].Hotkey != "Ctrl+Alt+K" {
		t.Errorf("profile 1 must be untouched: %+v", out.Profiles[1])
	}
	if strings.Contains(string(b), `"languages": ["en","ru"],`+"\n"+`  "`) && !strings.Contains(string(b), "profiles") {
		t.Error("the legacy keys must not be written when the file uses profiles")
	}
}

func TestSettingsPreservesTargetOnUnrelatedSave(t *testing.T) {
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+K", Target: "ko"})
	saveSettingsField(t, a, "auto_paste", true)
	st := a.settingsState()
	if st.Profiles[0].Target != "ko" || st.Profiles[0].Languages != nil {
		t.Fatalf("target profile changed: %+v", st.Profiles[0])
	}
	for _, it := range a.buildMenu() {
		if strings.HasPrefix(it.Label, "Languages") {
			t.Fatalf("unexpected language menu: %+v", it)
		}
	}
}

// A profile whose hotkey is not registered (over the OS limit) holds no key with
// the OS, so registering the same key for profile 0 succeeds. Saving it would
// write a config that loadConfig rejects at the next start (#84).
func TestChangeHotkeyRefusesAKeyUsedByAnInactiveProfile(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.hotkeyLimit = 1
	a.conf.Profiles = []config.Profile{
		{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		{Hotkey: "Ctrl+Alt+K", Target: "ko"},
	}
	a.conf.ExplicitProfiles = true
	a.conf.NormalizeProfiles()
	cfg := `{"port": ` + strconv.Itoa(a.conf.Port) + `, "profiles": [{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"]}, {"hotkey": "Ctrl+Alt+K", "target": "ko"}]}`
	path := filepath.Join(a.dir, "config.json")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a.registerStartupHotkey()
	registered := a.hotkey

	// Same key spelled differently: the parsed combinations are compared.
	for _, spec := range []string{"Ctrl+Alt+K", "alt+control+k"} {
		err := changeSettingsHotkey(t, a, spec)
		if a.hotkey != registered {
			t.Errorf("%s: the current registration must be kept", spec)
		}
		if a.conf.Hotkey != "Ctrl+Alt+E" || a.conf.Profiles[0].Hotkey != "Ctrl+Alt+E" {
			t.Errorf("%s: the hotkey in memory changed: %q", spec, a.conf.Hotkey)
		}
		if err == nil || !strings.Contains(err.Error(), "same key combination") {
			t.Errorf("%s: the user must be told why, got %v", spec, err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != cfg {
		t.Errorf("config.json must be left alone: %s %v", got, err)
	}
	if _, err := config.Load(a.dir); err != nil {
		t.Errorf("the saved configuration must still load: %v", err)
	}

	// A free key still works and the result loads.
	changeSettingsHotkey(t, a, "Ctrl+Shift+Y")
	if a.conf.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("a free key was refused: %q", a.conf.Hotkey)
	}
	if _, err := config.Load(a.dir); err != nil {
		t.Errorf("config after a valid change must load: %v", err)
	}
}
