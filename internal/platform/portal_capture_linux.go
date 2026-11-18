package platform

import (
	"errors"
	"time"
)

// Wayland cannot identify the focused window. Always report fromSelection=false:
// the application must never auto-paste a result captured by this task.
type portalCapture struct {
	remote                           *portalRemote
	released                         func() bool
	phase                            string
	session, old                     string
	deadline, releaseAt, stableSince time.Time
	before, last                     uint32
	keys                             *portalKeys
	fallback                         CaptureFallback
}

// portalCaptureReconnectWait bounds how long a capture waits for a reconnecting
// RemoteDesktop session (design memo §2, P1-7).
const portalCaptureReconnectWait = 2 * time.Second

// beginPortalCapture starts a capture. If the session is down and reconnecting
// (screen shield, Closed, backoff), it starts the reconnect at once and parks the
// job in the "reconnect" phase for at most portalCaptureReconnectWait. When the
// session returns in time the capture proceeds normally; otherwise it fails with
// errPortalNotAuthorized. The job is polled, so nothing is left running.
func beginPortalCapture(r *portalRemote, released func() bool) CaptureTask {
	now := r.client.now()
	j := &portalCapture{remote: r, released: released, session: r.session, phase: "modifiers", deadline: now.Add(time.Second)}
	if r.reconnecting() {
		j.phase = "reconnect"
		j.deadline = now.Add(portalCaptureReconnectWait)
		r.reconnectNow()
	}
	return j
}
func (j *portalCapture) Fallback() CaptureFallback { return j.fallback }
func (j *portalCapture) Wait() time.Duration       { return capturePollStep }
func (j *portalCapture) Step() (bool, string, bool, error) {
	r := j.remote
	r.Step()
	now := r.client.now()
	if j.phase == "failed" {
		return true, "", false, errPortalNotAuthorized
	}
	if j.phase == "reconnect" {
		switch {
		case r.phase == "ready" && r.session != "":
			j.session = r.session
			j.phase = "modifiers"
			j.deadline = now.Add(time.Second)
			return false, "", false, nil
		case !r.reconnecting():
			j.phase = "failed"
			return true, "", false, errPortalNotAuthorized // reconnect failed outright
		case !now.Before(j.deadline):
			j.phase = "failed"
			return true, "", false, errPortalNotAuthorized // bound expired; reconnect continues in the background
		}
		return false, "", false, nil
	}
	if r.phase != "ready" || r.session != j.session {
		return true, "", false, errPortalNotAuthorized
	}
	switch j.phase {
	case "modifiers":
		if !j.released() {
			j.releaseAt = time.Time{}
			if !now.Before(j.deadline) {
				return true, "", false, ErrCaptureModifiersHeld
			}
			return false, "", false, nil
		}
		if j.releaseAt.IsZero() {
			j.releaseAt = now.Add(150 * time.Millisecond)
		}
		if now.Before(j.releaseAt) {
			return false, "", false, nil
		}
		j.phase = "old"
		j.deadline = now.Add(2 * time.Second)
		fallthrough
	case "old":
		old, err := r.clip.Read()
		if errors.Is(err, errPortalPending) {
			if now.Before(j.deadline) {
				return false, "", false, nil
			}
			return true, "", false, errors.New("Wayland clipboard read timed out")
		}
		if errors.Is(err, errPortalNoText) {
			err = nil
			old = ""
		}
		if err != nil {
			return true, "", false, err
		}
		j.old = old
		j.before = r.clip.seq
		j.last = j.before
		j.keys = r.beginKeys('c')
		j.phase = "keys"
	case "keys":
		if !j.keys.done {
			return false, "", false, nil
		}
		if j.keys.err != nil {
			return true, "", false, j.keys.err
		}
		j.phase = "copy"
		j.deadline = now.Add(2 * time.Second)
	case "copy":
		seq := r.clip.seq
		if seq == j.before {
			if !now.Before(j.deadline) {
				j.fallback = FallbackNoCopy // Ctrl+C changed nothing: the old clipboard text is used
				return true, j.old, false, nil
			}
			return false, "", false, nil
		}
		if seq != j.last {
			j.last = seq
			j.stableSince = now
		}
		if now.Sub(j.stableSince) < 80*time.Millisecond && now.Before(j.deadline) {
			return false, "", false, nil
		}
		text, err := r.clip.Read()
		if errors.Is(err, errPortalPending) {
			if now.Before(j.deadline) {
				return false, "", false, nil
			}
			return true, "", false, errors.New("Wayland clipboard read timed out")
		}
		return true, text, false, err
	}
	return false, "", false, nil
}
