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
	*now = now.Add(900 * time.Millisecond)
	answerRemoteCreate(t, r, w)
	finishRestore(t, r, w, "restored-token")
	if done, _, _, err := task.Step(); done || err != nil || task.phase != "modifiers" {
		t.Fatalf("capture must resume after the session returns: done=%v err=%v phase=%s", done, err, task.phase)
	}
	if task.session != r.session {
		t.Fatal("capture must bind to the new session")
	}
	*now = now.Add(160 * time.Millisecond)
	task.Step()
	*now = now.Add(160 * time.Millisecond)
	task.Step()
	if task.phase != "old" && task.phase != "keys" {
		t.Fatalf("capture did not proceed after reconnect, phase=%s", task.phase)
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
