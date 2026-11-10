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
}

func beginPortalCapture(r *portalRemote, released func() bool) CaptureTask {
	return &portalCapture{remote: r, released: released, session: r.session, phase: "modifiers", deadline: r.client.now().Add(time.Second)}
}
func (j *portalCapture) Wait() time.Duration { return capturePollStep }
func (j *portalCapture) Step() (bool, string, bool, error) {
	r := j.remote
	r.Step()
	now := r.client.now()
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
