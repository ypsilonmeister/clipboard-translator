package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func TestStartupHotkeyConflictFallsBackToNoHotkey(t *testing.T) {
	a, _, errs := newTestApp(t)
	a.newHotkey = func(spec string) (platform.Hotkey, error) {
		if spec == "Ctrl+Alt+E" {
			return nil, errors.New("already registered")
		}
		return fakeHotkey{spec}, nil
	}

	a.registerStartupHotkey()
	if !a.hasNoHotkey() {
		t.Fatalf("a taken startup hotkey must leave the app on noHotkey{}, got %#v", a.hotkey)
	}
	if a.readyTip != noHotkeyTip {
		t.Fatalf("the tooltip must say no hotkey is active, got %q", a.readyTip)
	}
	if len(*errs) != 0 {
		t.Fatalf("no modal error is expected, got %v", *errs)
	}
	if pressed, err := a.hotkey.Pressed(); pressed || err != nil {
		t.Fatalf("noHotkey must be inert: %v %v", pressed, err)
	}
	// Settings stays reachable even without a registered hotkey.
	if it := a.buildMenu()[0]; it.ID != cmdSettings || it.Disabled {
		t.Fatalf("Settings unavailable: %+v", it)
	}
	err := changeSettingsHotkey(t, a, "Ctrl+Alt+E")
	if !a.hasNoHotkey() || err == nil || !strings.Contains(err.Error(), "Choose another hotkey") {
		t.Fatalf("failed retry: %#v / %v", a.hotkey, err)
	}
	if len(*errs) != 0 {
		t.Fatalf("a failed retry must not raise a modal error, got %v", *errs)
	}

	// Picking a free one recovers.
	changeSettingsHotkey(t, a, "Ctrl+Shift+Y")
	if hk, ok := a.hotkey.(fakeHotkey); !ok || hk.spec != "Ctrl+Shift+Y" {
		t.Fatalf("the chosen hotkey must be registered, got %#v", a.hotkey)
	}
	if a.conf.Hotkey != "Ctrl+Shift+Y" || !strings.Contains(a.readyTip, "Ctrl+Shift+Y") {
		t.Fatalf("hotkey not applied: %q / %q", a.conf.Hotkey, a.readyTip)
	}
	if c, _ := loadConfig(a.dir); c.Hotkey != "Ctrl+Shift+Y" {
		t.Fatalf("hotkey not saved: %q", c.Hotkey)
	}
}

// breakPreviousEngine makes the app's current server paths unusable, as if the
// runtime it was using had been deleted or damaged.
func breakPreviousEngine(a *app) {
	missing := filepath.Join(a.dir, "runtime", "gone", platform.ServerBinaryName())
	a.paths.server, a.paths.fallback = missing, missing
}

func TestSwitchBackendBothFailLeavesRecoverableState(t *testing.T) {
	a, tray, _ := newTestApp(t)
	good := a.paths
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	gen := pending(a) // a translation is in flight
	breakPreviousEngine(a)

	a.switchBackend("vulkan") // vulkan cannot start and neither can the old engine
	if a.sup.Running() || healthy(a) {
		t.Fatal("expected no server after both the switch and the rollback failed")
	}
	if a.sup.Exited() != nil {
		t.Fatal("precondition: Exited() is nil with no server, so the main loop needs another trigger")
	}
	if a.translating || a.generation == gen {
		t.Fatal("the in-flight translation must be aborted when the engine is switched")
	}
	if !strings.Contains(tray.lastNote(), "recover") {
		t.Fatalf("the user must be told the server is being recovered, got %q", tray.lastNote())
	}

	// The main loop notices and recovers through the bounded restart policy.
	a.paths = good // the old runtime is repaired in the meantime
	if !a.recoverServer() {
		t.Fatal("recoverServer must bring the server back once the runtime works")
	}
	if !a.sup.Running() || !healthy(a) {
		t.Fatal("a healthy server must be running after recovery")
	}
}

func TestServeRecoversOrQuitsAfterFailedSwitch(t *testing.T) {
	a, _, errs := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.hotkey = fakeHotkey{"Ctrl+Alt+E"}
	breakPreviousEngine(a)
	a.limiter = restartLimiter{max: 0, window: time.Minute} // no recovery attempts allowed
	req := newSettingsRequest(nil, time.Minute)
	req.action, req.arg = "engine", "vulkan"
	a.settingsReqs <- req

	done := make(chan struct{})
	go func() { a.serve(&testClipboard{}); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve must not keep running without a server")
	}
	if len(*errs) != 1 || !strings.Contains((*errs)[0], "keeps stopping") {
		t.Fatalf("the user must see why the app quits, got %v", *errs)
	}
	if a.sup.Running() {
		t.Fatal("no server may be running")
	}
}

func TestRetryingTheConfiguredHotkeyOnceFree(t *testing.T) {
	a, _, _ := newTestApp(t)
	taken := true
	a.newHotkey = func(spec string) (platform.Hotkey, error) {
		if taken {
			return nil, errors.New("already registered")
		}
		return fakeHotkey{spec}, nil
	}
	a.registerStartupHotkey()
	taken = false // the other program let go
	changeSettingsHotkey(t, a, a.conf.Hotkey)
	if a.hasNoHotkey() {
		t.Fatal("choosing the configured hotkey again must retry the registration")
	}
}

type quitOnNotifyTray struct {
	*fakeTray
}

func (q *quitOnNotifyTray) Notify(title, message string, isError bool) {
	if strings.Contains(message, "Copied.") {
		q.fakeTray.quit = true
	}
	q.fakeTray.Notify(title, message, isError)
}

func TestServeChecksQuitRequestedBetweenQueuedCommands(t *testing.T) {
	a, tray, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.hotkey = fakeHotkey{"Ctrl+Alt+E"}
	a.hist.Add(historyEntry{Source: "source", Translated: "translated"})

	// Wrap tray so that when cmdHistoryBase calls Notify, QuitRequested becomes true.
	wrappedTray := &quitOnNotifyTray{fakeTray: tray}
	a.tray = wrappedTray

	// Queue two commands:
	// 1. cmdHistoryBase (will trigger Notify and set quit = true)
	// 2. cmdHistoryClear (must NOT be executed because Quit was requested)
	tray.cmds = []int{cmdHistoryBase, cmdHistoryClear}

	done := make(chan struct{})
	go func() {
		a.serve(&testClipboard{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit after quit request")
	}

	if len(tray.notes) == 0 || !strings.Contains(tray.lastNote(), "Copied.") {
		t.Error("first command (cmdHistoryBase) should have executed")
	}
	if len(a.hist.Items()) == 0 {
		t.Error("second command (cmdHistoryClear) must not have executed after quit request")
	}
	if len(tray.cmds) != 1 || tray.cmds[0] != cmdHistoryClear {
		t.Errorf("cmdHistoryClear should have remained in queue, remaining cmds: %v", tray.cmds)
	}
}
