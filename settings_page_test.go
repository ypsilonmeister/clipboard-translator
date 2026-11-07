package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	a.newHotkey = func(spec string) (Hotkey, error) {
		f.reg.Add(1)
		if f.taken[strings.ToUpper(spec)] {
			return nil, errors.New("already registered")
		}
		return fakeHotkey{spec}, nil
	}
	a.openURL = func(u string) { f.opened = append(f.opened, u) }
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

func (f *settingsFixture) server(t *testing.T) *settingsServer {
	t.Helper()
	s, err := startSettingsServer(f.a.askSettings, f.a.dl, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.a.settings = s
	return s
}

func (s *settingsServer) do(t *testing.T, method, path string, body any, edit func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://"+s.host+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(settingsTokenHeader, s.token)
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

func decodeAPI(t *testing.T, b []byte) apiResponse {
	t.Helper()
	var r apiResponse
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("not JSON: %s", b)
	}
	return r
}

func validForm() settingsForm {
	return settingsForm{AutoPaste: true, MaxChars: 4000, Profiles: []profileForm{
		{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "en"}},
		{Ctrl: true, Alt: true, Key: "K", Target: "ko"},
	}}
}

func TestSettingsServerRefusesWhatItShould(t *testing.T) {
	s := newSettingsFixture(t).server(t)
	if !strings.HasPrefix(s.URL(), "http://127.0.0.1:") || !strings.Contains(s.URL(), "/?t="+s.token) {
		t.Fatalf("the page must be on 127.0.0.1 and carry the token: %s", s.URL())
	}
	if len(s.token) < 32 {
		t.Errorf("token too short: %q", s.token)
	}
	port := strings.Split(s.host, ":")[1]
	cases := []struct {
		name   string
		method string
		path   string
		body   any
		edit   func(*http.Request)
		want   int
	}{
		{"no token", "GET", "/api/state", nil, func(r *http.Request) { r.Header.Del(settingsTokenHeader) }, 403},
		{"wrong token", "GET", "/api/state", nil, func(r *http.Request) { r.Header.Set(settingsTokenHeader, "nope") }, 403},
		{"page without token", "GET", "/", nil, func(r *http.Request) { r.Header.Del(settingsTokenHeader) }, 403},
		{"asset without token", "GET", "/app.js", nil, func(r *http.Request) { r.Header.Del(settingsTokenHeader) }, 403},
		{"rebinding Host", "GET", "/api/state", nil, func(r *http.Request) { r.Host = "evil.example:" + port }, 403},
		{"localhost is not our Host", "GET", "/api/state", nil, func(r *http.Request) { r.Host = "localhost:" + port }, 403},
		{"cross-origin save", "POST", "/api/save", validForm(), func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		{"cross-site fetch", "POST", "/api/save", validForm(), func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"form post", "POST", "/api/save", validForm(), func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"GET on save", "GET", "/api/save", nil, nil, 405},
		{"POST on state", "POST", "/api/state", map[string]int{}, nil, 405},
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
	resp, body := s.do(t, "GET", "/?t="+s.token, nil, func(r *http.Request) { r.Header.Del(settingsTokenHeader) })
	if resp.StatusCode != 200 || !strings.Contains(string(body), "app.js?t="+s.token) || strings.Contains(string(body), "{{TOKEN}}") {
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
		r, b := s.do(t, "GET", p+"?t="+s.token, nil, func(r *http.Request) { r.Header.Del(settingsTokenHeader) })
		if r.StatusCode != 200 || len(b) < 500 {
			t.Errorf("%s: %d, %d bytes", p, r.StatusCode, len(b))
		}
	}
	// An author rule like display:flex beats the hidden attribute; this one restores it
	// (the busy bar once stayed on screen because of that).
	css, _ := os.ReadFile("settings_web/app.css")
	if !strings.Contains(string(css), "[hidden] { display: none !important; }") {
		t.Error("app.css must make the hidden attribute win over author display rules")
	}
	js, _ := os.ReadFile("settings_web/app.js")
	for _, bad := range []string{"innerHTML", "outerHTML", "document.write", "eval("} {
		if strings.Contains(string(js), bad) {
			t.Errorf("app.js uses %s", bad)
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
	c, err := loadConfig(f.a.dir)
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

	bad := map[string]settingsForm{
		"no profiles":    {},
		"no modifier":    {MaxChars: 4000, Profiles: []profileForm{{Key: "E", Languages: []string{"ja", "en"}}}},
		"not a letter":   {MaxChars: 4000, Profiles: []profileForm{{Ctrl: true, Key: "1", Languages: []string{"ja", "en"}}}},
		"same script":    {MaxChars: 4000, Profiles: []profileForm{{Ctrl: true, Key: "E", Languages: []string{"en", "de"}}}},
		"unknown target": {MaxChars: 4000, Profiles: []profileForm{{Ctrl: true, Key: "E", Target: "xx"}}},
		"duplicate key": {MaxChars: 4000, Profiles: []profileForm{
			{Ctrl: true, Alt: true, Key: "E", Languages: []string{"ja", "en"}},
			{Alt: true, Ctrl: true, Key: "e", Target: "ko"}}},
		"both modes": {MaxChars: 4000, Profiles: []profileForm{{Ctrl: true, Key: "E", Languages: []string{"ja", "en"}, Target: "ko"}}},
	}
	for name, form := range bad {
		resp, body := s.do(t, "POST", "/api/save", form, nil)
		r := decodeAPI(t, body)
		if resp.StatusCode != 422 || r.Error == "" {
			t.Errorf("%s: status %d, body %s", name, resp.StatusCode, body)
		}
	}
	// The error names the profile so the page can show it there.
	_, body := s.do(t, "POST", "/api/save", settingsForm{MaxChars: 4000, Profiles: []profileForm{
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
	resp, body = s.do(t, "POST", "/api/save", settingsForm{MaxChars: 4000, Profiles: []profileForm{
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
	// On Linux and macOS one hotkey is registered. A config with more loads there,
	// so the page must be able to save it and say which hotkeys are not active.
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
	a.newHotkey = func(spec string) (Hotkey, error) { return fakeHotkey{spec}, nil }
	a.registerStartupHotkey()

	// The loop is busy: the request times out and the page is told so.
	form := validForm()
	req := newSettingsRequest(&form, 30*time.Millisecond)
	rep := a.askSettings(req)
	if !errors.Is(rep.err, errSettingsBusy) {
		t.Fatalf("want busy, got %+v", rep)
	}
	// When the loop gets to it later, it must be dropped, not applied.
	time.Sleep(40 * time.Millisecond)
	a.handleSettingsRequest(<-a.settingsReqs)
	if len(a.conf.Profiles) != 1 || a.conf.AutoPaste {
		t.Fatalf("a request the page was told had failed was applied: %+v", a.conf)
	}
	if got := <-req.reply; !errors.Is(got.err, errSettingsBusy) {
		t.Errorf("the dropped request should be answered: %+v", got)
	}
}

func TestSettingsServerClosesWhenIdleOrToldTo(t *testing.T) {
	f := newSettingsFixture(t)
	s, err := startSettingsServer(f.a.askSettings, f.a.dl, 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Running() {
		t.Fatal("should be running")
	}
	time.Sleep(300 * time.Millisecond)
	if s.Running() {
		t.Fatal("an idle server must close itself")
	}
	if _, err := http.Get("http://" + s.host + "/"); err == nil {
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
	var item *MenuItem
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
	for _, hk := range []string{"Ctrl+Alt+E", "Ctrl+Shift+Y", "Alt+K", "Ctrl+Alt+Shift+Win+Q"} {
		f, err := formFromProfile(Profile{Hotkey: hk, Languages: []string{"ja", "en"}}, "windows")
		if err != nil {
			t.Fatal(err)
		}
		if got := f.hotkeyString(); got != hk {
			t.Errorf("%q -> %+v -> %q", hk, f, got)
		}
	}
	f, _ := formFromProfile(Profile{Hotkey: "cmd+option+e", Target: "ko"}, "darwin")
	if !f.Win || !f.Alt || f.Key != "E" || f.Target != "ko" || f.hotkeyString() != "Alt+Win+E" {
		t.Errorf("macOS names: %+v %q", f, f.hotkeyString())
	}
	if _, err := formFromProfile(Profile{Hotkey: "nonsense"}, "windows"); err == nil {
		t.Error("an invalid hotkey must be an error")
	}
	// toConfig does not modify its base.
	base := defaultConfig()
	base.normalizeProfiles()
	if _, err := validForm().toConfig(base); err != nil || len(base.Profiles) != 1 {
		t.Errorf("toConfig changed the base or failed: %v %+v", err, base.Profiles)
	}
}
