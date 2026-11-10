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
