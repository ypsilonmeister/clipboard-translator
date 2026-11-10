package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
)

// settingsFixture is an app whose settings requests are answered by a goroutine
// standing in for the main loop (serve), with fake hotkeys that can be made to fail.
type settingsFixture struct {
	a       *app
	taken   map[string]bool // hotkeys "another program" owns
	reg     atomic.Int32    // how many hotkeys were registered
	opened  []string
	stopped chan struct{}
}

func newSettingsFixture(t *testing.T) *settingsFixture {
	t.Helper()
	a, _, _ := newTestApp(t)
	f := &settingsFixture{a: a, taken: map[string]bool{}, stopped: make(chan struct{})}
	a.hotkeyLimit = 8
	a.newHotkey = func(spec string) (platform.Hotkey, error) {
		f.reg.Add(1)
		if f.taken[strings.ToUpper(spec)] {
			return nil, errors.New("already registered")
		}
		return fakeHotkey{spec}, nil
	}
	a.openURL = func(u string) error { f.opened = append(f.opened, u); return nil }
	a.registerStartupHotkey()
	go func() {
		for {
			select {
			case req := <-a.settingsReqs:
				a.handleSettingsRequest(req)
			case <-f.stopped:
				return
			}
		}
	}()
	t.Cleanup(func() { close(f.stopped); a.closeSettings() })
	return f
}

// testServer is a settings.Server plus the requests the tests make against it.
type testServer struct{ *settings.Server }

func (f *settingsFixture) server(t *testing.T) testServer {
	t.Helper()
	s, err := settings.Start(f.a.askSettings, f.a.dl, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.a.settings = s
	return testServer{s}
}

// hostAndToken reads what a browser would get from the server's URL.
func (s testServer) hostAndToken(t *testing.T) (host, token string) {
	t.Helper()
	u, err := url.Parse(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	return u.Host, u.Query().Get("t")
}

func (s testServer) do(t *testing.T, method, path string, body any, edit func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	host, token := s.hostAndToken(t)
	req, err := http.NewRequest(method, "http://"+host+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(settings.TokenHeader, token)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if edit != nil {
		edit(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func decodeAPI(t *testing.T, b []byte) settings.APIResponse {
	t.Helper()
	var r settings.APIResponse
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("not JSON: %s", b)
	}
	return r
}

func validForm() settings.Form {
	return settings.Form{AutoPaste: true, MaxChars: 4000, Profiles: []settings.ProfileForm{
		{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "en"}},
		{Ctrl: true, Alt: true, Key: "K", Target: "ko"},
	}}
}

func TestSettingsServerRefusesWhatItShould(t *testing.T) {
	s := newSettingsFixture(t).server(t)
	host, token := s.hostAndToken(t)
	_, _ = host, token
	if !strings.HasPrefix(s.URL(), "http://127.0.0.1:") || !strings.Contains(s.URL(), "/?t="+token) {
		t.Fatalf("the page must be on 127.0.0.1 and carry the token: %s", s.URL())
	}
	if len(token) < 32 {
		t.Errorf("token too short: %q", token)
	}
	port := strings.Split(host, ":")[1]
	cases := []struct {
		name   string
		method string
		path   string
		body   any
		edit   func(*http.Request)
		want   int
	}{
		{"no token", "GET", "/api/state", nil, func(r *http.Request) { r.Header.Del(settings.TokenHeader) }, 403},
		{"wrong token", "GET", "/api/state", nil, func(r *http.Request) { r.Header.Set(settings.TokenHeader, "nope") }, 403},
		{"page without token", "GET", "/", nil, func(r *http.Request) { r.Header.Del(settings.TokenHeader) }, 403},
		{"asset without token", "GET", "/app.js", nil, func(r *http.Request) { r.Header.Del(settings.TokenHeader) }, 403},
		{"rebinding Host", "GET", "/api/state", nil, func(r *http.Request) { r.Host = "evil.example:" + port }, 403},
		{"localhost is not our Host", "GET", "/api/state", nil, func(r *http.Request) { r.Host = "localhost:" + port }, 403},
		{"cross-origin save", "POST", "/api/save", validForm(), func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		{"cross-site fetch", "POST", "/api/save", validForm(), func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"cross-origin delete", "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "model"}, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		{"cross-site delete", "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "model"}, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"form post", "POST", "/api/save", validForm(), func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"form post delete", "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "model"}, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"GET on save", "GET", "/api/save", nil, nil, 405},
		{"GET on delete", "GET", "/api/model/delete", nil, nil, 405},
		{"POST on state", "POST", "/api/state", map[string]int{}, nil, 405},
		{"unknown field on delete", "POST", "/api/model/delete", map[string]any{"key": "2b", "kind": "model", "extra": 1}, nil, 400},
		{"unknown path", "GET", "/nothing", nil, nil, 404},
	}
	for _, tc := range cases {
		resp, _ := s.do(t, tc.method, tc.path, tc.body, tc.edit)
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
}

func TestSettingsServerHeadersAndAssets(t *testing.T) {
	s := newSettingsFixture(t).server(t)
	host, token := s.hostAndToken(t)
	_, _ = host, token
	resp, body := s.do(t, "GET", "/?t="+token, nil, func(r *http.Request) { r.Header.Del(settings.TokenHeader) })
	if resp.StatusCode != 200 || !strings.Contains(string(body), "app.js?t="+token) || strings.Contains(string(body), "{{TOKEN}}") {
		t.Fatalf("the page must link its assets with the token: %d %s", resp.StatusCode, body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("cache / referrer headers: %v", resp.Header)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS must stay off")
	}
	// The page must not load or run anything from elsewhere.
	for _, bad := range []string{"http://", "https://", "<script>", "onclick=", "style="} {
		if strings.Contains(string(body), bad) {
			t.Errorf("index.html contains %q", bad)
		}
	}
	for _, p := range []string{"/app.js", "/app.css"} {
		r, b := s.do(t, "GET", p+"?t="+token, nil, func(r *http.Request) { r.Header.Del(settings.TokenHeader) })
		if r.StatusCode != 200 || len(b) < 500 {
			t.Errorf("%s: %d, %d bytes", p, r.StatusCode, len(b))
		}
	}
	// An author rule like display:flex beats the hidden attribute; this one restores it
	// (the busy bar once stayed on screen because of that).
	css, _ := os.ReadFile("../settings/web/app.css")
	if !strings.Contains(string(css), "[hidden] { display: none !important; }") {
		t.Error("app.css must make the hidden attribute win over author display rules")
	}
	js, _ := os.ReadFile("../settings/web/app.js")
	for _, bad := range []string{"innerHTML", "outerHTML", "document.write", "eval("} {
		if strings.Contains(string(js), bad) {
			t.Errorf("app.js uses %s", bad)
		}
	}
	// Issue #81: goodbye must clear timers and reset dirty state, and save must clear stale banner.
	for _, want := range []string{"clearTimeout(pollTimer)", "clearInterval(pingTimer)", "showBanner([])"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

// The page logic is not run by go test, so these pin the three rules that keep a
// stale save from slipping through (found in review of #85).
func TestSettingsPageKeepsStaleSaveGuards(t *testing.T) {
	b, err := os.ReadFile("../settings/web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := strings.ReplaceAll(string(b), "\r\n", "\n")
	// The revision sent with a save is the one the edits are based on, not whatever
	// the last sync saw; a sync moves it only while nothing is dirty.
	if !strings.Contains(js, "base_revision: baseRevision,") || strings.Contains(js, "base_revision: (state") {
		t.Error("toPayload must send baseRevision, not the revision of the last sync")
	}
	if !strings.Contains(js, "if (!isDirty()) baseRevision = st.revision") {
		t.Error("mergeState must move baseRevision only while nothing is being edited")
	}
	// The poll must not rebuild the hotkey cards when nothing changed.
	if !strings.Contains(js, "JSON.stringify(next) !== JSON.stringify(model.profiles)") {
		t.Error("mergeState must rebuild the hotkey cards only when the profiles differ")
	}
	// Save with nothing edited sends nothing, unless no hotkey is active (in which case it retries).
	if !strings.Contains(js, "setStatus('No changes to save')") {
		t.Error("save must do nothing when no section is dirty")
	}
	if !strings.Contains(js, "isSaveNeeded") || !strings.Contains(js, "state.no_active_hotkey") {
		t.Error("save must check isSaveNeeded and retry when no hotkey is active")
	}
	if !strings.Contains(js, "dirtyFields.profiles || (state && state.no_active_hotkey)") {
		t.Error("toPayload must send profiles when state.no_active_hotkey is set")
	}
	if nodePath, err := exec.LookPath("node"); err == nil {
		cmd := exec.Command(nodePath, "-e", `
const fs = require('fs');
const js = fs.readFileSync('../settings/web/app.js', 'utf8');
const match = js.match(/function isSaveNeeded\([^)]*\)\s*\{[^}]*\}/);
if (!match) throw new Error('isSaveNeeded not found in app.js');
const fn = new Function('return ' + match[0])();
if (fn(false, false) !== false) throw new Error('isSaveNeeded(false, false) must be false');
if (fn(true, false) !== true) throw new Error('isSaveNeeded(true, false) must be true');
if (fn(false, true) !== true) throw new Error('isSaveNeeded(false, true) must be true');
if (fn(true, true) !== true) throw new Error('isSaveNeeded(true, true) must be true');
`)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("node test of isSaveNeeded failed: %v: %s", err, string(out))
		}
	}
}

func TestSettingsStateAndSave(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	_, body := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, body).State
	if st == nil || len(st.Profiles) != 1 || st.Profiles[0].Key != "E" || !st.Profiles[0].Ctrl || !st.Profiles[0].Alt || st.Profiles[0].Shift {
		t.Fatalf("state: %s", body)
	}
	if len(st.Languages) != 53 || st.Languages[0].Code != "ja" || st.Languages[1].Code != "en" {
		t.Errorf("language options: %d, first %v", len(st.Languages), st.Languages[:2])
	}
	if st.MaxHotkeys != 8 || st.WinKey == "" {
		t.Errorf("limit / win key: %+v", st)
	}

	// Add a hotkey and turn auto-paste on.
	resp, body := s.do(t, "POST", "/api/save", validForm(), nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.Error != "" || r.State == nil || len(r.State.Profiles) != 2 {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	if len(f.a.extras) != 1 || f.a.extras[0].hk == nil || f.a.conf.Profiles[1].Target != "ko" || !f.a.conf.AutoPaste {
		t.Fatalf("not applied: extras=%+v conf=%+v", f.a.extras, f.a.conf.Profiles)
	}
	if !strings.Contains(f.a.readyTip, "Ctrl+Alt+K") {
		t.Errorf("the tooltip must list the new hotkey: %q", f.a.readyTip)
	}

	raw, err := os.ReadFile(filepath.Join(f.a.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("config.json: %v\n%s", err, raw)
	}
	if cfg["hotkey"] != "Ctrl+Alt+E" || cfg["auto_paste"] != true || cfg["port"] == nil {
		t.Errorf("config.json lost or missed keys:\n%s", raw)
	}
	if ps, ok := cfg["profiles"].([]any); !ok || len(ps) != 2 {
		t.Errorf("profiles not saved:\n%s", raw)
	}
	// What was saved loads again as the same settings.
	c, err := config.Load(f.a.dir)
	if err != nil || len(c.Profiles) != 2 || c.Profiles[1].Target != "ko" || !c.AutoPaste {
		t.Fatalf("saved config does not load back: %+v %v", c, err)
	}

	// The same settings again change nothing, so nothing is registered again.
	before := f.reg.Load()
	if resp, _ := s.do(t, "POST", "/api/save", validForm(), nil); resp.StatusCode != 200 {
		t.Fatalf("second save: %d", resp.StatusCode)
	}
	if f.reg.Load() != before {
		t.Error("an unchanged save registered the hotkeys again")
	}

	// Only auto-paste changes: hotkeys stay registered.
	form := validForm()
	form.AutoPaste = false
	s.do(t, "POST", "/api/save", form, nil)
	if f.reg.Load() != before || f.a.conf.AutoPaste {
		t.Errorf("auto-paste only: registrations %d -> %d, autopaste %v", before, f.reg.Load(), f.a.conf.AutoPaste)
	}
}

func TestSettingsSaveRejectsInvalidAndChangesNothing(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)
	before := f.reg.Load()
	cfgBefore, _ := os.ReadFile(filepath.Join(f.a.dir, "config.json"))

	bad := map[string]settings.Form{
		"no profiles":    {},
		"no modifier":    {MaxChars: 4000, Profiles: []settings.ProfileForm{{Key: "E", Languages: []string{"ja", "en"}}}},
		"invalid key":    {MaxChars: 4000, Profiles: []settings.ProfileForm{{Ctrl: true, Key: "!", Languages: []string{"ja", "en"}}}},
		"same script":    {MaxChars: 4000, Profiles: []settings.ProfileForm{{Ctrl: true, Key: "E", Languages: []string{"en", "de"}}}},
		"unknown target": {MaxChars: 4000, Profiles: []settings.ProfileForm{{Ctrl: true, Key: "E", Target: "xx"}}},
		"duplicate key": {MaxChars: 4000, Profiles: []settings.ProfileForm{
			{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "en"}},
			{Alt: true, Ctrl: true, Key: "e", Target: "ko"}}},
		"both modes":     {MaxChars: 4000, Profiles: []settings.ProfileForm{{Ctrl: true, Key: "E", Languages: []string{"ja", "en"}, Target: "ko"}}},
		"unknown output": {MaxChars: 4000, Profiles: []settings.ProfileForm{{Ctrl: true, Key: "E", Target: "ko", Output: "foo"}}},
	}
	for name, form := range bad {
		resp, body := s.do(t, "POST", "/api/save", form, nil)
		r := decodeAPI(t, body)
		if resp.StatusCode != 422 || r.Error == "" {
			t.Errorf("%s: status %d, body %s", name, resp.StatusCode, body)
		}
	}
	// The error names the profile so the page can show it there.
	_, body := s.do(t, "POST", "/api/save", settings.Form{MaxChars: 4000, Profiles: []settings.ProfileForm{
		{Ctrl: true, Key: "E", Languages: []string{"ja", "en"}}, {Ctrl: true, Key: "K", Languages: []string{"en", "de"}}}}, nil)
	if e := decodeAPI(t, body).Error; !strings.HasPrefix(e, "profiles[1]:") {
		t.Errorf("error should start with profiles[1]: %q", e)
	}
	// Unknown fields are refused rather than ignored.
	if resp, _ := s.do(t, "POST", "/api/save", map[string]any{"profiles": []any{}, "hotkey": "x"}, nil); resp.StatusCode != 400 {
		t.Errorf("unknown field: status %d", resp.StatusCode)
	}
	if f.reg.Load() != before {
		t.Error("an invalid save touched the hotkeys")
	}
	if cfgAfter, _ := os.ReadFile(filepath.Join(f.a.dir, "config.json")); string(cfgAfter) != string(cfgBefore) {
		t.Error("an invalid save wrote config.json")
	}
}

func TestSettingsSavePartialAndTotalHotkeyFailure(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	// One of the new hotkeys is taken: the other works and the answer says which does not.
	f.taken["CTRL+ALT+K"] = true
	resp, body := s.do(t, "POST", "/api/save", validForm(), nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.State == nil {
		t.Fatalf("partial failure should still save: %d %s", resp.StatusCode, body)
	}
	joined := strings.Join(r.Warnings, "\n")
	if !strings.Contains(joined, "Ctrl+Alt+K") || !strings.Contains(joined, "already registered") {
		t.Errorf("warnings must name the inactive hotkey: %v", r.Warnings)
	}
	if len(r.State.Inactive) != 1 || f.a.hasNoHotkey() {
		t.Errorf("inactive=%v noHotkey=%v", r.State.Inactive, f.a.hasNoHotkey())
	}

	// Nothing can be registered: the previous settings stay, and the answer is an error.
	f.taken["CTRL+SHIFT+Y"] = true
	f.taken["CTRL+SHIFT+Z"] = true
	prev := f.a.conf.Profiles
	resp, body = s.do(t, "POST", "/api/save", settings.Form{MaxChars: 4000, Profiles: []settings.ProfileForm{
		{Ctrl: true, Shift: true, Key: "Y", Languages: []string{"ja", "en"}}, {Ctrl: true, Shift: true, Key: "Z", Target: "ko"}}}, nil)
	r = decodeAPI(t, body)
	if resp.StatusCode != 422 || !strings.Contains(r.Error, "previous settings were kept") {
		t.Fatalf("total failure: %d %s", resp.StatusCode, body)
	}
	if !sameProfiles(f.a.conf.Profiles, prev) || f.a.hasNoHotkey() || len(f.a.extras) != 1 {
		t.Errorf("the previous hotkeys must be back: conf=%+v extras=%+v", f.a.conf.Profiles, f.a.extras)
	}
}

func TestSettingsLimitIsReportedNotRefused(t *testing.T) {
	// When an OS build registers fewer hotkeys than configured (e.g. macOS holding
	// only one grab), the page must be able to save it and say which hotkeys are not active.
	f := newSettingsFixture(t)
	f.a.hotkeyLimit = 1
	s := f.server(t)
	resp, body := s.do(t, "POST", "/api/save", validForm(), nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.State == nil || len(r.State.Inactive) != 1 || !strings.Contains(r.State.Inactive[0], "at most") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if r.State.MaxHotkeys != 1 {
		t.Errorf("max hotkeys: %d", r.State.MaxHotkeys)
	}
}

func TestSettingsRequestTimedOutIsNeverApplied(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.hotkeyLimit = 8
	a.newHotkey = func(spec string) (platform.Hotkey, error) { return fakeHotkey{spec}, nil }
	a.registerStartupHotkey()

	// The loop is busy: the request times out and the page is told so.
	form := validForm()
	req := settings.NewRequest(&form, 30*time.Millisecond)
	rep := a.askSettings(req)
	if !errors.Is(rep.Err, settings.ErrBusy) {
		t.Fatalf("want busy, got %+v", rep)
	}
	// When the loop gets to it later, it must be dropped, not applied.
	time.Sleep(40 * time.Millisecond)
	a.handleSettingsRequest(<-a.settingsReqs)
	if len(a.conf.Profiles) != 1 || a.conf.AutoPaste {
		t.Fatalf("a request the page was told had failed was applied: %+v", a.conf)
	}
	if got := <-req.Reply; !errors.Is(got.Err, settings.ErrBusy) {
		t.Errorf("the dropped request should be answered: %+v", got)
	}
}

func TestSettingsServerClosesWhenIdleOrToldTo(t *testing.T) {
	f := newSettingsFixture(t)
	s, err := settings.Start(f.a.askSettings, f.a.dl, 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Running() {
		t.Fatal("should be running")
	}
	host, _ := testServer{s}.hostAndToken(t)
	time.Sleep(300 * time.Millisecond)
	if s.Running() {
		t.Fatal("an idle server must close itself")
	}
	if _, err := http.Get("http://" + host + "/"); err == nil {
		t.Error("the port is still open")
	}

	s2 := f.server(t)
	if resp, _ := s2.do(t, "POST", "/api/bye", map[string]int{}, nil); resp.StatusCode != 204 {
		t.Fatalf("bye: %d", resp.StatusCode)
	}
	deadline := time.Now().Add(2 * time.Second)
	for s2.Running() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s2.Running() {
		t.Error("bye must close the server")
	}
}

func TestSettingsMenuItemOpensThePageAndReusesIt(t *testing.T) {
	f := newSettingsFixture(t)
	var item *platform.MenuItem
	for _, it := range f.a.buildMenu() {
		if it.Label == "Settings..." {
			it := it
			item = &it
		}
	}
	if item == nil {
		t.Fatal("the tray menu has no Settings... item")
	}
	f.a.handleCommand(item.ID)
	f.a.handleCommand(item.ID)
	if len(f.opened) != 2 || f.opened[0] != f.opened[1] || !strings.HasPrefix(f.opened[0], "http://127.0.0.1:") {
		t.Fatalf("opened: %v", f.opened)
	}
	first := f.a.settings
	// Once the server closed itself, the next click starts a new one.
	first.Close()
	f.a.handleCommand(item.ID)
	if f.a.settings == first || !f.a.settings.Running() || len(f.opened) != 3 || f.opened[2] == f.opened[0] {
		t.Errorf("a closed server must be replaced: %v", f.opened)
	}
}

func TestProfileFormRoundTrip(t *testing.T) {
	for _, hk := range []string{"Ctrl+Alt+E", "Ctrl+Shift+Y", "Alt+K", "Ctrl+Alt+Shift+Win+Q", "Ctrl+7", "Alt+F5", "Ctrl+Shift+F12"} {
		f, err := settings.FormFromProfile(config.Profile{Hotkey: hk, Languages: []string{"ja", "en"}}, "windows")
		if err != nil {
			t.Fatal(err)
		}
		if got := f.HotkeyString(); got != hk {
			t.Errorf("%q -> %+v -> %q", hk, f, got)
		}
	}
	for _, tc := range []struct {
		hotkey  string
		wantKey string
	}{
		{"Ctrl+7", "7"},
		{"Ctrl+Alt+F5", "F5"},
	} {
		p := config.Profile{Hotkey: tc.hotkey, Languages: []string{"ja", "en"}}
		form, err := settings.FormFromProfile(p, "windows")
		if err != nil {
			t.Fatalf("formFromProfile(%q): %v", tc.hotkey, err)
		}
		if form.Key != tc.wantKey {
			t.Errorf("formFromProfile(%q).Key = %q, want %q", tc.hotkey, form.Key, tc.wantKey)
		}
		sf := settings.Form{MaxChars: 4000, Profiles: []settings.ProfileForm{form}}
		cfg, err := sf.ToConfig(config.Default())
		if err != nil {
			t.Fatalf("toConfig for %q: %v", tc.hotkey, err)
		}
		if cfg.Profiles[0].Hotkey != tc.hotkey {
			t.Errorf("toConfig hotkey = %q, want %q", cfg.Profiles[0].Hotkey, tc.hotkey)
		}
	}
	f, _ := settings.FormFromProfile(config.Profile{Hotkey: "cmd+option+e", Target: "ko"}, "darwin")
	if !f.Win || !f.Alt || f.Key != "E" || f.Target != "ko" || f.HotkeyString() != "Alt+Win+E" {
		t.Errorf("macOS names: %+v %q", f, f.HotkeyString())
	}
	if _, err := settings.FormFromProfile(config.Profile{Hotkey: "nonsense"}, "windows"); err == nil {
		t.Error("an invalid hotkey must be an error")
	}
	// toConfig does not modify its base.
	base := config.Default()
	base.NormalizeProfiles()
	if _, err := validForm().ToConfig(base); err != nil || len(base.Profiles) != 1 {
		t.Errorf("toConfig changed the base or failed: %v %+v", err, base.Profiles)
	}
}

func TestSettingsLanguageChangeFollowedByUnrelatedSettingsSave(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	// Settings page opened: read state.
	_, body := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, body).State
	if st == nil || st.Revision == 0 {
		t.Fatalf("expected state with revision: %s", body)
	}

	// Another Settings page changes languages while settings page is open.
	changeSettingsLanguages(t, f.a, []string{"ja", "ko"})
	if !languages.SamePair(f.a.conf.Languages, []string{"ja", "ko"}) {
		t.Fatalf("Settings did not change language: %v", f.a.conf.Languages)
	}

	// Settings page saves an unrelated setting (max_chars = 2000).
	resp, body := s.do(t, "POST", "/api/save", map[string]any{
		"base_revision": st.Revision,
		"max_chars":     2000,
		"changed":       []string{"max_chars"},
	}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.Error != "" {
		t.Fatalf("save failed: %d %s", resp.StatusCode, body)
	}

	// The other page's language choice must be preserved in memory and on disk.
	if !languages.SamePair(f.a.conf.Languages, []string{"ja", "ko"}) {
		t.Fatalf("Settings languages overwritten in memory: %v", f.a.conf.Languages)
	}
	if !languages.SamePair(f.a.conf.Profiles[0].Languages, []string{"ja", "ko"}) {
		t.Fatalf("profile 0 languages overwritten in memory: %v", f.a.conf.Profiles[0].Languages)
	}
	if f.a.conf.MaxChars != 2000 {
		t.Fatalf("max_chars not saved: %d", f.a.conf.MaxChars)
	}

	c, err := config.Load(f.a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !languages.SamePair(c.Languages, []string{"ja", "ko"}) && !languages.SamePair(c.Profiles[0].Languages, []string{"ja", "ko"}) {
		t.Fatalf("Settings languages overwritten on disk: %+v", c)
	}
	if c.MaxChars != 2000 {
		t.Fatalf("max_chars not saved to disk: %d", c.MaxChars)
	}

	// Also verify direct applySettings with Changed specified preserves Settings languages.
	changeSettingsLanguages(t, f.a, []string{"ja", "zh-hans"})
	if _, err := f.a.applySettings(settings.Form{
		BaseRevision: f.a.confRevision,
		MaxChars:     3000,
		Changed:      []string{"max_chars"},
	}); err != nil {
		t.Fatalf("applySettings: %v", err)
	}
	if !languages.SamePair(f.a.conf.Languages, []string{"ja", "zh-hans"}) {
		t.Fatalf("direct applySettings overwrote Settings languages: %v", f.a.conf.Languages)
	}
}

func TestSettingsHotkeyChangeFollowedByUnrelatedSettingsSave(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	_, body := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, body).State
	if st == nil || st.Revision == 0 {
		t.Fatalf("expected state with revision: %s", body)
	}

	// Another Settings page changes hotkey while settings page is open.
	changeSettingsHotkey(t, f.a, "Ctrl+Shift+Y")
	if f.a.conf.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("Settings hotkey not changed: %s", f.a.conf.Hotkey)
	}

	// Settings page saves an unrelated setting (max_chars = 2500).
	resp, body := s.do(t, "POST", "/api/save", map[string]any{
		"base_revision": st.Revision,
		"max_chars":     2500,
		"changed":       []string{"max_chars"},
	}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.Error != "" {
		t.Fatalf("save failed: %d %s", resp.StatusCode, body)
	}

	if f.a.conf.Hotkey != "Ctrl+Shift+Y" || f.a.conf.Profiles[0].Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("Settings hotkey overwritten in memory: %s", f.a.conf.Hotkey)
	}
	if f.a.conf.MaxChars != 2500 {
		t.Fatalf("max_chars not saved: %d", f.a.conf.MaxChars)
	}

	c, err := config.Load(f.a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hotkey != "Ctrl+Shift+Y" || c.Profiles[0].Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("Settings hotkey overwritten on disk: %+v", c)
	}
	if c.MaxChars != 2500 {
		t.Fatalf("max_chars not saved to disk: %d", c.MaxChars)
	}
}

func TestSettingsAutoPasteChangeFollowedByUnrelatedSettingsSave(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	_, body := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, body).State
	if st == nil || st.Revision == 0 {
		t.Fatalf("expected state with revision: %s", body)
	}

	// Another Settings page toggles auto-paste while settings page is open.
	saveSettingsField(t, f.a, "auto_paste", !f.a.conf.AutoPaste)
	if !f.a.conf.AutoPaste {
		t.Fatal("Settings auto_paste not enabled")
	}

	// Settings page saves max_chars = 3500 without touching auto_paste.
	resp, body := s.do(t, "POST", "/api/save", map[string]any{
		"base_revision": st.Revision,
		"max_chars":     3500,
		"changed":       []string{"max_chars"},
	}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.Error != "" {
		t.Fatalf("save failed: %d %s", resp.StatusCode, body)
	}

	if !f.a.conf.AutoPaste {
		t.Fatal("Settings auto_paste overwritten in memory")
	}
	if f.a.conf.MaxChars != 3500 {
		t.Fatalf("max_chars not saved: %d", f.a.conf.MaxChars)
	}

	c, err := config.Load(f.a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !c.AutoPaste {
		t.Fatal("Settings auto_paste overwritten on disk")
	}
	if c.MaxChars != 3500 {
		t.Fatalf("max_chars not saved to disk: %d", c.MaxChars)
	}
}

func TestSettingsNotifyOnCompleteChangeFollowedByUnrelatedSettingsSave(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	_, body := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, body).State
	if st == nil || st.Revision == 0 {
		t.Fatalf("expected state with revision: %s", body)
	}

	// Another Settings page toggles notify_on_complete while settings page is open.
	saveSettingsField(t, f.a, "notify_on_complete", !f.a.conf.NotifyOnComplete)
	if !f.a.conf.NotifyOnComplete {
		t.Fatal("Settings notify_on_complete not enabled")
	}

	// Settings page saves max_chars = 3500 without touching notify_on_complete.
	resp, body := s.do(t, "POST", "/api/save", map[string]any{
		"base_revision": st.Revision,
		"max_chars":     3500,
		"changed":       []string{"max_chars"},
	}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.Error != "" {
		t.Fatalf("save failed: %d %s", resp.StatusCode, body)
	}

	if !f.a.conf.NotifyOnComplete {
		t.Fatal("Settings notify_on_complete overwritten in memory")
	}
	if f.a.conf.MaxChars != 3500 {
		t.Fatalf("max_chars not saved: %d", f.a.conf.MaxChars)
	}

	c, err := config.Load(f.a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !c.NotifyOnComplete {
		t.Fatal("Settings notify_on_complete overwritten on disk")
	}
	if c.MaxChars != 3500 {
		t.Fatalf("max_chars not saved to disk: %d", c.MaxChars)
	}
}

func TestOverlappingEditsConflictHandling(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	// Case A: Another Settings page changes languages, page attempts to save stale profiles.
	_, body := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, body).State
	if st == nil || st.Revision == 0 {
		t.Fatalf("expected state: %s", body)
	}

	changeSettingsLanguages(t, f.a, []string{"ja", "ko"})

	resp, body := s.do(t, "POST", "/api/save", map[string]any{
		"base_revision": st.Revision,
		"profiles": []settings.ProfileForm{
			{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "en"}},
		},
		"changed": []string{"profiles"},
	}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 409 {
		t.Fatalf("overlapping edit must return 409 Conflict, got %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(r.Error, "settings were changed") {
		t.Fatalf("conflict error message unexpected: %q", r.Error)
	}
	if r.State == nil || !languages.SamePair(r.State.Profiles[0].Languages, []string{"ja", "ko"}) {
		t.Fatalf("conflict response must return current state: %+v", r.State)
	}
	// The other page's language stayed intact.
	if !languages.SamePair(f.a.conf.Languages, []string{"ja", "ko"}) {
		t.Fatalf("conf.Languages was corrupted on conflict: %v", f.a.conf.Languages)
	}

	// Resaving with updated revision succeeds.
	resp, body = s.do(t, "POST", "/api/save", map[string]any{
		"base_revision": r.State.Revision,
		"profiles": []settings.ProfileForm{
			{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "ko"}},
		},
		"changed": []string{"profiles"},
	}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("resave with current revision failed: %d %s", resp.StatusCode, body)
	}

	// Case B: Another Settings page changes auto-paste, page attempts to save stale auto-paste.
	_, body = s.do(t, "GET", "/api/state", nil, nil)
	st2 := decodeAPI(t, body).State

	saveSettingsField(t, f.a, "auto_paste", !f.a.conf.AutoPaste) // toggles auto-paste
	curAutoPaste := f.a.conf.AutoPaste

	resp, body = s.do(t, "POST", "/api/save", map[string]any{
		"base_revision": st2.Revision,
		"auto_paste":    !curAutoPaste,
		"changed":       []string{"auto_paste"},
	}, nil)
	r2 := decodeAPI(t, body)
	if resp.StatusCode != 409 {
		t.Fatalf("overlapping auto_paste edit must return 409 Conflict, got %d %s", resp.StatusCode, body)
	}
	if f.a.conf.AutoPaste != curAutoPaste {
		t.Fatal("auto_paste was overwritten on conflict")
	}
	if r2.State == nil || r2.State.AutoPaste != curAutoPaste {
		t.Fatalf("conflict state auto_paste mismatch: %+v", r2.State)
	}

	// Case B2: Another Settings page changes notify_on_complete, page attempts to save stale notify_on_complete.
	_, body = s.do(t, "GET", "/api/state", nil, nil)
	st3 := decodeAPI(t, body).State

	saveSettingsField(t, f.a, "notify_on_complete", !f.a.conf.NotifyOnComplete) // toggles notify_on_complete
	curNotify := f.a.conf.NotifyOnComplete

	resp, body = s.do(t, "POST", "/api/save", map[string]any{
		"base_revision":      st3.Revision,
		"notify_on_complete": !curNotify,
		"changed":            []string{"notify_on_complete"},
	}, nil)
	r3 := decodeAPI(t, body)
	if resp.StatusCode != 409 {
		t.Fatalf("overlapping notify_on_complete edit must return 409 Conflict, got %d %s", resp.StatusCode, body)
	}
	if f.a.conf.NotifyOnComplete != curNotify {
		t.Fatal("notify_on_complete was overwritten on conflict")
	}
	if r3.State == nil || r3.State.NotifyOnComplete != curNotify {
		t.Fatalf("conflict state notify_on_complete mismatch: %+v", r3.State)
	}

	// Case C: Direct applySettings call with stale BaseRevision on conflicting field.
	form := f.a.settingsState().Form
	changeSettingsLanguages(t, f.a, []string{"ja", "zh-hans"})
	form.Changed = []string{"profiles"}
	_, err := f.a.applySettings(form)
	if !errors.Is(err, settings.ErrConflict) {
		t.Fatalf("applySettings with stale BaseRevision must return ErrConflict, got %v", err)
	}
	if !languages.SamePair(f.a.conf.Languages, []string{"ja", "zh-hans"}) {
		t.Fatalf("Settings change lost on direct applySettings conflict: %v", f.a.conf.Languages)
	}
}

// These helpers edit the current Settings form, preserving unrelated profiles.
func changeSettingsHotkey(t *testing.T, a *app, spec string) error {
	t.Helper()
	form := a.settingsState().Form
	p := a.conf.Profiles[0]
	p.Hotkey = spec
	f, err := settings.FormFromProfile(p, "windows")
	if err != nil {
		t.Fatal(err)
	}
	form.Profiles[0] = f
	form.Changed = []string{"profiles"}
	_, err = a.applySettings(form)
	return err
}

func changeSettingsLanguages(t *testing.T, a *app, pair []string) {
	t.Helper()
	form := a.settingsState().Form
	form.Profiles[0].Languages = pair
	form.Changed = []string{"profiles"}
	if _, err := a.applySettings(form); err != nil {
		t.Fatal(err)
	}
}

func saveSettingsField(t *testing.T, a *app, field string, value bool) {
	t.Helper()
	form := a.settingsState().Form
	form.Changed = []string{field}
	switch field {
	case "auto_paste":
		form.AutoPaste = value
	case "notify_on_complete":
		form.NotifyOnComplete = value
	default:
		t.Fatalf("unexpected field %q", field)
	}
	if _, err := a.applySettings(form); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsAllHotkeysInactive(t *testing.T) {
	f := newSettingsFixture(t)
	f.a.closeHotkeys()
	f.a.conf.Profiles = []config.Profile{{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}}, {Hotkey: "Ctrl+Alt+K", Target: "ko"}}
	f.a.conf.NormalizeProfiles()
	f.taken["CTRL+ALT+E"], f.taken["CTRL+ALT+K"] = true, true
	f.a.registerStartupHotkey()
	if f.a.anyHotkeyActive() {
		t.Fatal("all registrations must fail")
	}
	item := f.a.buildMenu()[0]
	if item.ID != cmdSettings || item.Disabled {
		t.Fatalf("Settings must be first and enabled: %+v", item)
	}
	f.a.handleCommand(item.ID)
	if len(f.opened) != 1 {
		t.Fatal("Settings did not open")
	}
	_, body := testServer{f.a.settings}.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, body).State
	if st == nil || len(st.Inactive) != 2 || !strings.Contains(st.Inactive[0], "Ctrl+Alt+E") || !strings.Contains(st.Inactive[1], "Ctrl+Alt+K") {
		t.Fatalf("inactive state: %s", body)
	}
	if !st.NoActiveHotkey {
		t.Error("expected NoActiveHotkey=true when all hotkeys failed to register")
	}
	_, js := testServer{f.a.settings}.do(t, "GET", "/app.js", nil, nil)
	if !strings.Contains(string(js), "Choose another hotkey below and click Save") {
		t.Fatal("inactive banner must explain recovery")
	}
	// After the keys become free, an unchanged Save retries registration.
	f.taken["CTRL+ALT+E"], f.taken["CTRL+ALT+K"] = false, false
	resp, body := testServer{f.a.settings}.do(t, "POST", "/api/save", st.Form, nil)
	rep := decodeAPI(t, body)
	if resp.StatusCode != 200 || rep.State == nil || len(rep.State.Inactive) != 0 || !f.a.anyHotkeyActive() {
		t.Fatalf("retry: %d %s", resp.StatusCode, body)
	}
	if rep.State.NoActiveHotkey {
		t.Error("expected NoActiveHotkey=false after retry succeeded")
	}
}

func TestSettingsStateNoActiveHotkey(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	// Initially startup hotkey is registered and active
	st := f.a.settingsState()
	if st.NoActiveHotkey {
		t.Error("expected NoActiveHotkey=false when hotkey is active")
	}
	_, body := s.do(t, "GET", "/api/state", nil, nil)
	apiState := decodeAPI(t, body).State
	if apiState == nil || apiState.NoActiveHotkey {
		t.Errorf("API state NoActiveHotkey should be false, got %+v", apiState)
	}

	// Make hotkey inactive
	f.a.closeHotkeys()
	f.a.hotkey = noHotkey{}
	st = f.a.settingsState()
	if !st.NoActiveHotkey {
		t.Error("expected NoActiveHotkey=true when no hotkey is active")
	}
	_, body = s.do(t, "GET", "/api/state", nil, nil)
	apiState = decodeAPI(t, body).State
	if apiState == nil || !apiState.NoActiveHotkey {
		t.Errorf("API state NoActiveHotkey should be true, got %+v", apiState)
	}
}

func TestOpenSettingsOpenerFailureGuidanceAndIdleClose(t *testing.T) {
	f := newSettingsFixture(t)
	tray := f.a.tray.(*fakeTray)
	tray.notes = nil

	f.a.settingsIdle = 50 * time.Millisecond
	f.a.openURL = func(target string) error {
		return errors.New("browser execution failed")
	}

	// Must not panic on opener failure
	f.a.openSettings()

	note := tray.lastNote()
	if !strings.Contains(note, "Could not open Settings in the browser") {
		t.Fatalf("expected browser failure message, got %q", note)
	}
	if !strings.Contains(note, "Open config.json") || !strings.Contains(note, "restart") {
		t.Fatalf("expected actionable guidance to open config.json and restart, got %q", note)
	}
	if strings.Contains(note, "http://") || strings.Contains(note, "?t=") {
		t.Fatalf("notification must not leak URL or token: %q", note)
	}

	// Server is initially running
	if f.a.settings == nil || !f.a.settings.Running() {
		t.Fatal("expected settings server to be running initially")
	}

	// Idle timer must close the server
	deadline := time.Now().Add(500 * time.Millisecond)
	for f.a.settings.Running() {
		if time.Now().After(deadline) {
			t.Fatal("settings server was left running forever after opener failure")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPureHotkeyRetryDoesNotTouchConfigOrRevisions(t *testing.T) {
	f := newSettingsFixture(t)
	confPath := filepath.Join(f.a.dir, "config.json")
	bakPath := filepath.Join(f.a.dir, "config.json.bak")
	_ = os.Remove(bakPath)

	initialBytes, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	initialInfo, err := os.Stat(confPath)
	if err != nil {
		t.Fatal(err)
	}

	// Hotkey inactive at start
	f.a.closeHotkeys()
	f.a.hotkey = noHotkey{}
	f.a.confRevision = 5
	f.a.profilesRev = 5

	form := settings.Form{
		BaseRevision: 5,
		Changed:      []string{"profiles"},
		Profiles: []settings.ProfileForm{
			{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "en"}},
		},
	}

	var regCount int
	f.a.newHotkey = func(spec string) (platform.Hotkey, error) {
		regCount++
		return fakeHotkey{spec}, nil
	}

	warnings, err := f.a.applySettings(form)
	if err != nil {
		t.Fatalf("pure retry failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if !f.a.anyHotkeyActive() {
		t.Fatal("expected hotkey to be active after retry")
	}
	if regCount == 0 {
		t.Fatal("expected registration to be attempted on retry")
	}

	// Revisions must NOT change on pure retry
	if f.a.confRevision != 5 {
		t.Errorf("confRevision bumped from 5 to %d", f.a.confRevision)
	}
	if f.a.profilesRev != 5 {
		t.Errorf("profilesRev bumped from 5 to %d", f.a.profilesRev)
	}

	// config.json must NOT be touched
	currentBytes, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(currentBytes) != string(initialBytes) {
		t.Errorf("config.json changed: got %s, want %s", string(currentBytes), string(initialBytes))
	}
	currentInfo, err := os.Stat(confPath)
	if err != nil {
		t.Fatal(err)
	}
	if !currentInfo.ModTime().Equal(initialInfo.ModTime()) {
		t.Errorf("config.json mtime changed: got %v, want %v", currentInfo.ModTime(), initialInfo.ModTime())
	}
	if _, err := os.Stat(bakPath); !os.IsNotExist(err) {
		t.Errorf("config.json.bak was created: %v", err)
	}

	// Retry on already-working registrations must do nothing
	regCount = 0
	f.a.newHotkey = func(spec string) (platform.Hotkey, error) {
		regCount++
		t.Error("newHotkey must not be called when registrations are already active")
		return nil, errors.New("must not be called")
	}
	warnings, err = f.a.applySettings(form)
	if err != nil {
		t.Fatalf("unexpected error on active retry: %v", err)
	}
	if regCount != 0 {
		t.Errorf("expected 0 registration calls, got %d", regCount)
	}
	if f.a.confRevision != 5 || f.a.profilesRev != 5 {
		t.Errorf("revisions changed on active retry: confRev=%d, profilesRev=%d", f.a.confRevision, f.a.profilesRev)
	}
}

func TestSettingsProfileInputSaveAndState(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	_, body := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, body).State
	if st == nil || len(st.Profiles) != 1 || st.Profiles[0].Input != config.InputSelection {
		t.Fatalf("initial state input: %+v", st)
	}

	saveForm := settings.Form{
		BaseRevision: st.Revision,
		MaxChars:     4000,
		Changed:      []string{"profiles"},
		Profiles: []settings.ProfileForm{
			{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "en"}, Input: config.InputClipboard},
		},
	}
	resp, body := s.do(t, "POST", "/api/save", saveForm, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.Error != "" || r.State == nil {
		t.Fatalf("save clipboard input: %d %s", resp.StatusCode, body)
	}
	if r.State.Profiles[0].Input != config.InputClipboard || f.a.conf.Profiles[0].Input != config.InputClipboard {
		t.Fatalf("input not applied: state %q, conf %q", r.State.Profiles[0].Input, f.a.conf.Profiles[0].Input)
	}
	raw, err := os.ReadFile(filepath.Join(f.a.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"input":"clipboard"`) {
		t.Fatalf("config.json did not save the input mode:\n%s", raw)
	}

	// An unknown value is rejected and nothing changes.
	bad := saveForm
	bad.BaseRevision = r.State.Revision
	bad.Profiles = []settings.ProfileForm{{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "en"}, Input: "mouse"}}
	resp, body = s.do(t, "POST", "/api/save", bad, nil)
	if resp.StatusCode == 200 || !strings.Contains(decodeAPI(t, body).Error, "input") {
		t.Fatalf("an unknown input must be rejected: %d %s", resp.StatusCode, body)
	}
	if f.a.conf.Profiles[0].Input != config.InputClipboard {
		t.Fatal("a rejected save changed the input mode")
	}

	// Saving the same settings again changes nothing (hotkeys are not re-registered).
	before := f.reg.Load()
	same := saveForm
	same.BaseRevision = r.State.Revision
	if resp, _ := s.do(t, "POST", "/api/save", same, nil); resp.StatusCode != 200 {
		t.Fatalf("second save: %d", resp.StatusCode)
	}
	if f.reg.Load() != before {
		t.Error("an unchanged save re-registered hotkeys")
	}
}

func TestSettingsProfileOutputSaveAndConflict(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	// Initial state: profile 0 output is replace.
	_, body := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, body).State
	if st == nil || len(st.Profiles) != 1 || st.Profiles[0].Output != config.OutputReplace {
		t.Fatalf("initial state output = %q, want replace", st.Profiles[0].Output)
	}

	// Save profile with output: "bilingual".
	saveForm := settings.Form{
		BaseRevision: st.Revision,
		MaxChars:     4000,
		Changed:      []string{"profiles"},
		Profiles: []settings.ProfileForm{
			{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "en"}, Output: config.OutputBilingual},
		},
	}
	resp, body := s.do(t, "POST", "/api/save", saveForm, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.Error != "" || r.State == nil {
		t.Fatalf("save bilingual output: %d %s", resp.StatusCode, body)
	}
	if r.State.Profiles[0].Output != config.OutputBilingual {
		t.Fatalf("state profile 0 output = %q, want bilingual", r.State.Profiles[0].Output)
	}
	if f.a.conf.Profiles[0].Output != config.OutputBilingual {
		t.Fatalf("conf profile 0 output = %q, want bilingual", f.a.conf.Profiles[0].Output)
	}

	// Verify config.json on disk.
	raw, err := os.ReadFile(filepath.Join(f.a.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"output":"bilingual"`) {
		t.Fatalf("config.json did not save bilingual output:\n%s", raw)
	}

	// Second save of the exact same settings changes nothing (hotkeys not re-registered).
	before := f.reg.Load()
	saveForm.BaseRevision = r.State.Revision
	resp, _ = s.do(t, "POST", "/api/save", saveForm, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("second save: %d", resp.StatusCode)
	}
	if f.reg.Load() != before {
		t.Error("an unchanged save re-registered hotkeys")
	}

	// Overlapping edit conflict: app revision moves ahead (another editor changed the languages).
	changeSettingsLanguages(t, f.a, []string{"ja", "ko"})

	// Attempt save with stale base_revision.
	staleForm := settings.Form{
		BaseRevision: saveForm.BaseRevision, // older than f.a.profilesRev
		Changed:      []string{"profiles"},
		Profiles: []settings.ProfileForm{
			{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "en"}, Output: config.OutputReplace},
		},
	}
	resp, body = s.do(t, "POST", "/api/save", staleForm, nil)
	r = decodeAPI(t, body)
	if resp.StatusCode != 409 {
		t.Fatalf("stale profile output save must return 409 Conflict, got %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(r.Error, "settings were changed") {
		t.Fatalf("unexpected error message: %q", r.Error)
	}
}
