package app

import (
	"os"
	"syscall"
	"testing"
)

// Windows requires the sparse attribute before extending a file without allocating it.
func truncateSparse(f *os.File, size int64) error {
	var returned uint32
	const fsctlSetSparse = 0x900c4
	if err := syscall.DeviceIoControl(syscall.Handle(f.Fd()), fsctlSetSparse, nil, 0, nil, 0, &returned, nil); err != nil {
		return err
	}
	return f.Truncate(size)
}

func TestSparseModelFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "model")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const size = int64(4 << 30)
	if err := truncateSparse(f, size); err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	var native syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &native); err != nil {
		t.Fatal(err)
	}
	const fileAttributeSparseFile = 0x200
	if info.Size() != size || native.FileAttributes&fileAttributeSparseFile == 0 {
		t.Fatalf("size=%d attributes=%#x: expected sparse 4 GiB file", info.Size(), native.FileAttributes)
	}
}
