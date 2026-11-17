package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
	"github.com/ypsilonmeister/clipboard-translator/internal/update"
)

// Issue #198: tests for a failed reload from the unloaded state and for the
// tray menu while the main loop is inside a slow call.

// unloadedApp is an app whose model was unloaded on purpose, as after the
// idle timer or "Unload model".
func unloadedApp(t *testing.T) (*app, *fakeTray, *testClipboard) {
	t.Helper()
	a, tray, cb, _, _ := uxApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.unloadModel()
	cb.text = "こんにちは"
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return cb.text, true, nil }
	return a, tray, cb
}

func TestAPIReloadFailureAnswersWithoutModal(t *testing.T) {
	a, tray, _ := unloadedApp(t)
	a.conf.TranslateAPIEnabled = true
	a.paths.Server = filepath.Join(a.dir, "missing-server")
	a.paths.Fallback = a.paths.Server
	for try := 0; try < 2; try++ { // a client retries; no retry may open a box either
		replied := false
		a.showErr = func(msg string) {
			if !replied {
				t.Errorf("modal box before the /translate reply: %q", msg)
			}
			t.Errorf("modal box for an API request: %q", msg)
		}
		notes := len(tray.notes)
		req := settings.NewActionRequest("", "", time.Second)
		req.Text = "こんにちは"
		req.Context = context.Background()
		a.handleAPITranslation(req)
		select {
		case rep := <-req.Reply:
			replied = true
			if rep.Err == nil || errors.Is(rep.Err, settings.ErrBusy) {
				t.Fatalf("try %d: reply error = %v, want the start failure", try, rep.Err)
			}
		default:
			t.Fatalf("try %d: /translate was not answered", try)
		}
		if len(tray.notes) != notes {
			t.Fatalf("try %d: API failure raised a desktop notification: %q", try, tray.lastNote())
		}
		if a.translating || a.apiReply != nil || !a.modelUnloaded || a.serverWork != 0 {
			t.Fatalf("try %d: failed reload left busy state behind", try)
		}
	}
}

func TestCancelFromMenuDuringReload(t *testing.T) {
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "300")
	a, tray, cb := unloadedApp(t)
	gen := a.generation
	handledDuringLoad := false
	orig := a.sup.UI
	calls := 0
	a.sup.UI = func(label string, elapsed time.Duration) bool {
		calls++
		if calls == 1 {
			if !a.translating {
				t.Error("Cancel translation must be offered while the model loads")
			}
			tray.cmds = append(tray.cmds, cmdCancel)
		}
		if calls > 1 {
			elapsed += 5 * time.Second // a real load is long: the status line is rewritten
		}
		quit := orig(label, elapsed)
		if calls == 1 {
			handledDuringLoad = len(tray.cmds) == 0 && !a.translating
		}
		return quit
	}
	a.startTranslation(0)
	if !handledDuringLoad {
		t.Fatal("Cancel translation waited for the model load to finish")
	}
	if a.translating || a.generation == gen {
		t.Fatalf("translating=%v generation=%d (was %d): cancel must bump the generation", a.translating, a.generation, gen)
	}
	select {
	case res := <-a.results:
		t.Fatalf("a cancelled translation ran: %+v", res)
	case <-time.After(100 * time.Millisecond):
	}
	if cb.text != "こんにちは" {
		t.Fatalf("clipboard changed: %q", cb.text)
	}
	if !a.sup.Running() || a.modelUnloaded {
		t.Fatal("the load itself must finish; only the translation is cancelled")
	}
	if calls > 1 && tray.status != cancelledStatus {
		t.Fatalf("status after the load = %q, want %q", tray.status, cancelledStatus)
	}
}

func TestCancelFromMenuDuringAPIReloadAnswersAtOnce(t *testing.T) {
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "300")
	a, tray, _ := unloadedApp(t)
	a.conf.TranslateAPIEnabled = true
	req := settings.NewActionRequest("", "", time.Second)
	req.Text = "こんにちは"
	req.Context = context.Background()
	answeredDuringLoad := false
	orig := a.sup.UI
	calls := 0
	a.sup.UI = func(label string, elapsed time.Duration) bool {
		calls++
		if calls == 1 {
			tray.cmds = append(tray.cmds, cmdCancel)
		}
		quit := orig(label, elapsed)
		if calls == 1 {
			select {
			case rep := <-req.Reply:
				answeredDuringLoad = errors.Is(rep.Err, context.Canceled)
			default:
			}
		}
		return quit
	}
	a.handleAPITranslation(req)
	if !answeredDuringLoad {
		t.Fatal("the HTTP caller was not answered when the translation was cancelled during the load")
	}
	if a.translating || a.apiReply != nil {
		t.Fatal("cancelled API translation left busy state behind")
	}
}

func TestMenuCommandsDuringLoad(t *testing.T) {
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "300")
	a, tray, _ := unloadedApp(t)
	var opened []string
	a.openURL = func(target string) error { opened = append(opened, target); return nil }
	openedDuringLoad := false
	orig := a.sup.UI
	calls := 0
	a.sup.UI = func(label string, elapsed time.Duration) bool {
		calls++
		if calls == 1 {
			// Unload would nest a slow call: it must wait, and so must what was
			// picked after it, or the commands would run out of order.
			tray.cmds = append(tray.cmds, cmdPlamoLicense, cmdUnloadModel, cmdHistoryClear)
		}
		quit := orig(label, elapsed)
		if calls == 1 {
			openedDuringLoad = len(opened) == 1 && opened[0] == update.PlamoLicenseURL
		}
		return quit
	}
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if !openedDuringLoad {
		t.Fatalf("a safe menu command was not served during the load; opened=%v", opened)
	}
	if len(a.deferredCmds) != 2 || a.deferredCmds[0] != cmdUnloadModel || a.deferredCmds[1] != cmdHistoryClear {
		t.Fatalf("deferred = %v, want unload then clear history", a.deferredCmds)
	}
	if !a.sup.Running() {
		t.Fatal("a deferred command ran inside the load")
	}
	// The main loop takes the deferred commands first, in order, then the tray's.
	tray.cmds = append(tray.cmds, cmdOpenConfig)
	for _, want := range []int{cmdUnloadModel, cmdHistoryClear, cmdOpenConfig} {
		if id, ok := a.nextCommand(); !ok || id != want {
			t.Fatalf("nextCommand = %d, %v; want %d", id, ok, want)
		}
	}
	if _, ok := a.nextCommand(); ok {
		t.Fatal("nextCommand returned a command twice")
	}
}

func TestMenuAndAPIContextDuringRestartBackoff(t *testing.T) {
	a, tray, _ := newTestApp(t)
	var opened []string
	a.openURL = func(target string) error { opened = append(opened, target); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	a.apiContext = ctx
	a.apiReply = make(chan settings.Reply, 1)
	reply := a.apiReply
	a.translating = true
	cancel()
	tray.cmds = append(tray.cmds, cmdPlamoLicense, cmdUnloadModel)
	if a.sleepPumping(120 * time.Millisecond) {
		t.Fatal("no quit was requested")
	}
	if len(opened) != 1 || opened[0] != update.PlamoLicenseURL {
		t.Fatalf("menu command not served during the backoff; opened=%v", opened)
	}
	if len(a.deferredCmds) != 1 || a.deferredCmds[0] != cmdUnloadModel {
		t.Fatalf("deferred = %v", a.deferredCmds)
	}
	select {
	case rep := <-reply:
		if !errors.Is(rep.Err, context.Canceled) {
			t.Fatalf("reply = %v", rep.Err)
		}
	default:
		t.Fatal("a disconnected API caller was not released during the backoff")
	}
	if a.translating {
		t.Fatal("disconnected request still counted as translating")
	}

	// Quit wins over queued commands.
	tray.cmds = append(tray.cmds, cmdPlamoLicense)
	tray.quit = true
	if !a.sleepPumping(time.Second) || len(opened) != 1 {
		t.Fatal("quit during the backoff must stop it without running more commands")
	}
}

// pressHotkey reports one press each time it is armed.
type pressHotkey struct{ armed bool }

func (h *pressHotkey) Pressed() (bool, error) {
	p := h.armed
	h.armed = false
	return p, nil
}
func (*pressHotkey) Close() error { return nil }

// A hotkey pressed after "Cancel translation", while the model still loads, is
// a new request: it must not be dropped along with the cancelled one.
func TestHotkeyAfterCancelDuringReload(t *testing.T) {
	t.Setenv("CT_TEST_FAKE_SERVER_DELAY", "300")
	a, tray, cb := unloadedApp(t)
	hk := &pressHotkey{}
	a.hotkey = hk
	captures := 0
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { captures++; return cb.text, true, nil }
	orig := a.sup.UI
	calls := 0
	a.sup.UI = func(label string, elapsed time.Duration) bool {
		calls++
		switch calls {
		case 1:
			tray.cmds = append(tray.cmds, cmdCancel)
		case 2:
			hk.armed = true
		}
		return orig(label, elapsed)
	}
	a.startTranslation(0)
	if calls < 2 {
		t.Skip("the fake server came up before the second UI tick")
	}
	if captures != 2 || !a.translating {
		t.Fatalf("captures=%d translating=%v: the press after the cancel was dropped", captures, a.translating)
	}
	select {
	case res := <-a.results:
		if res.generation != a.generation {
			t.Fatalf("result generation %d, want %d", res.generation, a.generation)
		}
		a.handleResult(res)
	case <-time.After(5 * time.Second):
		t.Fatal("translation did not finish")
	}
}

// Before the first load at startup has finished no hotkey is registered, so
// Settings and the quick start guide wait; later loads serve them at once.
func TestSettingsAndGuideWaitForStartup(t *testing.T) {
	a, tray, _ := newTestApp(t)
	if a.hotkey != nil {
		t.Fatal("this test needs an app that has not registered its hotkeys")
	}
	a.conf.QuickstartShown = false
	opened := 0
	a.openURL = func(string) error { opened++; return nil }
	tray.cmds = append(tray.cmds, cmdQuickstart, cmdSettings, cmdPlamoLicense)
	a.serviceTrayWhileBusy()
	if len(tray.notes) != 0 || opened != 0 || a.settings != nil || a.conf.QuickstartShown {
		t.Fatalf("a command ran before startup finished: notes=%v opened=%d", tray.notes, opened)
	}
	if len(a.deferredCmds) != 3 {
		t.Fatalf("deferred = %v, want all three in order", a.deferredCmds)
	}

	a.deferredCmds = nil
	a.hotkey = fakeHotkey{a.conf.Hotkey}
	tray.cmds = append(tray.cmds, cmdQuickstart)
	a.serviceTrayWhileBusy()
	if len(tray.notes) != 1 || len(a.deferredCmds) != 0 {
		t.Fatalf("quick start not shown during a later load: notes=%v deferred=%v", tray.notes, a.deferredCmds)
	}
}

// "Cancel translation" is served even behind commands that wait.
func TestCancelOvertakesDeferredCommands(t *testing.T) {
	a, tray, _ := newTestApp(t)
	a.translating = true
	gen := a.generation
	tray.cmds = append(tray.cmds, cmdUnloadModel, cmdCancel, cmdPlamoLicense)
	a.serviceTrayWhileBusy()
	if a.translating || a.generation != gen+1 {
		t.Fatal("cancel waited behind a deferred command")
	}
	if len(a.deferredCmds) != 2 || a.deferredCmds[0] != cmdUnloadModel || a.deferredCmds[1] != cmdPlamoLicense {
		t.Fatalf("deferred = %v", a.deferredCmds)
	}
}
