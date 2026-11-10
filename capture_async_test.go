package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// scriptedCapture is a capture that finishes when done() says so.
type scriptedCapture struct {
	steps int
	done  func() bool
	text  string
	fresh bool
	err   error
}

func (c *scriptedCapture) Step() (bool, string, bool, error) {
	c.steps++
	if c.done != nil && !c.done() {
		return false, "", false, nil
	}
	return true, c.text, c.fresh, c.err
}

func (c *scriptedCapture) Wait() time.Duration { return time.Millisecond }

func translateStub(t *testing.T, out string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": out}}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPendingCaptureReturnsAtOnceAndTranslatesWhenDone(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	a.endpoint = translateStub(t, "こんにちは").URL
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	cb.text = "old clipboard"
	ready := false
	task := &scriptedCapture{done: func() bool { return ready }, text: "Hello", fresh: true}
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask { return task }
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) {
		t.Fatal("the blocking capture must not be used when a capture task exists")
		return "", false, nil
	}

	a.startTranslation(0)
	if a.capturing == nil || a.translating {
		t.Fatalf("capturing=%v translating=%v: the hotkey must return while the copy is pending", a.capturing != nil, a.translating)
	}
	if strings.Contains(tray.status, "copying") {
		t.Fatalf("status = %q: a copy pending for a moment needs no note yet", tray.status)
	}
	now = now.Add(copyingNoteAfter)
	a.stepCapture()
	if a.capturing == nil || a.translating {
		t.Fatal("a capture that is not done must stay pending")
	}
	if !strings.Contains(tray.status, "copying") {
		t.Fatalf("status = %q, want a copying note once the copy takes a while", tray.status)
	}

	ready = true
	a.stepCapture()
	if a.capturing != nil || !a.translating {
		t.Fatalf("capturing=%v translating=%v: a finished copy must start the translation", a.capturing != nil, a.translating)
	}
	select {
	case res := <-a.results:
		if res.original != "Hello" || !res.fromSelection {
			t.Fatalf("result original=%q fromSelection=%v", res.original, res.fromSelection)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no result")
	}
}

func TestCaptureDoneOnFirstStepTranslatesAtOnce(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	a.endpoint = translateStub(t, "こんにちは").URL
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask {
		return &scriptedCapture{text: "Hello", fresh: true}
	}
	a.startTranslation(0)
	if a.capturing != nil || !a.translating {
		t.Fatalf("capturing=%v translating=%v", a.capturing != nil, a.translating)
	}
	if strings.Contains(tray.status, "copying") {
		t.Fatalf("status = %q: an instant copy needs no copying note", tray.status)
	}
	select {
	case <-a.results:
	case <-time.After(5 * time.Second):
		t.Fatal("no result")
	}
}

// A new hotkey while a copy is pending drops that copy: it is never stepped
// again and never translated.
func TestNewHotkeyDropsPendingCapture(t *testing.T) {
	a := profilesApp(t,
		config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		config.Profile{Hotkey: "Ctrl+Alt+K", Languages: []string{"ja", "en"}, Input: config.InputClipboard})
	a.endpoint = translateStub(t, "こんにちは").URL
	a.foreground = func() uintptr { return 1 }
	a.cb = &testClipboard{text: "Copied by hand"}
	first := &scriptedCapture{done: func() bool { return false }, text: "never", fresh: true}
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask { return first }

	a.startTranslation(0)
	if a.capturing == nil {
		t.Fatal("first capture should be pending")
	}
	steps := first.steps
	a.startTranslation(1)
	if a.capturing != nil {
		t.Fatal("a new hotkey must drop the pending capture")
	}
	a.stepCapture()
	if first.steps != steps {
		t.Fatal("a dropped capture was stepped again")
	}
	select {
	case res := <-a.results:
		if res.original != "Copied by hand" {
			t.Fatalf("translated %q, want the second hotkey's clipboard text", res.original)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no result")
	}
}

// Cancelling (or any abort) also drops a pending capture.
func TestAbortDropsPendingCapture(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask {
		return &scriptedCapture{done: func() bool { return false }}
	}
	a.startTranslation(0)
	if a.capturing == nil {
		t.Fatal("capture should be pending")
	}
	now = now.Add(time.Second)
	a.stepCapture()
	if !strings.Contains(tray.status, "copying") {
		t.Fatalf("status = %q, want the copying note", tray.status)
	}
	a.abortTranslation()
	if a.capturing != nil {
		t.Fatal("abortTranslation must drop the pending capture")
	}
	if tray.status != a.readyTip {
		t.Fatalf("status = %q after the abort, want the ready tip", tray.status)
	}
}

// serveCaptureHotkey presses profile 0 once, then counts how often the serve
// loop polls it while the selection copy is pending, and quits once the
// translation is in the history.
type serveCaptureHotkey struct {
	app          *app
	tray         *fakeTray
	calls        int
	pollsPending int
	t            *testing.T
}

func (h *serveCaptureHotkey) Pressed() (bool, error) {
	h.calls++
	if h.calls == 1 {
		return true, nil
	}
	if h.app.capturing != nil {
		h.pollsPending++
	}
	if len(h.app.hist.Items()) > 0 || h.calls > 2000 {
		h.tray.quit = true
	}
	return false, nil
}

func (*serveCaptureHotkey) Close() error { return nil }

// The serve loop keeps polling hotkeys (and with them the tray's messages)
// while a slow copy is pending, then translates the selection.
func TestServeKeepsPollingWhileCopyIsPending(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	a.conf.PollMS = 5
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.endpoint = translateStub(t, "こんにちは").URL
	hk := &serveCaptureHotkey{app: a, tray: tray, t: t}
	a.hotkey = hk
	cb.text = "old clipboard"
	task := &scriptedCapture{done: func() bool {
		if hk.pollsPending < 3 {
			return false
		}
		cb.text = "Hello" // what the page's late Ctrl+C put on the clipboard
		return true
	}, text: "Hello", fresh: true}
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask { return task }

	a.serve(cb)

	if hk.pollsPending < 3 {
		t.Fatalf("hotkeys were polled %d times while the copy was pending; the loop must not block", hk.pollsPending)
	}
	items := a.hist.Items()
	if len(items) != 1 || items[0].Source != "Hello" {
		t.Fatalf("history = %+v, want the slow selection translated", items)
	}
}
