package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func unloadMenuEnabled(t *testing.T, a *app) bool {
	t.Helper()
	for _, item := range a.buildMenu() {
		if item.ID == cmdUnloadModel {
			return !item.Disabled
		}
	}
	t.Fatal("Unload model menu item missing")
	return false
}

func TestUnloadModelAndReloadTranslation(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if !unloadMenuEnabled(t, a) {
		t.Fatal("running idle server must allow unload")
	}
	for _, busy := range []string{"translation", "switch", "startup", "settings"} {
		a.translating = busy == "translation"
		a.serverWork = 0
		if busy == "switch" {
			a.serverWork = 1
		}
		a.sup.starting = busy == "startup"
		a.settingsServing = busy == "settings"
		if unloadMenuEnabled(t, a) {
			t.Fatalf("unload enabled during %s", busy)
		}
		a.handleCommand(cmdUnloadModel)
		if !a.sup.Running() {
			t.Fatalf("unloaded during %s", busy)
		}
	}
	a.translating, a.sup.starting, a.settingsServing, a.serverWork = false, false, false, 0
	notes := len(tray.notes)
	a.handleCommand(cmdUnloadModel)
	if a.sup.Running() || a.sup.Exited() != nil || !a.modelUnloaded || unloadMenuEnabled(t, a) {
		t.Fatal("manual unload must stop and unwatch the child and disable the menu")
	}
	if !strings.Contains(tray.status, "model unloaded") || !strings.Contains(a.readyTip, "next hotkey") {
		t.Fatalf("status=%q tip=%q", tray.status, a.readyTip)
	}
	if healthy(a) {
		t.Fatal("unloaded server still answers health")
	}
	a.handleCommand(cmdUnloadModel) // idempotent
	cb.text = "こんにちは"
	a.capture = func(Clipboard, uintptr) (string, bool, error) { return cb.text, true, nil }
	a.startTranslation(0)
	if !a.sup.Running() || a.modelUnloaded || !a.translating {
		t.Fatal("hotkey did not reload and translate")
	}
	select {
	case res := <-a.results:
		a.handleResult(res)
	case <-time.After(5 * time.Second):
		t.Fatal("translation did not finish")
	}
	if cb.text != fakeTranslation {
		t.Fatalf("clipboard=%q", cb.text)
	}
	if a.limiter.Attempt() != 0 {
		t.Fatal("intentional unload consumed restart budget")
	}
	for _, note := range tray.notes[notes:] {
		if strings.Contains(note, "stopped") {
			t.Fatalf("intentional unload treated as crash: %s", note)
		}
	}
	found := false
	for _, status := range tray.statuses {
		found = found || strings.Contains(status, "loading model")
	}
	if !found {
		t.Fatal("reload did not show loading status")
	}
}

func TestIdleUnloadTimer(t *testing.T) {
	a, _, _ := newTestApp(t)
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	a.conf.IdleUnloadMinutes = 2
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2*time.Minute - time.Nanosecond)
	a.tickIdleUnload()
	if !a.sup.Running() {
		t.Fatal("unloaded before deadline")
	}
	now = now.Add(time.Nanosecond)
	for _, busy := range []string{"translation", "switch", "startup", "settings"} {
		a.translating = busy == "translation"
		a.serverWork = 0
		if busy == "switch" {
			a.serverWork = 1
		}
		a.sup.starting = busy == "startup"
		a.settingsServing = busy == "settings"
		a.tickIdleUnload()
		if !a.sup.Running() {
			t.Fatalf("idle unload during %s", busy)
		}
	}
	a.translating, a.sup.starting, a.settingsServing, a.serverWork = false, false, false, 0
	// A completion starts a fresh idle interval, even if it failed.
	a.handleResult(result{generation: a.generation, err: errors.New("test failure")})
	a.tickIdleUnload()
	if !a.sup.Running() {
		t.Fatal("completion did not reset idle interval")
	}
	now = now.Add(2 * time.Minute)
	a.tickIdleUnload()
	if a.sup.Running() || !a.modelUnloaded {
		t.Fatal("did not unload at deadline")
	}
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.tickIdleUnload()
	if !a.sup.Running() {
		t.Fatal("server start did not reset timer")
	}
	a.conf.IdleUnloadMinutes = 0
	now = now.Add(48 * time.Hour)
	a.tickIdleUnload()
	if !a.sup.Running() {
		t.Fatal("zero must disable idle unload")
	}
}

type unloadIdleHotkey struct {
	tray  *fakeTray
	polls int
}

func (h *unloadIdleHotkey) Pressed() (bool, error) {
	h.polls++
	if h.polls == 3 {
		h.tray.quit = true
	}
	return false, nil
}
func (*unloadIdleHotkey) Close() error { return nil }

func TestServeKeepsIntentionalUnloadIdle(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.unloadModel()
	a.hotkey = &unloadIdleHotkey{tray: tray}
	a.serve(cb)
	if a.sup.Running() || a.limiter.Attempt() != 0 {
		t.Fatal("serve recovered an intentional stop")
	}
	for _, note := range tray.notes {
		if strings.Contains(note, "stopped") {
			t.Fatalf("unexpected crash notification: %s", note)
		}
	}
}

func TestIdleUnloadConfigValidation(t *testing.T) {
	for _, minutes := range []int{-1, 1441, 0, 1440} {
		t.Run(fmt.Sprint(minutes), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(fmt.Sprintf(`{"idle_unload_minutes":%d}`, minutes)), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := loadConfig(dir)
			invalid := minutes < 0 || minutes > 1440
			if (err != nil) != invalid {
				t.Fatalf("config minutes=%d err=%v", minutes, err)
			}
			if !invalid && c.IdleUnloadMinutes != minutes {
				t.Fatal("config value lost")
			}
			base := defaultConfig()
			base.normalizeProfiles()
			_, err = (settingsForm{IdleUnloadMinutes: minutes, Changed: []string{"idle_unload_minutes"}}).toConfig(base)
			if (err != nil) != invalid {
				t.Fatalf("settings minutes=%d err=%v", minutes, err)
			}
		})
	}
}

func TestIdleUnloadSettingsRoundTripAndConflicts(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)
	path := filepath.Join(f.a.dir, "config.json")
	original := []byte(`{"future_setting":{"keep":true},"max_chars":4000}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	_, body := s.do(t, "GET", "/api/state", nil, nil)
	old := decodeAPI(t, body).State
	if old.IdleUnloadMinutes != 0 {
		t.Fatal("default must be zero")
	}
	// An unrelated tray edit must survive a partial page save based on an older revision.
	f.a.handleCommand(cmdAutoPaste)
	resp, body := s.do(t, "POST", "/api/save", map[string]any{"base_revision": old.Revision, "idle_unload_minutes": 5}, nil)
	st := decodeAPI(t, body).State
	if resp.StatusCode != 200 || st == nil || st.IdleUnloadMinutes != 5 || !st.AutoPaste {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	resp, body = s.do(t, "POST", "/api/save", map[string]any{"base_revision": old.Revision, "idle_unload_minutes": 6, "changed": []string{"idle_unload_minutes"}}, nil)
	if resp.StatusCode != 409 || decodeAPI(t, body).State.IdleUnloadMinutes != 5 {
		t.Fatalf("conflict: %d %s", resp.StatusCode, body)
	}
	// A stale unrelated field can still be saved without replacing the new idle timeout.
	resp, body = s.do(t, "POST", "/api/save", map[string]any{"base_revision": old.Revision, "max_chars": 5000, "idle_unload_minutes": 0, "changed": []string{"max_chars"}}, nil)
	if resp.StatusCode != 200 || decodeAPI(t, body).State.IdleUnloadMinutes != 5 {
		t.Fatalf("partial: %d %s", resp.StatusCode, body)
	}
	for _, minutes := range []int{-1, 1441} {
		resp, body = s.do(t, "POST", "/api/save", map[string]any{"idle_unload_minutes": minutes}, nil)
		if resp.StatusCode != 422 {
			t.Fatalf("invalid: %d %s", resp.StatusCode, body)
		}
	}
	_, body = s.do(t, "GET", "/api/state", nil, nil)
	revision := decodeAPI(t, body).State.Revision
	resp, body = s.do(t, "POST", "/api/save", map[string]any{"base_revision": revision, "idle_unload_minutes": 10, "changed": []string{"idle_unload_minutes"}}, nil)
	if resp.StatusCode != 200 || decodeAPI(t, body).State.IdleUnloadMinutes != 10 {
		t.Fatalf("resave: %d %s", resp.StatusCode, body)
	}
	c, err := loadConfig(f.a.dir)
	if err != nil || c.IdleUnloadMinutes != 10 || c.MaxChars != 5000 || !c.AutoPaste {
		t.Fatalf("saved config: %+v %v", c, err)
	}
	for _, name := range []string{path, path + ".bak"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(b, &obj); err != nil {
			t.Fatal(err)
		}
		if string(obj["future_setting"]) != `{"keep":true}` {
			t.Fatalf("unknown field lost in %s: %s", name, b)
		}
	}
}

func TestIdleUnloadSettingsResetTimer(t *testing.T) {
	a, _, _ := newTestApp(t)
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	a.conf.IdleUnloadMinutes = 1
	a.resetIdleTimer()
	now = now.Add(time.Hour)
	form := settingsForm{IdleUnloadMinutes: 2, Changed: []string{"idle_unload_minutes"}}
	if _, err := a.applySettings(form); err != nil {
		t.Fatal(err)
	}
	if !a.idleSince.Equal(now) {
		t.Fatal("idle setting change did not reset timer")
	}
}

func TestUnloadReloadFailureStaysUnloaded(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.unloadModel()
	a.paths.server = filepath.Join(a.dir, "missing-server")
	a.paths.fallback = a.paths.server
	cb.text = "こんにちは"
	a.capture = func(Clipboard, uintptr) (string, bool, error) { return cb.text, true, nil }
	var failure string
	a.showErr = func(msg string) { failure = msg }
	a.startTranslation(0)
	if failure == "" || !strings.Contains(failure, "server.log") {
		t.Fatalf("reload error not actionable: %q", failure)
	}
	if a.translating || a.sup.Running() || !a.modelUnloaded || !strings.Contains(tray.status, "model unloaded") {
		t.Fatal("failed reload must stay unloaded for retry")
	}
	if cb.text != "こんにちは" || a.limiter.Attempt() != 0 || a.serverWork != 0 {
		t.Fatal("failed reload changed clipboard, crash budget, or serverWork")
	}
}

func TestSettingsWhileUnloadedAndEngineSwitch(t *testing.T) {
	a, _, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.unloadModel()
	req := newSettingsRequest(nil, time.Second)
	a.handleSettingsRequest(req)
	st := (<-req.reply).state
	if len(st.Models) == 0 || len(st.Engines) == 0 || a.sup.Running() || !a.modelUnloaded {
		t.Fatal("settings must remain available without loading the model")
	}
	a.switchBackend("cuda")
	if !a.sup.Running() || a.modelUnloaded || a.serverWork != 0 {
		t.Fatal("engine switch did not restore running state")
	}
	if a.limiter.Attempt() != 0 {
		t.Fatal("engine switch from unloaded consumed crash budget")
	}
}

func TestReloadSupersededBySecondHotkeyDuringLoad(t *testing.T) {
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "200")
	a, _, cb, pastes, _ := uxApp(t)
	a.conf.AutoPaste = true
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.unloadModel()

	currentText := "こんにちは"
	a.capture = func(Clipboard, uintptr) (string, bool, error) {
		cb.text = currentText
		return currentText, true, nil
	}

	var hotkeyFired bool
	a.hotkey = pressable{spec: "Ctrl+Alt+E", fired: &hotkeyFired}

	origUI := a.sup.ui
	a.sup.ui = func(label string, elapsed time.Duration) bool {
		if currentText == "こんにちは" {
			currentText = "さようなら"
			hotkeyFired = true
		}
		return origUI(label, elapsed)
	}

	cb.text = "こんにちは"
	a.startTranslation(0)

	// Only さようなら should be translated.
	select {
	case res := <-a.results:
		a.handleResult(res)
		if res.original != "さようなら" {
			t.Fatalf("expected さようなら translated, got %q", res.original)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("translation of second hotkey did not complete")
	}

	if cb.text != fakeTranslation {
		t.Fatalf("clipboard = %q, want %q", cb.text, fakeTranslation)
	}
	if *pastes != 1 {
		t.Fatalf("pastes = %d, want 1", *pastes)
	}

	// Verify no result for text A was ever generated.
	select {
	case res := <-a.results:
		t.Fatalf("unexpected extra result: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestReloadSecondHotkeyAfterLoadBehavesAsBefore(t *testing.T) {
	a, _, cb, pastes, fg := uxApp(t)
	a.conf.AutoPaste = true
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.unloadModel()

	currentText := "こんにちは"
	a.capture = func(Clipboard, uintptr) (string, bool, error) {
		cb.text = currentText
		return currentText, true, nil
	}

	// First hotkey starts reload and translates.
	a.startTranslation(0)
	g1 := a.generation
	if !a.sup.Running() || a.modelUnloaded || !a.translating {
		t.Fatal("server must be running and translating")
	}

	// While translating, a second hotkey arrives after reload finished.
	currentText = "さようなら"
	a.startTranslation(0)
	g2 := a.generation
	if g2 <= g1 {
		t.Fatalf("generation must advance: g1=%d g2=%d", g1, g2)
	}

	// Stale result for first request is ignored.
	a.handleResult(result{generation: g1, original: "こんにちは", translated: "Hello", window: *fg, fromSelection: true})
	if cb.text == "Hello" {
		t.Fatal("stale result for request A must be dropped")
	}
	if *pastes != 0 {
		t.Fatal("stale result must not paste")
	}

	// Result for second request succeeds.
	a.handleResult(result{generation: g2, original: "さようなら", translated: "Goodbye", window: *fg, fromSelection: true})
	if cb.text != "Goodbye" {
		t.Fatalf("clipboard = %q, want Goodbye", cb.text)
	}
	if *pastes != 1 {
		t.Fatalf("pastes = %d, want 1", *pastes)
	}
}

func TestReloadCancelDuringLoad(t *testing.T) {
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "200")
	a, tray, cb, pastes, _ := uxApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.unloadModel()

	cb.text = "こんにちは"
	a.capture = func(Clipboard, uintptr) (string, bool, error) { return cb.text, true, nil }

	cancelled := false
	origUI := a.sup.ui
	a.sup.ui = func(label string, elapsed time.Duration) bool {
		if !cancelled {
			cancelled = true
			tray.cmds = append(tray.cmds, cmdCancel)
		}
		return origUI(label, elapsed)
	}

	a.startTranslation(0)

	if a.translating {
		t.Fatal("translating must be false after cancel")
	}
	if !strings.Contains(tray.status, "cancelled") {
		t.Fatalf("status = %q, want cancelled", tray.status)
	}
	if cb.text != "こんにちは" {
		t.Fatalf("clipboard modified: %q", cb.text)
	}
	if *pastes != 0 {
		t.Fatalf("pastes = %d, want 0", *pastes)
	}
	select {
	case res := <-a.results:
		t.Fatalf("translation of A ran after cancel: %+v", res)
	default:
	}
	if !a.sup.Running() || a.modelUnloaded {
		t.Fatal("server should finish loading and be running")
	}
}

func TestReloadQuitDuringLoad(t *testing.T) {
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "200")
	a, tray, cb, pastes, _ := uxApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.unloadModel()

	cb.text = "こんにちは"
	a.capture = func(Clipboard, uintptr) (string, bool, error) { return cb.text, true, nil }

	origUI := a.sup.ui
	a.sup.ui = func(label string, elapsed time.Duration) bool {
		tray.quit = true
		return origUI(label, elapsed)
	}

	a.startTranslation(0)

	if a.translating {
		t.Fatal("translating must be false after quit")
	}
	if a.sup.Running() {
		t.Fatal("server must be stopped after quit")
	}
	if !a.modelUnloaded {
		t.Fatal("model must remain unloaded after quit")
	}
	select {
	case res := <-a.results:
		t.Fatalf("translation of A ran after quit: %+v", res)
	default:
	}
	if cb.text != "こんにちは" {
		t.Fatalf("clipboard modified: %q", cb.text)
	}
	if *pastes != 0 {
		t.Fatalf("pastes = %d, want 0", *pastes)
	}
}

func TestEngineSwitchQueuedDuringDelayedStartRunsAfter(t *testing.T) {
	findBackendCmd := func(a *app, name string) int {
		backends := availableBackends(a.dir)
		for i, b := range backends {
			if b == name {
				return cmdBackendBase + i
			}
		}
		return -1
	}

	t.Run("DuringReload", func(t *testing.T) {
		t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "200")
		a, tray, cb, pastes, _ := uxApp(t)
		if err := a.startServer(); err != nil {
			t.Fatal(err)
		}
		a.unloadModel()

		switchCmd := findBackendCmd(a, "cuda")
		if switchCmd < 0 {
			t.Fatal("cuda backend not available in test")
		}

		cb.text = "こんにちは"
		a.capture = func(Clipboard, uintptr) (string, bool, error) { return cb.text, true, nil }

		queued := false
		var workDuringOuterStart int
		var startingDuringOuterStart bool
		origUI := a.sup.ui
		a.sup.ui = func(label string, elapsed time.Duration) bool {
			if !queued {
				queued = true
				tray.cmds = append(tray.cmds, switchCmd)
			}
			if a.activeBackend == "cuda" {
				t.Fatal("engine switch executed inside sup.ui during outer start")
			}
			if a.serverWork > 1 {
				t.Fatalf("outer start re-entered: serverWork = %d", a.serverWork)
			}
			workDuringOuterStart = a.serverWork
			startingDuringOuterStart = a.sup.starting
			return origUI(label, elapsed)
		}

		a.startTranslation(0)

		if !queued {
			t.Fatal("sup.ui was not called to queue engine switch")
		}
		if workDuringOuterStart != 1 {
			t.Fatalf("serverWork during outer start = %d, want 1", workDuringOuterStart)
		}
		if !startingDuringOuterStart {
			t.Fatal("sup.starting should be true during outer start")
		}
		if a.activeBackend != "cuda" {
			t.Fatalf("backend = %q, want cuda", a.activeBackend)
		}
		if a.sup.starting {
			t.Fatal("a.sup.starting must be false after start and switch completed")
		}
		if a.serverWork != 0 {
			t.Fatalf("a.serverWork = %d, want 0", a.serverWork)
		}
		if !a.canUnloadModel() {
			t.Fatal("canUnloadModel must be true after switch completed")
		}
		if a.translating {
			t.Fatal("translating must be false after switch aborted the translation")
		}
		if *pastes != 0 {
			t.Fatalf("pastes = %d, want 0", *pastes)
		}
		select {
		case res := <-a.results:
			t.Fatalf("unexpected translation result for A: %+v", res)
		default:
		}
	})

	t.Run("DuringStartServer", func(t *testing.T) {
		t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "200")
		a, tray, _ := newTestApp(t)
		switchCmd := findBackendCmd(a, "cuda")
		if switchCmd < 0 {
			t.Fatal("cuda backend not available in test")
		}
		queued := false
		origUI := a.sup.ui
		a.sup.ui = func(label string, elapsed time.Duration) bool {
			if !queued {
				queued = true
				tray.cmds = append(tray.cmds, switchCmd)
			}
			if a.activeBackend == "cuda" {
				t.Fatal("engine switch ran inside sup.ui during startServer")
			}
			if a.serverWork > 1 {
				t.Fatalf("outer start re-entered: serverWork = %d", a.serverWork)
			}
			return origUI(label, elapsed)
		}
		if err := a.startServer(); err != nil {
			t.Fatal(err)
		}
		if a.activeBackend == "cuda" {
			t.Fatal("engine switch should not run until after startServer completed")
		}
		if a.sup.starting {
			t.Fatal("sup.starting must be false after startServer returns")
		}
		if a.serverWork != 0 {
			t.Fatalf("serverWork = %d, want 0", a.serverWork)
		}
		id, ok := tray.NextCommand()
		if !ok || id != switchCmd {
			t.Fatalf("expected switch command queued, got id=%d ok=%v", id, ok)
		}
		a.handleCommand(id)
		if a.activeBackend != "cuda" {
			t.Fatalf("backend = %q, want cuda", a.activeBackend)
		}
		if a.sup.starting {
			t.Fatal("sup.starting must be false after switch completed")
		}
		if a.serverWork != 0 {
			t.Fatalf("serverWork = %d, want 0", a.serverWork)
		}
		if !a.canUnloadModel() {
			t.Fatal("canUnloadModel must be true")
		}
	})
}

type serveIdleTestHotkey struct {
	tray *fakeTray
	now  *time.Time
	step int
	t    *testing.T
	app  *app
}

func (h *serveIdleTestHotkey) Pressed() (bool, error) {
	switch h.step {
	case 0:
		// Advance 1m (idle limit is 2m). Nothing should unload.
		*h.now = h.now.Add(1 * time.Minute)
	case 1:
		if !h.app.sup.Running() || h.app.modelUnloaded {
			h.t.Fatal("unloaded before deadline")
		}
		// Cross deadline, but mark translating. Must not unload while translating.
		*h.now = h.now.Add(2 * time.Minute)
		h.app.translating = true
	case 2:
		if !h.app.sup.Running() || h.app.modelUnloaded {
			h.t.Fatal("unloaded while translating")
		}
		// Finish translation and reset timer.
		h.app.translating = false
		h.app.resetIdleTimer()
	case 3:
		if !h.app.sup.Running() || h.app.modelUnloaded {
			h.t.Fatal("unloaded immediately after translation")
		}
		// Advance 1m post-translation (deadline 2m).
		*h.now = h.now.Add(1 * time.Minute)
	case 4:
		if !h.app.sup.Running() || h.app.modelUnloaded {
			h.t.Fatal("unloaded before post-translation deadline")
		}
		// Cross deadline post-translation. Next tickIdleUnload in serve must unload.
		*h.now = h.now.Add(2 * time.Minute)
	case 5:
		if h.app.sup.Running() || !h.app.modelUnloaded {
			h.t.Fatal("expected model unloaded in serve after deadline crossed")
		}
		h.tray.quit = true
	}
	h.step++
	return false, nil
}

func (*serveIdleTestHotkey) Close() error { return nil }

func TestServeIdleAutomaticUnload(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	a.conf.IdleUnloadMinutes = 2
	a.conf.PollMS = 10
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}

	hk := &serveIdleTestHotkey{
		tray: tray,
		now:  &now,
		t:    t,
		app:  a,
	}
	a.hotkey = hk
	a.serve(cb)

	if hk.step < 6 {
		t.Fatalf("test did not complete all steps, stopped at %d", hk.step)
	}
	if a.sup.Running() || !a.modelUnloaded {
		t.Fatal("serve did not unload idle model")
	}
	if !strings.Contains(tray.status, "model unloaded") {
		t.Fatalf("status = %q, want model unloaded", tray.status)
	}
}

func TestServerWorkResetAfterFailedStartFallbackAndErrQuit(t *testing.T) {
	// 1. Failed start
	a1, _, _ := newTestApp(t)
	a1.paths.server = filepath.Join(a1.dir, "missing-server")
	a1.paths.fallback = a1.paths.server
	if err := a1.startServer(); err == nil {
		t.Fatal("expected startServer failure")
	}
	if a1.serverWork != 0 {
		t.Fatalf("serverWork = %d after failed start, want 0", a1.serverWork)
	}

	// 2. CPU fallback
	a2, _, _ := newTestApp(t)
	a2.conf.ServerFile = filepath.Join("runtime", "vulkan", serverBinaryName())
	a2.paths.server = filepath.Join(a2.dir, a2.conf.ServerFile)
	if err := a2.startServer(); err != nil {
		t.Fatalf("startServer fallback: %v", err)
	}
	if !a2.onFallback() {
		t.Fatal("expected CPU fallback")
	}
	if a2.serverWork != 0 {
		t.Fatalf("serverWork = %d after CPU fallback, want 0", a2.serverWork)
	}

	// 3. errQuit
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "200")
	a3, tray3, _ := newTestApp(t)
	origUI := a3.sup.ui
	a3.sup.ui = func(label string, elapsed time.Duration) bool {
		tray3.quit = true
		return origUI(label, elapsed)
	}
	err := a3.startServer()
	if !errors.Is(err, errQuit) {
		t.Fatalf("expected errQuit, got %v", err)
	}
	if a3.serverWork != 0 {
		t.Fatalf("serverWork = %d after errQuit, want 0", a3.serverWork)
	}
}
