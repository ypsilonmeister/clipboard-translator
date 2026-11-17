package platform

import (
	"encoding/binary"
	"errors"
	"syscall"
	"unsafe"
)

var (
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
	procGetCurrentProcess        = kernel32.NewProc("GetCurrentProcess")
	procOpenProcessToken         = advapi32.NewProc("OpenProcessToken")
	procGetTokenInformation      = advapi32.NewProc("GetTokenInformation")
)

const (
	processQueryLimitedInformation = 0x1000
	tokenQuery                     = 0x0008
	tokenIntegrityLevel            = 25 // TOKEN_INFORMATION_CLASS TokenIntegrityLevel
)

// windowElevated reports whether the foreground window's process runs at a
// higher integrity level than this one (an administrator window), in which
// case SendInput's Ctrl+C is dropped without any error.
func windowElevated(win uintptr) bool {
	return windowMoreElevated(win, integrityOps{
		windowLevel: win32TokenCalls.windowLevel,
		selfLevel: func() (uint32, bool) {
			h, _, _ := procGetCurrentProcess.Call() // a pseudo handle: nothing to close
			// Our own token always opens; should it not, the level is unknown and
			// only a window we are refused access to counts as higher.
			level, _, ok := win32TokenCalls.processLevel(h)
			return level, ok
		},
	})
}

// accessDenied tells a refusal from every other failure of a system call.
func accessDenied(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}

var win32TokenCalls = tokenCalls{
	pidOf: func(win uintptr) uint32 {
		var pid uint32
		procGetWindowThreadProcessId.Call(win, uintptr(unsafe.Pointer(&pid)))
		return pid
	},
	openProcess: func(pid uint32) (uintptr, bool) {
		h, _, err := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
		return h, h == 0 && accessDenied(err)
	},
	openToken: func(process uintptr) (uintptr, bool) {
		var tok syscall.Handle
		r, _, err := procOpenProcessToken.Call(process, tokenQuery, uintptr(unsafe.Pointer(&tok)))
		if r == 0 {
			return 0, accessDenied(err)
		}
		return uintptr(tok), false
	},
	level:       tokenLevel,
	closeHandle: func(h uintptr) { syscall.CloseHandle(syscall.Handle(h)) },
}

// tokenLevel reads the integrity level of an open token.
func tokenLevel(tok uintptr) (uint32, bool) {
	// TOKEN_MANDATORY_LABEL followed by the SID its pointer refers to. The words
	// give the buffer the alignment of that struct (a byte array promises none).
	var words [16]uint64
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*8)
	var n uint32
	if r, _, _ := procGetTokenInformation.Call(tok, tokenIntegrityLevel, uintptr(unsafe.Pointer(&words[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n))); r == 0 {
		return 0, false
	}
	if int(n) > len(buf) {
		return 0, false
	}
	// The label's SID pointer points into the buffer itself (after the struct):
	// turn it into an offset instead of dereferencing a raw address.
	sidAddr := *(*uintptr)(unsafe.Pointer(&words[0]))
	return integrityFromLabel(buf[:n], sidAddr-uintptr(unsafe.Pointer(&words[0])))
}

// integrityFromLabel parses a TOKEN_MANDATORY_LABEL buffer whose SID starts at
// offset sid: the integrity level is the SID's last sub-authority (its RID).
// SID layout: revision, sub-authority count, 6-byte authority, then the 32-bit
// sub-authorities.
func integrityFromLabel(buf []byte, sid uintptr) (uint32, bool) {
	const labelSize = unsafe.Sizeof(uintptr(0)) + 4 // SID pointer, attributes
	if sid < labelSize || sid > uintptr(len(buf)) || len(buf)-int(sid) < 8 {
		return 0, false
	}
	s := buf[sid:]
	count := int(s[1])
	if count == 0 || len(s) < 8+4*count {
		return 0, false
	}
	return binary.LittleEndian.Uint32(s[8+4*(count-1):]), true
}
