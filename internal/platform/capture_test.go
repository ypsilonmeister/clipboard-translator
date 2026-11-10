package platform

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// captureEnv is a scripted desktop for captureWith: a clipboard, a sequence
// counter, a foreground window and the hooks that let a test change them at the
// moment the real code would be waiting or injecting keys.
type captureEnv struct {
	clip      *testClipboard
	seq       uint32
	fg        uintptr
	copies    int
	onWait    func(e *captureEnv) bool // runs while "waiting" for the modifiers
	onCopy    func(e *captureEnv)      // what the target app does with Ctrl+C
	copyError error
}

func (e *captureEnv) ops() captureOps {
	return captureOps{
		foreground:  func() uintptr { return e.fg },
		modifiersUp: func() bool { return e.onWait == nil || e.onWait(e) },
		sequence:    func() uint32 { return e.seq },
		sendCopy: func() error {
			e.copies++
			if e.onCopy != nil {
				e.onCopy(e)
			}
			return e.copyError
		},
		modifierTimeout: 50 * time.Millisecond,
		copyTimeout:     50 * time.Millisecond,
	}
}

// copySelection makes Ctrl+C put text on the clipboard.
func copySelection(text string) func(*captureEnv) {
	return func(e *captureEnv) { e.clip.text = text; e.seq++ }
}

func TestCaptureReadsFreshSelection(t *testing.T) {
	e := &captureEnv{clip: &testClipboard{text: "old"}, fg: 7, onCopy: copySelection("selected")}
	got, fresh, err := captureWith(e.clip, 7, e.ops())
	if err != nil || got != "selected" || !fresh || e.copies != 1 {
		t.Fatalf("got=%q fresh=%v err=%v copies=%d", got, fresh, err, e.copies)
	}
}

func TestCaptureFallsBackToClipboardWhenNothingSelected(t *testing.T) {
	e := &captureEnv{clip: &testClipboard{text: "copied earlier"}, fg: 7}
	got, fresh, err := captureWith(e.clip, 7, e.ops())
	if err != nil || got != "copied earlier" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v", got, fresh, err)
	}
	// Keys that cannot be injected degrade the same way.
	e = &captureEnv{clip: &testClipboard{text: "copied earlier"}, fg: 7, copyError: errors.New("blocked")}
	if got, fresh, err = captureWith(e.clip, 7, e.ops()); err != nil || got != "copied earlier" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v", got, fresh, err)
	}
}

// Issue #22: modifiers held past the timeout must not translate stale text.
func TestCaptureAbortsWhenModifiersStayHeld(t *testing.T) {
	e := &captureEnv{clip: &testClipboard{text: "stale"}, fg: 7, onWait: func(*captureEnv) bool { return false }}
	got, fresh, err := captureWith(e.clip, 7, e.ops())
	if !errors.Is(err, ErrCaptureModifiersHeld) || got != "" || fresh || e.copies != 0 {
		t.Fatalf("got=%q fresh=%v err=%v copies=%d", got, fresh, err, e.copies)
	}
}

// Issue #22: focus moving during the modifier wait must not send Ctrl+C to the new window.
func TestCaptureAbortsWhenFocusChangesDuringModifierWait(t *testing.T) {
	e := &captureEnv{clip: &testClipboard{text: "stale"}, fg: 7, onCopy: copySelection("other window's text"),
		onWait: func(e *captureEnv) bool { e.fg = 8; return true }}
	got, _, err := captureWith(e.clip, 7, e.ops())
	if !errors.Is(err, ErrCaptureFocusChanged) || got != "" || e.copies != 0 {
		t.Fatalf("got=%q err=%v copies=%d", got, err, e.copies)
	}
}

// Issue #22: a clipboard update during the modifier wait is not a selection.
func TestCaptureSequenceIsMeasuredAcrossTheCopy(t *testing.T) {
	e := &captureEnv{clip: &testClipboard{text: "old"}, fg: 7,
		onWait: func(e *captureEnv) bool { e.clip.text = "changed meanwhile"; e.seq += 5; return true }}
	got, fresh, err := captureWith(e.clip, 7, e.ops())
	if err != nil || fresh || got != "changed meanwhile" || e.copies != 1 {
		t.Fatalf("got=%q fresh=%v err=%v copies=%d", got, fresh, err, e.copies)
	}
}

type fakeClock struct {
	now        time.Time
	totalSleep time.Duration
	sleeps     int
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) time() time.Time {
	return c.now
}

func (c *fakeClock) sleep(d time.Duration) {
	c.sleeps++
	c.totalSleep += d
	c.now = c.now.Add(d)
}

type timedClipboard struct {
	testClipboard
	readTimes []time.Time
	onRead    func()
	clock     *fakeClock
}

func (c *timedClipboard) Read() (string, error) {
	if c.clock != nil {
		c.readTimes = append(c.readTimes, c.clock.time())
	}
	if c.onRead != nil {
		c.onRead()
	}
	return c.testClipboard.Read()
}

func TestCaptureSettlesBeforeReading(t *testing.T) {
	// A clipboard that changes twice in quick succession is read only after
	// a full settle interval following the last sequence change.
	clk := newFakeClock()
	start := clk.time()
	settleTime := 40 * time.Millisecond
	copyTimeout := 200 * time.Millisecond
	lastChange := 30 * time.Millisecond

	clip := &timedClipboard{
		testClipboard: testClipboard{text: "old"},
		clock:         clk,
	}

	ops := captureOps{
		foreground:  func() uintptr { return 7 },
		modifiersUp: func() bool { return true },
		sequence: func() uint32 {
			elapsed := clk.time().Sub(start)
			if elapsed >= lastChange {
				clip.text = "second"
				return 2
			}
			if clk.sleeps > 0 || clip.text == "first" {
				return 1
			}
			return 0
		},
		sendCopy: func() error {
			clip.text = "first"
			return nil
		},
		modifierTimeout: time.Second,
		copyTimeout:     copyTimeout,
		settleTime:      settleTime,
		now:             clk.time,
		sleep:           clk.sleep,
	}

	got, fresh, err := captureWith(clip, 7, ops)
	if err != nil || got != "second" || !fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want 'second' read after second change", got, fresh, err)
	}

	// The first read snapshots old text before sendCopy; the second is the post-settle read.
	if len(clip.readTimes) != 2 {
		t.Fatalf("read count = %d, want 2", len(clip.readTimes))
	}
	finalRead := clip.readTimes[1]
	lastChangeTime := start.Add(lastChange)
	if finalRead.Sub(lastChangeTime) < settleTime {
		t.Fatalf("final read happened at %v, only %v after last change at %v; want at least full settle interval %v",
			finalRead.Sub(start), finalRead.Sub(lastChangeTime), lastChange, settleTime)
	}
}

func TestCaptureNeverSettlesReadsAtDeadline(t *testing.T) {
	// A clipboard that keeps changing without settling is read exactly at the copy deadline.
	clk := newFakeClock()
	start := clk.time()
	settleTime := 50 * time.Millisecond
	copyTimeout := 40 * time.Millisecond

	clip := &timedClipboard{
		testClipboard: testClipboard{text: "old"},
		clock:         clk,
	}

	seqStep := uint32(0)
	ops := captureOps{
		foreground:  func() uintptr { return 7 },
		modifiersUp: func() bool { return true },
		sequence: func() uint32 {
			seqStep++
			clip.text = fmt.Sprintf("unsettled-%d", seqStep)
			return seqStep
		},
		sendCopy: func() error {
			return nil
		},
		modifierTimeout: time.Second,
		copyTimeout:     copyTimeout,
		settleTime:      settleTime,
		now:             clk.time,
		sleep:           clk.sleep,
	}

	got, fresh, err := captureWith(clip, 7, ops)
	if err != nil || !fresh || !strings.HasPrefix(got, "unsettled-") {
		t.Fatalf("got=%q fresh=%v err=%v; want unsettled text read at deadline", got, fresh, err)
	}
	if len(clip.readTimes) != 2 {
		t.Fatalf("read count = %d, want 2", len(clip.readTimes))
	}
	finalRead := clip.readTimes[1]
	wantDeadline := start.Add(copyTimeout)
	if !finalRead.Equal(wantDeadline) {
		t.Fatalf("final read at %v; want read exactly at deadline %v", finalRead.Sub(start), copyTimeout)
	}
}

func TestCaptureZeroSettleTimeNoExtraSleep(t *testing.T) {
	// With settleTime <= 0, the fresh selection is read immediately without extra sleep.
	clk := newFakeClock()
	start := clk.time()
	copyTimeout := 50 * time.Millisecond

	clip := &timedClipboard{
		testClipboard: testClipboard{text: "old"},
		clock:         clk,
	}

	seq := uint32(0)
	ops := captureOps{
		foreground:  func() uintptr { return 7 },
		modifiersUp: func() bool { return true },
		sequence:    func() uint32 { return seq },
		sendCopy: func() error {
			seq = 1
			clip.text = "fresh selection"
			return nil
		},
		modifierTimeout: time.Second,
		copyTimeout:     copyTimeout,
		settleTime:      0, // settleTime <= 0
		now:             clk.time,
		sleep:           clk.sleep,
	}

	got, fresh, err := captureWith(clip, 7, ops)
	if err != nil || got != "fresh selection" || !fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want 'fresh selection'", got, fresh, err)
	}
	if clk.sleeps != 0 || clk.totalSleep != 0 {
		t.Fatalf("sleeps=%d, totalSleep=%v; want no extra sleep with settleTime <= 0", clk.sleeps, clk.totalSleep)
	}
	if len(clip.readTimes) != 2 || !clip.readTimes[1].Equal(start) {
		t.Fatalf("final read at %v, want exactly %v", clip.readTimes, start)
	}
}

func TestCaptureNoExtraDelayWhenSequenceNeverChanges(t *testing.T) {
	// When the sequence never changes, falls back to the old clipboard text
	// at the copy deadline without waiting for settleTime.
	clk := newFakeClock()
	start := clk.time()
	settleTime := 200 * time.Millisecond
	copyTimeout := 30 * time.Millisecond

	clip := &timedClipboard{
		testClipboard: testClipboard{text: "old clipboard"},
		clock:         clk,
	}

	ops := captureOps{
		foreground:      func() uintptr { return 7 },
		modifiersUp:     func() bool { return true },
		sequence:        func() uint32 { return 0 }, // never changes
		sendCopy:        func() error { return nil },
		modifierTimeout: time.Second,
		copyTimeout:     copyTimeout,
		settleTime:      settleTime,
		now:             clk.time,
		sleep:           clk.sleep,
	}

	got, fresh, err := captureWith(clip, 7, ops)
	if err != nil || got != "old clipboard" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want fallback to old clipboard", got, fresh, err)
	}
	elapsed := clk.time().Sub(start)
	if elapsed != copyTimeout {
		t.Fatalf("elapsed=%v; want exactly copyTimeout (%v)", elapsed, copyTimeout)
	}
	if clk.totalSleep >= settleTime {
		t.Fatalf("totalSleep=%v; sequence never changed must not wait for settleTime (%v)", clk.totalSleep, settleTime)
	}
}

func TestCaptureAbortsWhenFocusChangesDuringSettling(t *testing.T) {
	// Focus changes during settling (and a second sequence change occurs from
	// copying in the other window). Capture must abort with errCaptureFocusChanged
	// and not return the other window's text.
	clk := newFakeClock()
	settleTime := 50 * time.Millisecond
	copyTimeout := 200 * time.Millisecond

	fg := uintptr(7)
	seq := uint32(0)
	clip := &timedClipboard{
		testClipboard: testClipboard{text: "original"},
		clock:         clk,
	}

	ops := captureOps{
		foreground:  func() uintptr { return fg },
		modifiersUp: func() bool { return true },
		sequence:    func() uint32 { return seq },
		sendCopy: func() error {
			seq = 1
			clip.text = "selection from win 7"
			return nil
		},
		modifierTimeout: time.Second,
		copyTimeout:     copyTimeout,
		settleTime:      settleTime,
		now:             clk.time,
		sleep: func(d time.Duration) {
			clk.sleep(d)
			// User switches to window 8 during the settle wait and copies text there
			fg = 8
			seq = 2
			clip.text = "text copied in window 8"
		},
	}

	got, fresh, err := captureWith(clip, 7, ops)
	if !errors.Is(err, ErrCaptureFocusChanged) || got != "" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want errCaptureFocusChanged and empty text", got, fresh, err)
	}
}

func TestCaptureAbortsWhenFocusChangesAfterFinalRead(t *testing.T) {
	// Focus changes right after the final clipboard read completes (between read
	// and return). Capture must abort with errCaptureFocusChanged and not return text.
	clk := newFakeClock()
	fg := uintptr(7)
	seq := uint32(0)

	clip := &timedClipboard{
		testClipboard: testClipboard{text: "original"},
		clock:         clk,
	}
	clip.onRead = func() {
		if seq > 0 {
			// During the final read of the fresh selection, focus moves away.
			fg = 8
		}
	}

	ops := captureOps{
		foreground:  func() uintptr { return fg },
		modifiersUp: func() bool { return true },
		sequence:    func() uint32 { return seq },
		sendCopy: func() error {
			seq = 1
			clip.text = "selection"
			return nil
		},
		modifierTimeout: time.Second,
		copyTimeout:     50 * time.Millisecond,
		settleTime:      0,
		now:             clk.time,
		sleep:           clk.sleep,
	}

	got, fresh, err := captureWith(clip, 7, ops)
	if !errors.Is(err, ErrCaptureFocusChanged) || got != "" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want errCaptureFocusChanged and empty text", got, fresh, err)
	}
}

func TestCaptureWithPrimaryOwnerMatches(t *testing.T) {
	cb := &testClipboard{text: "existing"}
	ops := primaryOps{
		selectedIn:  func(win uintptr) bool { return win == 10 },
		readPrimary: func() string { return "fresh selection" },
	}
	got, fresh, err := captureWithPrimary(cb, 10, ops)
	if err != nil || got != "fresh selection" || !fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want fresh selection", got, fresh, err)
	}
	if cb.text != "fresh selection" || cb.writes != 1 {
		t.Fatalf("selection must be staged on clipboard: text=%q writes=%d", cb.text, cb.writes)
	}
}

func TestCaptureWithPrimaryOtherWindowKeepsTextButIsNotFresh(t *testing.T) {
	cb := &testClipboard{text: "existing clipboard"}
	ops := primaryOps{
		// Selection is owned by window 5, but window 10 is active. The owner check
		// is a heuristic, so the text is still translated; it just must not auto-paste.
		selectedIn:  func(win uintptr) bool { return false },
		readPrimary: func() string { return "selection from window 5" },
	}
	got, fresh, err := captureWithPrimary(cb, 10, ops)
	if err != nil || got != "selection from window 5" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want the selection text, not fresh", got, fresh, err)
	}
	if cb.text != "selection from window 5" || cb.writes != 1 {
		t.Fatalf("the selection must be staged on the clipboard: text=%q writes=%d", cb.text, cb.writes)
	}
}

func TestCaptureWithPrimaryOwnerMatchesEmptyText(t *testing.T) {
	cb := &testClipboard{text: "existing clipboard"}
	ops := primaryOps{
		selectedIn:  func(win uintptr) bool { return true },
		readPrimary: func() string { return "" },
	}
	got, fresh, err := captureWithPrimary(cb, 10, ops)
	if err != nil || got != "existing clipboard" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want empty selection falling back to clipboard", got, fresh, err)
	}
	if cb.writes != 0 {
		t.Fatalf("clipboard must not be written when primary is empty: writes=%d", cb.writes)
	}
}

func TestCaptureWithPrimaryZeroWindow(t *testing.T) {
	cb := &testClipboard{text: "existing clipboard"}
	ops := primaryOps{
		selectedIn:  func(win uintptr) bool { return true },
		readPrimary: func() string { return "selection" },
	}
	got, fresh, err := captureWithPrimary(cb, 0, ops)
	if err != nil || got != "selection" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want the selection text, not fresh, when the window is unknown", got, fresh, err)
	}
}

type failingWriteClipboard struct {
	testClipboard
}

func (c *failingWriteClipboard) Write(string) error {
	return errors.New("write failed")
}

func TestCaptureWithPrimaryWriteError(t *testing.T) {
	cb := &failingWriteClipboard{testClipboard: testClipboard{text: "existing"}}
	ops := primaryOps{
		selectedIn:  func(win uintptr) bool { return true },
		readPrimary: func() string { return "selection" },
	}
	got, fresh, err := captureWithPrimary(cb, 10, ops)
	if err == nil || got != "" || fresh {
		t.Fatalf("got=%q fresh=%v err=%v; want write error", got, fresh, err)
	}
}
