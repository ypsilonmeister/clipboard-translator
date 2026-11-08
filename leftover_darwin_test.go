//go:build darwin

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// A recorded process whose parent is still alive (here: this test) is not a
// leftover, so stopLeftoverServer must leave it running and drop the record.
func TestStopLeftoverServerSparesLivingParentsChild(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	file := filepath.Join(t.TempDir(), "llama-server-1.pid")
	recordChild(file, cmd)
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("record not written: %v", err)
	}
	stopLeftoverServer(file)
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("a process with a living parent must not be killed: %v", err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record must be removed after the check, stat: %v", err)
	}
	stopLeftoverServer(file) // no record: a harmless no-op
}
