//go:build darwin

package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

// An orphaned process (reparented to launchd, ppid == 1) matching the recorded
// executable path is a leftover from an earlier run, so stopLeftoverServer must
// terminate it and remove the record.
func TestStopLeftoverServerKillsOrphanedChild(t *testing.T) {
	// A helper shell process starts sleep in the background and exits immediately,
	// reparenting the sleep process to launchd (ppid == 1).
	cmd := exec.Command("/bin/sh", "-c", "/bin/sleep 30 & echo $!")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	pidStr := strings.TrimSpace(string(out))
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 1 {
		t.Fatalf("unexpected pid %q: %v", pidStr, err)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()

	// Wait for the child process to be reparented to launchd (ppid == 1).
	reparented := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		psOut, err := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
		if err == nil && strings.TrimSpace(string(psOut)) == "1" {
			reparented = true
			break
		}
	}
	if !reparented {
		t.Skip("child process was not reparented to launchd in time")
	}

	file := filepath.Join(t.TempDir(), "llama-server-1.pid")
	data, err := encodeChildRecord(childRecord{PID: pid, Path: "/bin/sleep"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}

	stopLeftoverServer(file)

	// The orphaned process must have been killed.
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("orphaned process must be killed, syscall.Kill(0) = %v", err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record must be removed after the check, stat: %v", err)
	}
}
