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
			h, _, _ := procGetCurrentProcess.Call()
			return tokenIntegrity(h)
		},
	})
}

// tokenIntegrity reads the integrity level (the last sub-authority of the
// token's mandatory label SID) of the process handle h.
func tokenIntegrity(process uintptr) (uint32, bool) {
	var tok syscall.Handle
	if r, _, _ := procOpenProcessToken.Call(process, tokenQuery, uintptr(unsafe.Pointer(&tok))); r == 0 {
		return 0, false
	}
	defer syscall.CloseHandle(tok)
	var buf [128]byte // TOKEN_MANDATORY_LABEL plus a short SID
	var n uint32
	if r, _, _ := procGetTokenInformation.Call(uintptr(tok), tokenIntegrityLevel, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n))); r == 0 {
		return 0, false
	}
	// The label's SID pointer points into buf itself (after the struct): parse it
	// there instead of dereferencing a raw address.
	sid := int(binary.LittleEndian.Uint64(buf[:8]) - uint64(uintptr(unsafe.Pointer(&buf[0]))))
	if sid < 16 || sid+8 > len(buf) {
		return 0, false
	}
	count := int(buf[sid+1]) // SID: revision, sub-authority count, 6-byte authority, then the sub-authorities
	last := sid + 8 + 4*(count-1)
	if count == 0 || last+4 > len(buf) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(buf[last:]), true
}
