package platform

import (
	"errors"
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
		done, text, fresh, err := j.Step()
		if done {
			if err != nil || text != "slow page selection" || !fresh {
				t.Fatalf("text=%q fresh=%v err=%v", text, fresh, err)
			}
			break
		}
		w := j.Wait()
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
		done, text, _, err := j.Step()
		if done {
			if !errors.Is(err, ErrCaptureModifiersHeld) || text != "" || copies != 0 {
				t.Fatalf("text=%q err=%v copies=%d", text, err, copies)
			}
			return
		}
		clk.now = clk.now.Add(j.Wait())
	}
}

// runJob steps j on a fake clock until it is done.
func runJob(t *testing.T, j *captureJob, clk *fakeClock) (text string, fresh bool, err error) {
	t.Helper()
	for i := 0; i < 5000; i++ {
		done, text, fresh, err := j.Step()
		if done {
			return text, fresh, err
		}
		clk.now = clk.now.Add(j.Wait())
	}
	t.Fatal("the capture never finished")
	return
}

func baseOps(clk *fakeClock) captureOps {
	return captureOps{
		foreground:      func() uintptr { return 7 },
		modifiersUp:     func() bool { return true },
		sequence:        func() uint32 { return 0 },
		sendCopy:        func() error { return nil },
		modifierTimeout: 900 * time.Millisecond,
		copyTimeout:     2 * time.Second,
		settleTime:      80 * time.Millisecond,
		now:             clk.time,
		sleep:           func(time.Duration) { panic("a capture job must not sleep") },
	}
}

func TestCaptureJobReportsNoCopyFallback(t *testing.T) {
	clk := newFakeClock()
	ops := baseOps(clk) // Ctrl+C changes nothing
	j := newCaptureJob(&testClipboard{text: "older text"}, 7, ops)
	text, fresh, err := runJob(t, j, clk)
	if err != nil || text != "older text" || fresh || j.Fallback() != FallbackNoCopy {
		t.Fatalf("text=%q fresh=%v err=%v fallback=%v", text, fresh, err, j.Fallback())
	}
}

func TestCaptureJobReportsNoCopyFallbackWhenKeysCannotBeSent(t *testing.T) {
	clk := newFakeClock()
	ops := baseOps(clk)
	ops.sendCopy = func() error { return errors.New("blocked") }
	j := newCaptureJob(&testClipboard{text: "older text"}, 7, ops)
	text, fresh, err := runJob(t, j, clk)
	if err != nil || text != "older text" || fresh || j.Fallback() != FallbackNoCopy {
		t.Fatalf("text=%q fresh=%v err=%v fallback=%v", text, fresh, err, j.Fallback())
	}
}

func TestCaptureJobNoFallbackWhenCopySucceeds(t *testing.T) {
	clk := newFakeClock()
	clip := &testClipboard{text: "old"}
	seq := uint32(0)
	ops := baseOps(clk)
	ops.sequence = func() uint32 { return seq }
	ops.sendCopy = func() error { seq, clip.text = 1, "selected"; return nil }
	j := newCaptureJob(clip, 7, ops)
	text, fresh, err := runJob(t, j, clk)
	if err != nil || text != "selected" || !fresh || j.Fallback() != FallbackNone {
		t.Fatalf("text=%q fresh=%v err=%v fallback=%v", text, fresh, err, j.Fallback())
	}
}

func TestCaptureJobElevatedWindowSkipsTheCopyAndTheWait(t *testing.T) {
	clk := newFakeClock()
	start := clk.time()
	ops := baseOps(clk)
	ops.modifiersUp = func() bool { return false } // still held: must not matter
	copies := 0
	ops.sendCopy = func() error { copies++; return nil }
	ops.elevated = func(win uintptr) bool { return win == 7 }
	j := newCaptureJob(&testClipboard{text: "copied by hand"}, 7, ops)
	done, text, fresh, err := j.Step()
	if !done || err != nil || text != "copied by hand" || fresh || j.Fallback() != FallbackElevated {
		t.Fatalf("done=%v text=%q fresh=%v err=%v fallback=%v", done, text, fresh, err, j.Fallback())
	}
	if copies != 0 || !clk.time().Equal(start) {
		t.Fatalf("copies=%d, waited %v; want none", copies, clk.time().Sub(start))
	}
}

func TestWindowMoreElevated(t *testing.T) {
	const medium, high = 0x2000, 0x3000
	self := func() (uint32, bool) { return medium, true }
	cases := []struct {
		name string
		win  uintptr
		ops  integrityOps
		want bool
	}{
		{"higher", 5, integrityOps{func(uintptr) (uint32, bool, bool) { return high, false, true }, self}, true},
		{"same", 5, integrityOps{func(uintptr) (uint32, bool, bool) { return medium, false, true }, self}, false},
		{"lower", 5, integrityOps{func(uintptr) (uint32, bool, bool) { return 0x1000, false, true }, self}, false},
		{"access denied counts as higher", 5, integrityOps{func(uintptr) (uint32, bool, bool) { return 0, true, false }, self}, true},
		{"window gone", 5, integrityOps{func(uintptr) (uint32, bool, bool) { return 0, false, false }, self}, false},
		{"own level unknown", 5, integrityOps{func(uintptr) (uint32, bool, bool) { return high, false, true }, func() (uint32, bool) { return 0, false }}, false},
		{"no window", 0, integrityOps{func(uintptr) (uint32, bool, bool) { return high, false, true }, self}, false},
	}
	for _, c := range cases {
		if got := windowMoreElevated(c.win, c.ops); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
