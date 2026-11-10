//go:build windows

package platform

import (
	"errors"
	"syscall"
	"unsafe"
)

const (
	hkeyCurrentUser = 0x80000001
	keyQueryValue   = 0x0001
	keySetValue     = 0x0002
	regSZ           = 1
	errorFileNotFnd = 2
)

var (
	advapi32          = syscall.NewLazyDLL("advapi32.dll")
	regOpenKeyExW     = advapi32.NewProc("RegOpenKeyExW")
	regCreateKeyExW   = advapi32.NewProc("RegCreateKeyExW")
	regQueryValueExW  = advapi32.NewProc("RegQueryValueExW")
	regSetValueExW    = advapi32.NewProc("RegSetValueExW")
	regDeleteValueW   = advapi32.NewProc("RegDeleteValueW")
	regDeleteKeyW     = advapi32.NewProc("RegDeleteKeyW")
	regCloseKey       = advapi32.NewProc("RegCloseKey")
	appAutostart      = autostartStore{key: `Software\Microsoft\Windows\CurrentVersion\Run`, name: "ClipboardTranslator"}
	errAutostartValue = errors.New("registry value has unexpected type")
)

// autostartStore manages one per-user "run at logon" value under HKCU. The key
// path is a field only so tests can use a scratch key instead of the real Run key.
type autostartStore struct{ key, name string }

func (a autostartStore) open(access uint32) (uintptr, bool) {
	var h uintptr
	r, _, _ := regOpenKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(u16(a.key))), 0, uintptr(access), uintptr(unsafe.Pointer(&h)))
	return h, r == 0
}

// Enabled reports whether the value exists and launches exactly command.
// A value pointing at another location (e.g. the folder was moved) counts as
// disabled, so toggling it on repairs the path.
func (a autostartStore) Enabled(command string) bool {
	v, ok := a.Get()
	return ok && v == command
}

// Get reads the value as a string. ok is false when the key or value is
// missing or is not a REG_SZ.
func (a autostartStore) Get() (value string, ok bool) {
	h, opened := a.open(keyQueryValue)
	if !opened {
		return "", false
	}
	defer regCloseKey.Call(h)
	name := uintptr(unsafe.Pointer(u16(a.name)))
	var typ, size uint32
	r, _, _ := regQueryValueExW.Call(h, name, 0, uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&size)))
	if r != 0 || typ != regSZ || size == 0 {
		return "", false
	}
	buf := make([]uint16, size/2+1)
	size = uint32(len(buf) * 2)
	r, _, _ = regQueryValueExW.Call(h, name, 0, uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return "", false
	}
	return syscall.UTF16ToString(buf[:size/2]), true
}

// Set creates or overwrites the value with command.
func (a autostartStore) Set(command string) error {
	var h uintptr
	r, _, _ := regCreateKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(u16(a.key))), 0, 0, 0, keySetValue, 0,
		uintptr(unsafe.Pointer(&h)), 0)
	if r != 0 {
		return syscall.Errno(r)
	}
	defer regCloseKey.Call(h)
	data, err := syscall.UTF16FromString(command)
	if err != nil {
		return err
	}
	r, _, _ = regSetValueExW.Call(h, uintptr(unsafe.Pointer(u16(a.name))), 0, regSZ,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// Clear removes the value. A missing value is not an error.
func (a autostartStore) Clear() error {
	h, ok := a.open(keySetValue)
	if !ok {
		return nil
	}
	defer regCloseKey.Call(h)
	r, _, _ := regDeleteValueW.Call(h, uintptr(unsafe.Pointer(u16(a.name))))
	if r != 0 && r != errorFileNotFnd {
		return syscall.Errno(r)
	}
	return nil
}
