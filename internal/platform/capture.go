package platform

import (
	"errors"
	"time"
)

// Why a selection could not be captured. startTranslation tells the user so
// instead of silently translating whatever happens to be on the clipboard.
var (
	ErrCaptureModifiersHeld = errors.New("the hotkey's modifier keys were still held down")
	ErrCaptureFocusChanged  = errors.New("the focused window changed before the selection could be copied")
)

// CaptureFallback says why a capture ended on the text that was already on the
// clipboard instead of a fresh selection. The text is still translated (the
// user may have copied it on purpose); the app only has to say so, because a
// copy that silently did not happen is otherwise indistinguishable from one
// that did. It is deliberately separate from fromSelection: an X11 PRIMARY
// selection made in another window is not fresh either, yet it is a selection.
type CaptureFallback int

const (
	FallbackNone     CaptureFallback = iota // a selection was captured (fresh or not)
	FallbackNoCopy                          // the copy changed nothing (or could not be sent): the old clipboard text is used
	FallbackElevated                        // the window runs with a higher integrity level, so Ctrl+C was not sent
)

// captureOps are the OS pieces of selection capture, so the ordering rules in
// captureJob can be tested without a desktop.
type captureOps struct {
	foreground      func() uintptr
	modifiersUp     func() bool   // true when Ctrl/Alt/Shift/Win are all up
	sequence        func() uint32 // clipboard sequence number
	sendCopy        func() error  // synthetic Ctrl+C
	modifierTimeout time.Duration
	copyTimeout     time.Duration
	settleTime      time.Duration
	now             func() time.Time
	sleep           func(time.Duration)    // only captureWith sleeps; the app polls instead
	elevated        func(win uintptr) bool // optional: win cannot receive our synthetic keys (UIPI)
}

// CaptureTask is a selection capture in progress. The UI thread calls step
// until it reports done, waiting about Wait() in between while it keeps pumping
// messages, so a slow copy never freezes the tray or the other hotkeys.
type CaptureTask interface {
	Step() (done bool, text string, fromSelection bool, err error)
	Wait() time.Duration
	// Fallback is read once Step has reported done: whether the text is the old
	// clipboard text because no selection was copied. FallbackElevated is also
	// reported together with an error (the clipboard could not be read).
	Fallback() CaptureFallback
}

// captureJob copies the selection of window win and reports the clipboard text
// and whether it came from a fresh selection (Ctrl+C changed the clipboard).
// When Ctrl+C changes nothing it returns the existing clipboard text with
// fromSelection false, which is the "translate what I copied" path.
//
// Order matters: modifiers are awaited first, and only then are the old text
// and the sequence number snapshotted and the focus checked, immediately before
// the keys are injected. A hotkey whose modifiers stay held, or whose window
// lost focus meanwhile, aborts instead of copying into another window or
// falling back to unrelated clipboard text.
type captureJob struct {
	cb  Clipboard
	win uintptr
	ops captureOps

	fallback    CaptureFallback
	elevChecked bool
	copying     bool // Ctrl+C has been sent
	modDeadline time.Time
	deadline    time.Time // for the copy
	old         string    // the clipboard text before Ctrl+C
	before      uint32    // the sequence number before Ctrl+C
	lastSeq     uint32
	stableSince time.Time
}

const capturePollStep = 15 * time.Millisecond

func newCaptureJob(cb Clipboard, win uintptr, ops captureOps) *captureJob {
	if ops.now == nil {
		ops.now = time.Now
	}
	if ops.sleep == nil {
		ops.sleep = time.Sleep
	}
	return &captureJob{cb: cb, win: win, ops: ops, modDeadline: ops.now().Add(ops.modifierTimeout)}
}

// Fallback implements CaptureTask.
func (j *captureJob) Fallback() CaptureFallback { return j.fallback }

func (j *captureJob) focusLost() bool {
	return j.ops.foreground != nil && j.ops.foreground() != j.win
}

// step does whatever can be done now. It never sleeps itself (opening a
// clipboard another program holds may retry briefly, as every Read does).
func (j *captureJob) Step() (done bool, text string, fromSelection bool, err error) {
	if !j.copying {
		if !j.elevChecked {
			j.elevChecked = true
			if j.ops.elevated != nil && j.ops.elevated(j.win) {
				// The keys we would send are dropped silently: do not wait for a copy
				// that cannot happen, use what the user copied themselves. The mark
				// is set before the read: a clipboard without text is still a press
				// in an administrator window, and the app says so.
				j.fallback = FallbackElevated
				old, err := j.cb.Read()
				if err != nil {
					return true, "", false, err
				}
				return true, old, false, nil
			}
		}
		if !j.ops.modifiersUp() {
			if !j.ops.now().Before(j.modDeadline) {
				return true, "", false, ErrCaptureModifiersHeld
			}
			return false, "", false, nil
		}
		old, err := j.cb.Read()
		if err != nil {
			return true, "", false, err
		}
		j.before = j.ops.sequence()
		if j.focusLost() {
			return true, "", false, ErrCaptureFocusChanged
		}
		if err := j.ops.sendCopy(); err != nil {
			j.fallback = FallbackNoCopy
			return true, old, false, nil // cannot inject keys: fall back to the existing clipboard text
		}
		j.copying, j.old, j.lastSeq = true, old, j.before
		j.deadline = j.ops.now().Add(j.ops.copyTimeout)
		// Fall through: a copy that is already done is picked up without a wait.
	}
	if j.focusLost() {
		return true, "", false, ErrCaptureFocusChanged
	}
	now := j.ops.now()
	seq := j.ops.sequence()
	if seq == j.before {
		if !now.Before(j.deadline) {
			j.fallback = FallbackNoCopy
			return true, j.old, false, nil
		}
		return false, "", false, nil
	}
	if seq != j.lastSeq {
		j.lastSeq = seq
		j.stableSince = now
	}
	if j.ops.settleTime <= 0 || now.Sub(j.stableSince) >= j.ops.settleTime || !now.Before(j.deadline) {
		picked, err := j.cb.Read()
		if err != nil {
			return true, "", false, err
		}
		if j.focusLost() {
			return true, "", false, ErrCaptureFocusChanged
		}
		return true, picked, true, nil
	}
	return false, "", false, nil
}

// wait is how long until the next step can tell something new: the poll step,
// shortened so that the copy deadline and the end of the settle time are hit
// exactly.
func (j *captureJob) Wait() time.Duration {
	d := capturePollStep
	if !j.copying {
		return d
	}
	now := j.ops.now()
	if rem := j.deadline.Sub(now); rem > 0 && rem < d {
		d = rem
	}
	if j.ops.settleTime > 0 && j.lastSeq != j.before {
		if rem := j.ops.settleTime - now.Sub(j.stableSince); rem > 0 && rem < d {
			d = rem
		}
	}
	return d
}

// captureWith runs a captureJob to the end, sleeping between steps.
func captureWith(cb Clipboard, win uintptr, ops captureOps) (text string, fromSelection bool, err error) {
	j := newCaptureJob(cb, win, ops)
	for {
		done, text, fresh, err := j.Step()
		if done {
			return text, fresh, err
		}
		j.ops.sleep(j.Wait())
	}
}

// runCapture runs a capture task to the end on a real clock.
func runCapture(t CaptureTask) (string, bool, error) {
	for {
		done, text, fresh, err := t.Step()
		if done {
			return text, fresh, err
		}
		time.Sleep(t.Wait())
	}
}

// primaryOps are the OS pieces of X11 PRIMARY selection capture.
type primaryOps struct {
	selectedIn  func(win uintptr) bool // was PRIMARY last changed while win had the focus?
	readPrimary func() string
}

// captureWithPrimary reads the X11 PRIMARY selection and stages its text onto
// the clipboard. The selection counts as fresh (fromSelection true, so
// auto-paste may fire) only when it was made while win, the window the hotkey
// was pressed in, had the focus. If PRIMARY was last changed somewhere else, or
// that cannot be told, the text is still translated but reported as not fresh:
// PRIMARY may be left over from a window the user is no longer in, and
// auto-paste must never act on it. That is still a selection, so it is not a
// fallback.
//
// Translating the text regardless keeps this safe if the freshness test is
// wrong: dropping the selection would translate the stale clipboard text
// instead of what the user just selected. If PRIMARY is empty, the existing
// clipboard text is used and the result is a FallbackNoCopy.
func captureWithPrimary(cb Clipboard, win uintptr, ops primaryOps) (string, bool, CaptureFallback, error) {
	text := ""
	if ops.readPrimary != nil {
		text = ops.readPrimary()
	}
	if text == "" {
		text, err := cb.Read()
		return text, false, FallbackNoCopy, err
	}
	if err := cb.Write(text); err != nil {
		return "", false, FallbackNone, err
	}
	fresh := win != 0 && ops.selectedIn != nil && ops.selectedIn(win)
	return text, fresh, FallbackNone, nil
}

// primaryCapture is captureWithPrimary as an already-finished CaptureTask, so
// X11 reports the fallback through the same interface as the other platforms.
type primaryCapture struct {
	text     string
	fresh    bool
	fallback CaptureFallback
	err      error
}

func newPrimaryCapture(cb Clipboard, win uintptr, ops primaryOps) *primaryCapture {
	c := &primaryCapture{}
	c.text, c.fresh, c.fallback, c.err = captureWithPrimary(cb, win, ops)
	return c
}

func (c *primaryCapture) Step() (bool, string, bool, error) { return true, c.text, c.fresh, c.err }
func (c *primaryCapture) Wait() time.Duration               { return 0 }
func (c *primaryCapture) Fallback() CaptureFallback         { return c.fallback }
