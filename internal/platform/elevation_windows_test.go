package platform

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

// label builds a TOKEN_MANDATORY_LABEL buffer: the struct, then a SID with the
// given sub-authorities.
func label(subs ...uint32) ([]byte, uintptr) {
	const sid = 16
	buf := make([]byte, sid+8+4*len(subs))
	buf[sid], buf[sid+1] = 1, byte(len(subs))
	buf[sid+7] = 16 // SECURITY_MANDATORY_LABEL_AUTHORITY
	for i, s := range subs {
		binary.LittleEndian.PutUint32(buf[sid+8+4*i:], s)
	}
	return buf, sid
}

func TestIntegrityFromLabel(t *testing.T) {
	buf, sid := label(0x3000)
	if level, ok := integrityFromLabel(buf, sid); !ok || level != 0x3000 {
		t.Fatalf("level=%#x ok=%v, want 0x3000", level, ok)
	}
	// The level is the last sub-authority, whatever comes before it.
	buf, sid = label(7, 0x2000)
	if level, ok := integrityFromLabel(buf, sid); !ok || level != 0x2000 {
		t.Fatalf("level=%#x ok=%v, want 0x2000", level, ok)
	}
	empty, _ := label()
	for name, c := range map[string]struct {
		buf []byte
		sid uintptr
	}{
		"no sub-authority":           {empty, sid},
		"sid inside the struct":      {buf, 4},
		"sid past the buffer":        {buf, uintptr(len(buf)) + 8},
		"sid address below buffer":   {buf, ^uintptr(0) - 3},
		"sub-authorities cut off":    {buf[:len(buf)-1], sid},
		"header cut off":             {buf[:sid+4], sid},
		"empty":                      {nil, 0},
		"sid at the very end":        {buf, uintptr(len(buf))},
		"label smaller than pointer": {buf[:unsafe.Sizeof(uintptr(0))], sid},
	} {
		if level, ok := integrityFromLabel(c.buf, c.sid); ok {
			t.Errorf("%s: got level %#x, want a failure", name, level)
		}
	}
}

// The real calls, on this process only (no window, no desktop): they must give
// a level, and it must be one of the mandatory-label RIDs.
func TestTokenIntegrityOfThisProcess(t *testing.T) {
	h, _, _ := procGetCurrentProcess.Call()
	level, ok := tokenIntegrity(h)
	if !ok {
		t.Fatal("cannot read this process's integrity level")
	}
	if level < 0x1000 || level > 0x5000 {
		t.Fatalf("level = %#x, want a mandatory level between low (0x1000) and protected (0x5000)", level)
	}
	again, ok := tokenIntegrity(h)
	if !ok || again != level {
		t.Fatalf("second read = %#x ok=%v, want %#x", again, ok, level)
	}
}

func TestWindowElevatedWithoutAWindow(t *testing.T) {
	if windowElevated(0) {
		t.Fatal("no window is not an administrator window")
	}
	if windowElevated(^uintptr(0) - 1) { // not a window handle: the pid lookup fails
		t.Fatal("an unknown window must not count as an administrator window")
	}
}
