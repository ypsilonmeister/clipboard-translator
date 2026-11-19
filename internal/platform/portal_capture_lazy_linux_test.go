package platform

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

// These tests pin #133 P1-7: a hotkey capture that starts while the
// RemoteDesktop session is reconnecting waits up to 2 s for it, instead of
// failing at once.

func closedRemote(t *testing.T) (*portalRemote, *fakeRemoteWire, *time.Time) {
	t.Helper()
	r, w, now := remoteFixture(t)
	authorizeRemote(t, r, w)
	w.events = append(w.events, portalEvent{kind: "closed", session: inhibitSession})
	r.Step()
	if !r.reconnecting() {
		t.Fatalf("fixture must be reconnecting, phase=%s", r.phase)
	}
	return r, w, now
}

func TestPortalCaptureWaitsForReconnectWithinBound(t *testing.T) {
	r, w, now := closedRemote(t)
	requests := len(w.requests)
	task := beginPortalCapture(r, func() bool { return true }).(*portalCapture)
	if done, _, _, err := task.Step(); done || err != nil {
		t.Fatalf("capture must wait for the reconnect, done=%v err=%v", done, err)
	}
	if len(w.requests) != requests+1 || r.operation != "create" {
		t.Fatal("a capture during reconnect must start the reconnect at once, not wait for the backoff")
	}
	// Step the capture in every intermediate reconnect phase: it must keep waiting.
	stepWaiting := func(phase string) {
		t.Helper()
		*now = now.Add(100 * time.Millisecond)
		if r.phase != phase {
			t.Fatalf("remote phase=%s, want %s", r.phase, phase)
		}
		if done, _, _, err := task.Step(); done || err != nil || task.phase != "reconnect" {
			t.Fatalf("capture must wait while the remote is in %s: done=%v err=%v phase=%s", phase, done, err, task.phase)
		}
	}
	stepWaiting("create")
	answerRemoteCreate(t, r, w)
	stepWaiting("select")
	w.events = append(w.events, portalEvent{kind: "response", token: r.pending})
	r.Step()
	stepWaiting("clipboard")
	w.events = append(w.events, portalEvent{kind: "method", token: r.pending})
	r.Step()
	stepWaiting("start")
	w.events = append(w.events, portalEvent{kind: "response", token: r.pending, devices: 1, clipboard: true, restore: "restored-token"})
	r.Step()
	if r.phase != "ready" {
		t.Fatalf("restore did not reach ready: phase=%s err=%v", r.phase, r.err)
	}
	if done, _, _, err := task.Step(); done || err != nil || task.phase != "modifiers" {
		t.Fatalf("capture must resume after the session returns: done=%v err=%v phase=%s", done, err, task.phase)
	}
	if task.session != r.session {
		t.Fatal("capture must bind to the new session")
	}
	task.Step() // modifiers released: arms the 150 ms settle timer
	*now = now.Add(160 * time.Millisecond)
	task.Step() // enter "old", clipboard read pending
	primeClipboard(t, r, w, "old text")
	*now = now.Add(10 * time.Millisecond)
	task.Step()
	if task.phase != "keys" {
		t.Fatalf("capture did not proceed after reconnect, phase=%s", task.phase)
	}
	finishRemoteKeys(t, r, w, task.keys)
	task.Step()
	w.events = append(w.events, portalEvent{kind: "selection-owner", session: r.session, mime: portalTextMIME})
	r.Step()
	task.Step()
	*now = now.Add(100 * time.Millisecond)
	task.Step()
	if r.clip.read != nil && !r.clip.read.done {
		w.events = append(w.events, portalEvent{kind: "read", token: r.clip.read.token, data: []byte("captured")})
	}
	done, text, fromSel, err := task.Step()
	if !done || err != nil || text != "captured" || fromSel {
		t.Fatalf("capture must finish with the copied text: done=%v text=%q fromSel=%v err=%v phase=%s", done, text, fromSel, err, task.phase)
	}
}

// A hotkey that arrives while the remote is already past CreateSession
// (select/clipboard/start) is still a reconnect and must wait.
func TestPortalCaptureStartsDuringLaterReconnectPhases(t *testing.T) {
	for _, phase := range []string{"select", "clipboard", "start"} {
		t.Run(phase, func(t *testing.T) {
			r, w, now := closedRemote(t)
			r.reconnectNow()
			r.Step()
			answerRemoteCreate(t, r, w)
			if phase != "select" {
				w.events = append(w.events, portalEvent{kind: "response", token: r.pending})
				r.Step()
			}
			if phase == "start" {
				w.events = append(w.events, portalEvent{kind: "method", token: r.pending})
				r.Step()
			}
			if r.phase != phase {
				t.Fatalf("fixture phase=%s, want %s", r.phase, phase)
			}
			task := beginPortalCapture(r, func() bool { return true }).(*portalCapture)
			if task.phase != "reconnect" {
				t.Fatalf("capture must enter reconnect while remote is in %s, got %s", phase, task.phase)
			}
			*now = now.Add(100 * time.Millisecond)
			if done, _, _, err := task.Step(); done || err != nil {
				t.Fatalf("capture must wait in %s: done=%v err=%v", phase, done, err)
			}
		})
	}
}

func TestPortalCaptureReconnectBeyondBoundFails(t *testing.T) {
	r, w, now := closedRemote(t)
	_ = w
	before := runtime.NumGoroutine()
	task := beginPortalCapture(r, func() bool { return true }).(*portalCapture)
	task.Step()
	*now = now.Add(portalCaptureReconnectWait - time.Millisecond)
	if done, _, _, _ := task.Step(); done {
		t.Fatal("capture must keep waiting until the bound")
	}
	*now = now.Add(time.Millisecond)
	done, text, fromSel, err := task.Step()
	if !done || text != "" || fromSel || !errors.Is(err, errPortalNotAuthorized) {
		t.Fatalf("expiry must fail with errPortalNotAuthorized: done=%v text=%q err=%v", done, text, err)
	}
	// The reconnect itself keeps going in the background; a late session must
	// not revive the expired job.
	answerRemoteCreate(t, r, w)
	finishRestore(t, r, w, "late-token")
	if done, _, _, err := task.Step(); !done || err == nil {
		t.Fatal("an expired job must stay failed")
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines leaked: %d -> %d", before, after)
	}
}

func TestPortalCaptureReconnectWhileShieldUpFailsAtBound(t *testing.T) {
	r, w, now := closedRemote(t)
	shield(w, true)
	r.Step()
	task := beginPortalCapture(r, func() bool { return true }).(*portalCapture)
	task.Step()
	if r.operation == "create" {
		w.events = append(w.events, portalEvent{kind: "response", token: r.pending, code: 2})
		r.Step()
	}
	*now = now.Add(portalCaptureReconnectWait)
	if done, _, _, err := task.Step(); !done || !errors.Is(err, errPortalNotAuthorized) {
		t.Fatalf("inhibited reconnect must fail at the bound: done=%v err=%v", done, err)
	}
	if r.phase != "waiting" {
		t.Fatalf("expiry must not disturb the P1-2 waiting state, phase=%s", r.phase)
	}
}

func TestPortalCaptureReadySessionDoesNotWait(t *testing.T) {
	r, w, _ := remoteFixture(t)
	authorizeRemote(t, r, w)
	if task := beginPortalCapture(r, func() bool { return true }).(*portalCapture); task.phase != "modifiers" {
		t.Fatalf("ready session must not enter the reconnect wait, phase=%s", task.phase)
	}
}
