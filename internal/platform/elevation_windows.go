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
		windowLevel: func(win uintptr) (uint32, bool, bool) {
			var pid uint32
			procGetWindowThreadProcessId.Call(win, uintptr(unsafe.Pointer(&pid)))
			if pid == 0 {
				return 0, false, false
			}
			h, _, err := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
			if h == 0 {
				return 0, errors.Is(err, syscall.ERROR_ACCESS_DENIED), false
			}
			defer syscall.CloseHandle(syscall.Handle(h))
			level, ok := tokenIntegrity(h)
			return level, false, ok
		},
		selfLevel: func() (uint32, bool) {
			h, _, _ := procGetCurrentProcess.Call() // a pseudo handle: nothing to close
			return tokenIntegrity(h)
		},
	})
}

// tokenIntegrity reads the integrity level of the process handle h. A token
// that cannot be opened or read is "unknown", not "higher": only a process that
// cannot be opened at all is taken for an elevated one (see windowMoreElevated).
func tokenIntegrity(process uintptr) (uint32, bool) {
	var tok syscall.Handle
	if r, _, _ := procOpenProcessToken.Call(process, tokenQuery, uintptr(unsafe.Pointer(&tok))); r == 0 {
		return 0, false
	}
	defer syscall.CloseHandle(tok)
	// TOKEN_MANDATORY_LABEL followed by the SID its pointer refers to. The words
	// give the buffer the alignment of that struct (a byte array promises none).
	var words [16]uint64
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*8)
	var n uint32
	if r, _, _ := procGetTokenInformation.Call(uintptr(tok), tokenIntegrityLevel, uintptr(unsafe.Pointer(&words[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n))); r == 0 {
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
