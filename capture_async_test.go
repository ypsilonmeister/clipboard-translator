package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The job itself never sleeps: the app steps it and keeps pumping messages in
// between. A copy that takes 1.5 s is still picked up as a fresh selection.
func TestCaptureJobStepsWithoutSleeping(t *testing.T) {
	clk := newFakeClock()
	start := clk.time()
	clip := &testClipboard{text: "old"}
	seq := uint32(0)
	held := 3 // the hotkey's modifiers are up after three checks
	copied := false
	ops := captureOps{
		foreground:  func() uintptr { return 7 },
		modifiersUp: func() bool { held--; return held < 0 },
		sequence: func() uint32 {
			if copied && clk.time().Sub(start) >= 1500*time.Millisecond && seq == 0 {
				seq, clip.text = 1, "slow page selection"
			}
			return seq
		},
		sendCopy:        func() error { copied = true; return nil },
		modifierTimeout: 900 * time.Millisecond,
		copyTimeout:     2 * time.Second,
		settleTime:      80 * time.Millisecond,
		now:             clk.time,
		sleep:           func(time.Duration) { t.Fatal("a capture job must not sleep") },
	}
	j := newCaptureJob(clip, 7, ops)
	for steps := 0; ; steps++ {
		if steps > 1000 {
			t.Fatal("the capture never finished")
		}
		done, text, fresh, err := j.step()
		if done {
			if err != nil || text != "slow page selection" || !fresh {
				t.Fatalf("text=%q fresh=%v err=%v", text, fresh, err)
			}
			break
		}
		w := j.wait()
		if w <= 0 || w > capturePollStep {
			t.Fatalf("wait() = %v, want a short positive poll step", w)
		}
		clk.now = clk.now.Add(w)
	}
	if got := clk.time().Sub(start); got < 1580*time.Millisecond || got > 1700*time.Millisecond {
		t.Fatalf("finished after %v, want just after the copy settled (~1.58 s)", got)
	}
}

func TestCaptureJobModifiersHeldTimesOutWithoutSleeping(t *testing.T) {
	clk := newFakeClock()
	clip := &testClipboard{text: "stale"}
	copies := 0
	j := newCaptureJob(clip, 7, captureOps{
		foreground:      func() uintptr { return 7 },
		modifiersUp:     func() bool { return false },
		sequence:        func() uint32 { return 0 },
		sendCopy:        func() error { copies++; return nil },
		modifierTimeout: 900 * time.Millisecond,
		copyTimeout:     2 * time.Second,
		now:             clk.time,
		sleep:           func(time.Duration) { t.Fatal("a capture job must not sleep") },
	})
	for {
		done, text, _, err := j.step()
		if done {
			if !errors.Is(err, errCaptureModifiersHeld) || text != "" || copies != 0 {
				t.Fatalf("text=%q err=%v copies=%d", text, err, copies)
			}
			return
		}
		clk.now = clk.now.Add(j.wait())
	}
}

// scriptedCapture is a capture that finishes when done() says so.
type scriptedCapture struct {
	steps int
	done  func() bool
	text  string
	fresh bool
	err   error
}

func (c *scriptedCapture) step() (bool, string, bool, error) {
	c.steps++
	if c.done != nil && !c.done() {
		return false, "", false, nil
	}
	return true, c.text, c.fresh, c.err
}

func (c *scriptedCapture) wait() time.Duration { return time.Millisecond }

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
	cb.text = "old clipboard"
	ready := false
	task := &scriptedCapture{done: func() bool { return ready }, text: "Hello", fresh: true}
	a.beginCapture = func(Clipboard, uintptr) captureTask { return task }
	a.capture = func(Clipboard, uintptr) (string, bool, error) {
		t.Fatal("the blocking capture must not be used when a capture task exists")
		return "", false, nil
	}

	a.startTranslation(0)
	if a.capturing == nil || a.translating {
		t.Fatalf("capturing=%v translating=%v: the hotkey must return while the copy is pending", a.capturing != nil, a.translating)
	}
	if !strings.Contains(tray.status, "copying") {
		t.Fatalf("status = %q, want a copying note", tray.status)
	}
	a.stepCapture()
	if a.capturing == nil || a.translating {
		t.Fatal("a capture that is not done must stay pending")
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
	a.beginCapture = func(Clipboard, uintptr) captureTask { return &scriptedCapture{text: "Hello", fresh: true} }
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
		Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}},
		Profile{Hotkey: "Ctrl+Alt+K", Languages: []string{"ja", "en"}, Input: InputClipboard})
	a.endpoint = translateStub(t, "こんにちは").URL
	a.foreground = func() uintptr { return 1 }
	a.cb = &testClipboard{text: "Copied by hand"}
	first := &scriptedCapture{done: func() bool { return false }, text: "never", fresh: true}
	a.beginCapture = func(Clipboard, uintptr) captureTask { return first }

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
	a, _, _, _, _ := uxApp(t)
	a.beginCapture = func(Clipboard, uintptr) captureTask {
		return &scriptedCapture{done: func() bool { return false }}
	}
	a.startTranslation(0)
	if a.capturing == nil {
		t.Fatal("capture should be pending")
	}
	a.abortTranslation()
	if a.capturing != nil {
		t.Fatal("abortTranslation must drop the pending capture")
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
	a.beginCapture = func(Clipboard, uintptr) captureTask { return task }

	a.serve(cb)

	if hk.pollsPending < 3 {
		t.Fatalf("hotkeys were polled %d times while the copy was pending; the loop must not block", hk.pollsPending)
	}
	items := a.hist.Items()
	if len(items) != 1 || items[0].Source != "Hello" {
		t.Fatalf("history = %+v, want the slow selection translated", items)
	}
}
