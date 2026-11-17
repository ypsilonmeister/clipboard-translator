package platform

import "testing"

// fakeTokenCalls scripts the system calls of the integrity check and counts
// the handles that are open.
type fakeTokenCalls struct {
	pid                        uint32
	processDenied, processFail bool
	tokenDenied, tokenFail     bool
	level                      uint32
	levelFail                  bool
	open                       map[uintptr]bool
	next                       uintptr
}

func (f *fakeTokenCalls) handle() uintptr {
	f.next++
	if f.open == nil {
		f.open = map[uintptr]bool{}
	}
	f.open[f.next] = true
	return f.next
}

func (f *fakeTokenCalls) calls() tokenCalls {
	return tokenCalls{
		pidOf: func(uintptr) uint32 { return f.pid },
		openProcess: func(uint32) (uintptr, bool) {
			if f.processDenied || f.processFail {
				return 0, f.processDenied
			}
			return f.handle(), false
		},
		openToken: func(uintptr) (uintptr, bool) {
			if f.tokenDenied || f.tokenFail {
				return 0, f.tokenDenied
			}
			return f.handle(), false
		},
		level:       func(uintptr) (uint32, bool) { return f.level, !f.levelFail },
		closeHandle: func(h uintptr) { delete(f.open, h) },
	}
}

// Which failure makes a window count as an administrator window: being refused
// the process or its token does, every other failure does not, and no handle
// stays open on any path.
func TestWindowElevationByFailureKind(t *testing.T) {
	const medium, high = 0x2000, 0x3000
	cases := []struct {
		name string
		f    fakeTokenCalls
		want bool
	}{
		{"higher level", fakeTokenCalls{pid: 9, level: high}, true},
		{"same level", fakeTokenCalls{pid: 9, level: medium}, false},
		{"no process for the window", fakeTokenCalls{pid: 0, level: high}, false},
		{"process refused", fakeTokenCalls{pid: 9, processDenied: true}, true},
		{"process gone", fakeTokenCalls{pid: 9, processFail: true}, false},
		{"token refused", fakeTokenCalls{pid: 9, tokenDenied: true}, true},
		{"token open fails otherwise", fakeTokenCalls{pid: 9, tokenFail: true, level: high}, false},
		{"token level cannot be read", fakeTokenCalls{pid: 9, levelFail: true, level: high}, false},
	}
	for _, c := range cases {
		f := c.f
		ops := integrityOps{
			windowLevel: f.calls().windowLevel,
			selfLevel:   func() (uint32, bool) { return medium, true },
		}
		if got := windowMoreElevated(5, ops); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		if len(f.open) != 0 {
			t.Errorf("%s: %d handles left open", c.name, len(f.open))
		}
	}
}

// Our own token: a refusal there is "unknown", and then only a window we are
// refused access to counts as higher.
func TestWindowElevationWhenOwnLevelIsUnknown(t *testing.T) {
	self := fakeTokenCalls{tokenDenied: true}
	selfLevel := func() (uint32, bool) {
		level, _, ok := self.calls().processLevel(1)
		return level, ok
	}
	known := fakeTokenCalls{pid: 9, level: 0x3000}
	if windowMoreElevated(5, integrityOps{windowLevel: known.calls().windowLevel, selfLevel: selfLevel}) {
		t.Fatal("a level cannot be compared with an unknown own level")
	}
	refused := fakeTokenCalls{pid: 9, tokenDenied: true}
	if !windowMoreElevated(5, integrityOps{windowLevel: refused.calls().windowLevel, selfLevel: selfLevel}) {
		t.Fatal("a refused window counts as higher whatever our own level is")
	}
	if len(known.open)+len(refused.open)+len(self.open) != 0 {
		t.Fatal("handles left open")
	}
}
