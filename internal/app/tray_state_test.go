package app

import (
	"errors"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// TestTrayStateLifecycleSuccess verifies the full copy -> translate -> ready -> idle flow.
func TestTrayStateLifecycleSuccess(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	if tray.lastState() != platform.TrayIdle {
		t.Fatalf("initial tray state = %v, want %v", tray.lastState(), platform.TrayIdle)
	}

	captureDone := false
	task := &scriptedCapture{
		done:  func() bool { return captureDone },
		text:  "こんにちは",
		fresh: true,
	}
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask { return task }

	// 1. User presses hotkey: copy starts -> state becomes busy.
	a.startTranslation(0)
	if a.capturing == nil {
		t.Fatal("expected pending capture")
	}
	if got := tray.lastState(); got != platform.TrayBusy {
		t.Fatalf("state during copy = %v, want %v", got, platform.TrayBusy)
	}

	// 2. Selection copy finishes -> translation starts -> state stays busy.
	captureDone = true
	a.stepCapture()
	if a.capturing != nil {
		t.Fatal("expected capture to be done")
	}
	if !a.translating {
		t.Fatal("expected translation to be running")
	}
	if got := tray.lastState(); got != platform.TrayBusy {
		t.Fatalf("state during translating = %v, want %v", got, platform.TrayBusy)
	}

	// 3. Translation finishes and reaches clipboard -> state becomes ready (done).
	cb.text = "こんにちは"
	g := a.generation
	a.handleResult(result{
		original:   "こんにちは",
		translated: "Hello",
		generation: g,
		direction:  languages.Direction{Source: "ja", Target: "en"},
		window:     100,
	})
	if a.translating {
		t.Fatal("translating flag must be cleared")
	}
	if cb.text != "Hello" {
		t.Fatalf("clipboard = %q, want %q", cb.text, "Hello")
	}
	if got := tray.lastState(); got != platform.TrayReady {
		t.Fatalf("state after translation reaches clipboard = %v, want %v", got, platform.TrayReady)
	}

	// 4. Timeout expires -> state reverts to idle.
	now = now.Add(trayDoneDuration + 100*time.Millisecond)
	a.tickTransientStatus()
	if got := tray.lastState(); got != platform.TrayIdle {
		t.Fatalf("state after timeout = %v, want %v", got, platform.TrayIdle)
	}

	// Check recorded state transitions.
	wantStates := []platform.TrayState{platform.TrayBusy, platform.TrayReady, platform.TrayIdle}
	if len(tray.states) != len(wantStates) {
		t.Fatalf("tray.states = %v, want %v", tray.states, wantStates)
	}
	for i, s := range wantStates {
		if tray.states[i] != s {
			t.Fatalf("tray.states[%d] = %v, want %v", i, tray.states[i], s)
		}
	}
}

// TestTrayStateLifecycleCaptureFailure verifies problem state on capture error.
func TestTrayStateLifecycleCaptureFailure(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	task := &scriptedCapture{
		err: errors.New("cannot capture focus changed"),
	}
	a.beginCapture = func(platform.Clipboard, uintptr) platform.CaptureTask { return task }

	a.startTranslation(0)
	if got := tray.lastState(); got != platform.TrayProblem {
		t.Fatalf("state after capture failure = %v, want %v", got, platform.TrayProblem)
	}

	// Advance time past problem timeout -> returns to idle.
	now = now.Add(trayProblemDuration + 100*time.Millisecond)
	a.tickTransientStatus()
	if got := tray.lastState(); got != platform.TrayIdle {
		t.Fatalf("state after problem timeout = %v, want %v", got, platform.TrayIdle)
	}
}

// TestTrayStateLifecycleTranslationFailure verifies problem state on translation engine error.
func TestTrayStateLifecycleTranslationFailure(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	cb.text = "こんにちは"
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return cb.text, true, nil }

	a.startTranslation(0)
	if got := tray.lastState(); got != platform.TrayBusy {
		t.Fatalf("state during translation = %v, want %v", got, platform.TrayBusy)
	}

	g := a.generation
	a.handleResult(result{
		original:   "こんにちは",
		generation: g,
		err:        errors.New("llama-server crashed"),
	})
	if got := tray.lastState(); got != platform.TrayProblem {
		t.Fatalf("state after translation failure = %v, want %v", got, platform.TrayProblem)
	}

	now = now.Add(trayProblemDuration + 100*time.Millisecond)
	a.tickTransientStatus()
	if got := tray.lastState(); got != platform.TrayIdle {
		t.Fatalf("state after problem timeout = %v, want %v", got, platform.TrayIdle)
	}
}

// TestTrayStateLifecycleSkipPaths verifies problem state when text is skipped.
func TestTrayStateLifecycleSkipPaths(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	// Empty selection skip.
	cb.text = ""
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return "", false, nil }
	a.startTranslation(0)

	if got := tray.lastState(); got != platform.TrayProblem {
		t.Fatalf("state after skip = %v, want %v", got, platform.TrayProblem)
	}

	now = now.Add(trayProblemDuration + 100*time.Millisecond)
	a.tickTransientStatus()
	if got := tray.lastState(); got != platform.TrayIdle {
		t.Fatalf("state after problem timeout = %v, want %v", got, platform.TrayIdle)
	}
}

// TestTrayStateProblemClearedByNextPress verifies that pressing the hotkey
// while in problem state immediately clears problem and starts busy.
func TestTrayStateProblemClearedByNextPress(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	// Trigger skip -> problem state
	cb.text = ""
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return "", false, nil }
	a.startTranslation(0)
	if got := tray.lastState(); got != platform.TrayProblem {
		t.Fatalf("state after skip = %v, want %v", got, platform.TrayProblem)
	}

	// Press again with valid text before timeout expires.
	cb.text = "こんにちは"
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return cb.text, true, nil }
	a.startTranslation(0)

	if got := tray.lastState(); got != platform.TrayBusy {
		t.Fatalf("state after next press = %v, want %v", got, platform.TrayBusy)
	}
}

// TestTrayStateUnchangedSourceProblem verifies that when source == translated,
// the state indicates problem/notice.
func TestTrayStateUnchangedSourceProblem(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	cb.text = "Same text"
	g := pending(a)
	a.handleResult(result{
		original:   "Same text",
		translated: "Same text",
		generation: g,
	})

	if got := tray.lastState(); got != platform.TrayProblem {
		t.Fatalf("state after unchanged translation = %v, want %v", got, platform.TrayProblem)
	}

	now = now.Add(trayProblemDuration + 100*time.Millisecond)
	a.tickTransientStatus()
	if got := tray.lastState(); got != platform.TrayIdle {
		t.Fatalf("state after timeout = %v, want %v", got, platform.TrayIdle)
	}
}
