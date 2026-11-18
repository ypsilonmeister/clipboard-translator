package platform

import (
	"errors"
	"testing"
	"time"
)

// The tests in this file pin #133 P1-2: GNOME Shell inhibits RemoteDesktop
// session creation while the screen shield is up (design memo §0.2) and closes
// started sessions when it comes up. The remote feature must wait and
// reconnect with its restore token instead of failing or forgetting consent.

const inhibitSession = "/org/freedesktop/portal/desktop/session/test/remote"

func shield(w *fakeRemoteWire, active bool) {
	code := uint32(0)
	if active {
		code = 1
	}
	w.events = append(w.events, portalEvent{kind: "screensaver", code: code})
}

func lastSelect(w *fakeRemoteWire) (remoteCall, bool) {
	for i := len(w.calls) - 1; i >= 0; i-- {
		if w.calls[i].kind == "select" {
			return w.calls[i], true
		}
	}
	return remoteCall{}, false
}

// finishRestore drives select → clipboard → start for the current session.
func finishRestore(t *testing.T, r *portalRemote, w *fakeRemoteWire, restore string) {
	t.Helper()
	if r.operation != "select" {
		t.Fatalf("expected SelectDevices, got %q", r.operation)
	}
	w.events = append(w.events, portalEvent{kind: "response", token: r.pending})
	r.Step()
	w.events = append(w.events, portalEvent{kind: "method", token: r.pending})
	r.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: r.pending, devices: 1, clipboard: true, restore: restore})
	r.Step()
	if r.phase != "ready" || r.err != nil {
		t.Fatalf("restore did not reach ready: phase=%s err=%v", r.phase, r.err)
	}
}

func TestPortalRemoteQueriesScreenSaverOnce(t *testing.T) {
	r, w, _ := remoteFixture(t)
	if w.saverQueries != 1 {
		t.Fatalf("GetActive must be queried when the remote feature starts, got %d", w.saverQueries)
	}
	shield(w, true)
	r.Step()
	if r.shield != shieldActive {
		t.Fatal("GetActive/ActiveChanged(true) not recorded")
	}
	shield(w, false)
	r.Step()
	if r.shield != shieldInactive || r.phase != "idle" || len(w.requests) != 0 {
		t.Fatal("a shield change alone must not start an unwanted session")
	}
}

// Desktop level: Response 2 while the shield is active is "waiting", not
// "refused", the hotkey stays bound, and no Retry is offered.
func TestPortalRemoteInhibitedCreateWaitsWithoutRetry(t *testing.T) {
	d, w, h := boundDesktop(t)
	defer d.Close()
	shortcuts := d.hotkeys.session
	shield(&w.fakeRemoteWire, true)
	d.Step()
	requests := len(w.requests)
	w.events = append(w.events, portalEvent{kind: "response", token: d.remote.pending, code: 2})
	d.Step()
	if d.remote.phase != "waiting" || d.remote.err != nil {
		t.Fatalf("inhibited create must wait: phase=%s err=%v", d.remote.phase, d.remote.err)
	}
	s := d.DesktopStatus()
	if s.Retry || s.Ready || s.Message != d.remote.WaitMessage() || s.Message == "" {
		t.Fatalf("waiting must not offer Retry: %+v", s)
	}
	if _, err := d.Read(); err == nil || err.Error() != d.remote.WaitMessage() {
		t.Fatalf("clipboard must report the wait, got %v", err)
	}
	assertHotkeyStillWorks(t, d, w, h, shortcuts, requests)
	// Retry is a no-op while waiting: it must not create a second session.
	d.RetryDesktop()
	d.Step()
	if len(w.requests) != requests || d.remote.phase != "waiting" {
		t.Fatal("Retry while waiting must not start a session")
	}
	// Shield lifts: reconnect immediately, no Retry needed.
	shield(&w.fakeRemoteWire, false)
	d.Step()
	if d.remote.operation != "create" || len(w.requests) != requests+1 || w.requests[requests].iface != portalRemoteDesktop {
		t.Fatalf("ActiveChanged(false) must reconnect: operation=%q requests=%d", d.remote.operation, len(w.requests))
	}
	if w.binds != 1 || d.hotkeys.session != shortcuts {
		t.Fatal("reconnect disturbed the shortcuts")
	}
}

// Without the shield (or with no screen saver service) Response 2 is still a
// failure the user can retry; Response 1 is a refusal even under the shield.
func TestPortalRemoteResponseWithoutShieldStillFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		shield int
		code   uint32
	}{
		{"unknown-2", shieldUnknown, 2},
		{"inactive-2", shieldInactive, 2},
		{"active-cancelled-1", shieldActive, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, w, _ := remoteFixture(t)
			r.shield = tc.shield
			r.Authorize()
			r.Step()
			w.events = append(w.events, portalEvent{kind: "response", token: r.pending, code: tc.code})
			r.Step()
			if r.phase != "failed" || r.err == nil {
				t.Fatalf("phase=%s err=%v; want failed", r.phase, r.err)
			}
		})
	}
}

// Closed on a started session keeps the token and schedules a reconnect.
func TestPortalRemoteClosedKeepsTokenAndReconnects(t *testing.T) {
	r, w, now := remoteFixture(t)
	authorizeRemote(t, r, w)
	w.events = append(w.events, portalEvent{kind: "closed", session: inhibitSession})
	r.Step()
	if r.phase != "waiting" || r.err != nil || r.session != "" {
		t.Fatalf("Closed must wait to reconnect: phase=%s err=%v session=%q", r.phase, r.err, r.session)
	}
	for _, s := range w.closedSessions {
		if s == inhibitSession {
			t.Fatal("a session the portal closed must not be closed again")
		}
	}
	if token, err := r.client.tokens.Load(); err != nil || token != "new-private-token" {
		t.Fatalf("Closed must keep the restore token, got %q", token)
	}
	if _, err := r.clip.Read(); !errors.Is(err, errPortalNotAuthorized) {
		t.Fatal("clipboard must not be used while reconnecting")
	}
	requests := len(w.requests)
	*now = now.Add(500 * time.Millisecond)
	r.Step()
	if len(w.requests) != requests {
		t.Fatal("reconnect fired before the first backoff step")
	}
	*now = now.Add(600 * time.Millisecond)
	r.Step()
	answerRemoteCreate(t, r, w)
	sel, ok := lastSelect(w)
	if !ok || sel.value != "new-private-token" {
		t.Fatalf("reconnect must restore with the stored token, got %+v", sel)
	}
	finishRestore(t, r, w, "rotated-private-token")
	if token, _ := r.client.tokens.Load(); token != "rotated-private-token" {
		t.Fatal("token not rotated after the reconnect's Start")
	}
}

// The shield comes up (session closed), the reconnect is inhibited, then the
// shield lifts: the app reconnects at once, using the stored token.
func TestPortalRemoteShieldCycleRestoresWithStoredToken(t *testing.T) {
	r, w, now := remoteFixture(t)
	authorizeRemote(t, r, w)
	shield(w, true)
	w.events = append(w.events, portalEvent{kind: "closed", session: inhibitSession})
	r.Step()
	if r.phase != "waiting" || r.WaitMessage() != "Waiting for the screen to be unlocked to restore Wayland keyboard and clipboard access..." {
		t.Fatalf("shield up must wait for unlock: phase=%s msg=%q", r.phase, r.WaitMessage())
	}
	// Backoff retries while the shield stays up are inhibited again and grow.
	gaps := []time.Duration{}
	base := len(w.requests)
	for i := 0; i < 4; i++ {
		start := *now
		for len(w.requests) == base+i {
			*now = now.Add(250 * time.Millisecond)
			r.Step()
			if now.Sub(start) > 10*time.Minute {
				t.Fatal("no reconnect attempt scheduled")
			}
		}
		gaps = append(gaps, now.Sub(start))
		w.events = append(w.events, portalEvent{kind: "response", token: r.pending, code: 2})
		r.Step()
		if r.phase != "waiting" || r.err != nil {
			t.Fatalf("inhibited reconnect must keep waiting: phase=%s err=%v", r.phase, r.err)
		}
	}
	want := []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, time.Minute}
	for i := range want {
		if gaps[i] != want[i] {
			t.Fatalf("backoff %v, want %v", gaps, want)
		}
	}
	if token, _ := r.client.tokens.Load(); token != "new-private-token" {
		t.Fatal("inhibited reconnects must not touch the token")
	}
	requests := len(w.requests)
	shield(w, false)
	r.Step()
	if len(w.requests) != requests+1 || r.operation != "create" {
		t.Fatal("ActiveChanged(false) must reconnect without waiting for the backoff")
	}
	answerRemoteCreate(t, r, w)
	if sel, _ := lastSelect(w); sel.value != "new-private-token" {
		t.Fatalf("restore after unlock must use the stored token, got %q", sel.value)
	}
	finishRestore(t, r, w, "after-unlock-token")
	if r.attempt != 0 {
		t.Fatalf("shield lift must restart the backoff, attempt=%d", r.attempt)
	}
}

func TestPortalRemoteBackoffCapsAtFiveMinutes(t *testing.T) {
	r, w, now := remoteFixture(t)
	shield(w, true)
	r.Step()
	r.Authorize()
	r.Step()
	var last time.Duration
	for i := 0; i < 9; i++ {
		w.events = append(w.events, portalEvent{kind: "response", token: r.pending, code: 2})
		r.Step()
		last = r.retryAt.Sub(*now)
		*now = r.retryAt
		r.Step()
		if r.operation != "create" {
			t.Fatalf("backoff step %d did not reconnect", i)
		}
	}
	if last != 5*time.Minute {
		t.Fatalf("backoff must cap at 5 min, got %v", last)
	}
	// An explicit Retry after a real failure starts again from 1 s.
	w.events = append(w.events, portalEvent{kind: "response", token: r.pending, code: 1})
	r.Step()
	r.Authorize()
	if r.attempt != 0 {
		t.Fatal("Authorize must reset the backoff")
	}
}

// A session that stayed up for a while before closing restarts the backoff;
// one that closes right after Start keeps growing it, so a portal that closes
// every session at once cannot make the app reconnect every second.
func TestPortalRemoteFlappingSessionKeepsBackingOff(t *testing.T) {
	r, w, now := remoteFixture(t)
	authorizeRemote(t, r, w)
	w.events = append(w.events, portalEvent{kind: "closed", session: inhibitSession})
	r.Step()
	if got := r.retryAt.Sub(*now); got != time.Second {
		t.Fatalf("first reconnect after %v, want 1s", got)
	}
	*now = r.retryAt
	r.Step()
	answerRemoteCreate(t, r, w)
	finishRestore(t, r, w, "t2")
	w.events = append(w.events, portalEvent{kind: "closed", session: inhibitSession})
	r.Step()
	if got := r.retryAt.Sub(*now); got != 5*time.Second {
		t.Fatalf("immediate re-close must back off further, got %v", got)
	}
	*now = r.retryAt
	r.Step()
	answerRemoteCreate(t, r, w)
	finishRestore(t, r, w, "t3")
	*now = now.Add(2 * time.Minute)
	w.events = append(w.events, portalEvent{kind: "closed", session: inhibitSession})
	r.Step()
	if got := r.retryAt.Sub(*now); got != time.Second {
		t.Fatalf("a stable session must restart the backoff, got %v", got)
	}
}

// The token is replaced only after a successful Start. Every interrupted
// attempt leaves the previous token on disk for the next one.
func TestPortalRemoteTokenReplacedOnlyAfterStart(t *testing.T) {
	for _, stage := range []string{"select-refused", "start-refused", "start-no-keyboard", "timeout", "closed-mid-flow", "inhibited-mid-flow", "success"} {
		t.Run(stage, func(t *testing.T) {
			r, w, now := remoteFixture(t)
			if err := r.client.tokens.Save("kept-token"); err != nil {
				t.Fatal(err)
			}
			r.Authorize()
			r.Step()
			answerRemoteCreate(t, r, w)
			if sel, _ := lastSelect(w); sel.value != "kept-token" {
				t.Fatal("stored token not offered to SelectDevices")
			}
			switch stage {
			case "select-refused":
				w.events = append(w.events, portalEvent{kind: "response", token: r.pending, code: 1})
			case "timeout":
				*now = now.Add(4 * time.Minute)
			case "closed-mid-flow":
				w.events = append(w.events, portalEvent{kind: "closed", session: inhibitSession})
			case "inhibited-mid-flow":
				shield(w, true)
				w.events = append(w.events, portalEvent{kind: "response", token: r.pending, code: 2})
			default:
				w.events = append(w.events, portalEvent{kind: "response", token: r.pending})
				r.Step()
				w.events = append(w.events, portalEvent{kind: "method", token: r.pending})
				r.Step()
				switch stage {
				case "start-refused":
					w.events = append(w.events, portalEvent{kind: "response", token: r.pending, code: 2, restore: "must-not-save"})
				case "start-no-keyboard":
					w.events = append(w.events, portalEvent{kind: "response", token: r.pending, clipboard: true, restore: "must-not-save"})
				case "success":
					w.events = append(w.events, portalEvent{kind: "response", token: r.pending, devices: 1, clipboard: true, restore: "replacement"})
				}
			}
			r.Step()
			want := "kept-token"
			if stage == "success" {
				want = "replacement"
			}
			if token, err := r.client.tokens.Load(); err != nil || token != want {
				t.Fatalf("token=%q err=%v, want %q", token, err, want)
			}
			if stage == "inhibited-mid-flow" || stage == "closed-mid-flow" {
				if r.phase != "waiting" {
					t.Fatalf("phase=%s, want waiting", r.phase)
				}
				if stage == "inhibited-mid-flow" {
					found := false
					for _, s := range w.closedSessions {
						found = found || s == inhibitSession
					}
					if !found {
						t.Fatal("an inhibited mid-flow session must be closed by the app")
					}
				}
			}
		})
	}
}

// Shutdown while waiting is clean and a lost connection still fails.
func TestPortalRemoteWaitingStopsOnConnectionLoss(t *testing.T) {
	r, w, _ := remoteFixture(t)
	authorizeRemote(t, r, w)
	w.events = append(w.events, portalEvent{kind: "closed", session: inhibitSession}, portalEvent{kind: "failed"})
	r.Step()
	if r.phase != "failed" || r.err == nil {
		t.Fatalf("connection loss while waiting must fail: phase=%s err=%v", r.phase, r.err)
	}
	if token, _ := r.client.tokens.Load(); token != "new-private-token" {
		t.Fatal("connection loss must keep the token")
	}
}
